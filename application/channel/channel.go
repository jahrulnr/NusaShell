// Package appchannel connects external chats (WhatsApp, Telegram) to rooms.
// A chat is linked to one room; a message from it becomes a turn in that
// room — run through the same turn path the web uses, so the web sees it
// live — and the room's final reply goes back to the chat. A few slash
// commands are handled here instead of reaching the agent.
//
// Channels own their wire, identity, allowlist, and text format. This
// package owns the room side: linking, commands, routing of replies, and
// approval pings. It never sees thoughts or tool output leave the web.
package appchannel

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Runner runs turns the way the web does. The WebSocket hub implements it.
type Runner interface {
	// Run opens a send turn and blocks until it ends. An empty convID
	// mints a room; the room id comes back.
	Run(ctx context.Context, convID, providerID, modelID, content string, attachments []valueobject.Attachment, opts appjob.SendOptions, observe event.TurnEventHandler) (string, appconversation.SendResult, error)
	// Steer adds text to the room's running turn; false when none runs.
	Steer(convID, text string) bool
	// Cancel stops the room's running turn; false when none runs.
	Cancel(convID string) bool
	// Busy reports whether a turn of the room runs.
	Busy(convID string) bool
}

// Rooms is what this use case reads and changes on rooms.
// *appconversation.ConversationApp satisfies it.
type Rooms interface {
	Info(ctx context.Context, id string) (drepo.ConversationInfo, error)
	List(ctx context.Context) ([]drepo.ConversationInfo, error)
	History(ctx context.Context, id string, start, end int) ([]entity.Message, error)
	Annotate(ctx context.Context, convID string, a appconversation.Annotation) error
	PendingApprovals(convID string) []valueobject.ApprovalRequest
	Approve(convID, requestID, optionID string) error
	AnswerElicitation(convID, id string, a valueobject.ElicitationAnswer) error
	// AgentCommands are the slash commands the room's agent session offers.
	AgentCommands(convID string) []dservice.AgentCommand
	// Roots are the folders a room's turns work in and the storage that
	// reaches them; a reply's file links resolve only inside them.
	Roots(ctx context.Context, p appconversation.Place) (drepo.Workspace, []string, error)
	// RecordDelivery notes on a reply the files that went to a chat with
	// it and the chat's receipt.
	RecordDelivery(ctx context.Context, convID, msgID string, files []valueobject.Attachment, receipt valueobject.Receipt) error
}

// Runtime is what a turn runs on: an agent, or a provider and model.
type Runtime struct {
	AgentID    string
	ProviderID string
	ModelID    string
}

func (r Runtime) empty() bool { return r.AgentID == "" && r.ProviderID == "" }

func (r Runtime) String() string {
	if r.AgentID != "" {
		if r.ModelID != "" {
			return r.AgentID + "/" + r.ModelID + " (ACP)"
		}
		return r.AgentID + " (ACP)"
	}
	return r.ProviderID + "/" + r.ModelID + " (API)"
}

// Choice is one runtime a chat may switch to with /use.
type Choice struct {
	Runtime Runtime
	Name    string
}

// Catalog answers which runtimes exist and which one a new chat starts on.
type Catalog interface {
	// Default is the runtime for a room opened from a chat.
	Default(ctx context.Context) (Runtime, error)
	// Choices lists the runtimes /use can pick.
	Choices(ctx context.Context) ([]Choice, error)
}

// App is the channel use case.
type App struct {
	links    drepo.ChannelLink
	index    drepo.ChatIndex
	inbound  drepo.InboundLog
	uploads  drepo.Uploads
	projects drepo.Project
	rooms    Rooms
	runner   Runner
	catalog  Catalog
	log      *slog.Logger

	mu       sync.Mutex
	channels map[string]dservice.Channel
	next     map[string]Runtime             // /use picks waiting for the room's next turn
	origins  map[string]valueobject.ChatRef // the chat each running turn came from
	polls    map[string]*pollWaiter         // polls sent for running turns' requests
	// sent maps a chat's message id ("channel|id") to the reply it
	// carried, for receipts; sentOrder forgets the oldest first.
	sent      map[string]*sentReply
	sentOrder []string
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// maxInboundFiles bounds the files one chat message brings into a turn.
const maxInboundFiles = 6

// maxQuoteRunes bounds the quoted message a reply carries into the turn.
const maxQuoteRunes = 120

// New wires the use case. Register channels before Start. uploads keeps
// the files chats send until their turn reads them; projects are what a
// chat can name besides rooms.
func New(links drepo.ChannelLink, inbound drepo.InboundLog, uploads drepo.Uploads, projects drepo.Project, rooms Rooms, runner Runner, catalog Catalog, log *slog.Logger) *App {
	if log == nil {
		log = slog.Default()
	}
	return &App{links: links, inbound: inbound, uploads: uploads, projects: projects, rooms: rooms, runner: runner, catalog: catalog,
		log: log.With("component", "channels"), channels: map[string]dservice.Channel{}, next: map[string]Runtime{},
		origins: map[string]valueobject.ChatRef{}, polls: map[string]*pollWaiter{}, sent: map[string]*sentReply{}}
}

// Origin is the chat the room's running turn came from; false when no
// chat turn runs there.
func (a *App) Origin(convID string) (valueobject.ChatRef, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.origins[convID]
	return c, ok
}

// Register adds a channel.
func (a *App) Register(ch dservice.Channel) {
	a.mu.Lock()
	a.channels[ch.Name()] = ch
	a.mu.Unlock()
}

// Channel returns a registered channel.
func (a *App) Channel(name string) (dservice.Channel, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch, ok := a.channels[name]
	return ch, ok
}

// Statuses lists every registered channel's status, by name.
func (a *App) Statuses() []dservice.ChannelStatus {
	a.mu.Lock()
	list := make([]dservice.Channel, 0, len(a.channels))
	for _, ch := range a.channels {
		list = append(list, ch)
	}
	a.mu.Unlock()
	out := make([]dservice.ChannelStatus, 0, len(list))
	for _, ch := range list {
		out = append(out, ch.Status())
	}
	slices.SortFunc(out, func(x, y dservice.ChannelStatus) int { return strings.Compare(x.Name, y.Name) })
	return out
}

// Start starts every channel and the dedupe-log pruning. It returns once
// the channels have started their own background work.
func (a *App) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Lock()
	list := make([]dservice.Channel, 0, len(a.channels))
	for _, ch := range a.channels {
		list = append(list, ch)
	}
	a.mu.Unlock()
	for _, ch := range list {
		if err := ch.Start(ctx, a); err != nil {
			a.log.Warn("channel failed to start", "channel", ch.Name(), "error", err)
		}
	}
	a.wg.Go(func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			_ = a.inbound.Prune(context.Background(), time.Now().Add(-7*24*time.Hour))
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})
}

// Stop stops every channel and waits for in-flight chat turns to report.
func (a *App) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Lock()
	list := make([]dservice.Channel, 0, len(a.channels))
	for _, ch := range a.channels {
		list = append(list, ch)
	}
	a.mu.Unlock()
	for _, ch := range list {
		_ = ch.Stop()
	}
	a.wg.Wait()
}

// HandleInbound takes one chat message. The work runs on its own goroutine
// so a slow turn never stalls the channel's receive loop.
func (a *App) HandleInbound(_ context.Context, in dservice.InboundMessage) {
	a.wg.Go(func() { a.process(context.Background(), in) })
}

// reply sends text to the chat a message came from; failures are logged.
func (a *App) reply(ctx context.Context, chat valueobject.ChatRef, text string) {
	ch, ok := a.Channel(chat.Channel())
	if !ok {
		return
	}
	if _, err := ch.Send(ctx, chat, text); err != nil {
		a.log.Warn("could not reply on channel", "channel", chat.Channel(), "error", err)
	}
}

func (a *App) process(ctx context.Context, in dservice.InboundMessage) {
	chat := in.Chat
	if in.MessageID != "" {
		fresh, err := a.inbound.Claim(ctx, chat.Channel(), in.MessageID)
		if err != nil {
			a.log.Warn("could not record an inbound message", "error", err)
			return
		}
		if !fresh {
			return
		}
	}
	if in.Vote != nil {
		a.vote(ctx, in)
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" && len(in.Files) == 0 {
		return
	}
	convID := ""
	if l, err := a.links.Find(ctx, chat); err == nil {
		convID = l.ConversationID()
	} else if apperror.As(err).StatusCode() != apperror.StatusNotFound {
		a.log.Warn("could not read a channel link", "error", err)
		return
	}
	if cmd, args, ok := parseCommand(text); ok {
		if a.command(ctx, in, convID, cmd, args) {
			return
		}
	}
	prefix := ""
	if in.Group && strings.TrimSpace(in.SenderName) != "" {
		prefix = in.SenderName + ": "
	}
	// A reply carries what it replies to, so a short answer such as "yes"
	// still means something to the room.
	if q := strings.Join(strings.Fields(in.Quote), " "); q != "" {
		prefix += "[In reply to: " + truncateRunes(q, maxQuoteRunes) + "]\n"
	}
	body, shift := text, 0
	if prefix != "" {
		body, shift = strings.TrimSpace(prefix+text), len(prefix)
	}
	var files []valueobject.Attachment
	lost := 0
	for i, f := range in.Files {
		if i >= maxInboundFiles {
			lost += len(in.Files) - i
			break
		}
		name := f.Name
		if name == "" {
			name = fmt.Sprintf("attachment-%d", i+1)
		}
		mediaType := f.MediaType
		if mediaType == "" {
			mediaType = http.DetectContentType(f.Data)
		}
		_, att, err := a.uploads.Save(ctx, name, mediaType, f.Data)
		if err != nil {
			a.log.Warn("could not keep a file a chat sent", "channel", chat.Channel(), "error", err)
			lost++
			continue
		}
		files = append(files, att)
	}
	if lost > 0 {
		body = strings.TrimSpace(body + fmt.Sprintf("\n\n[%d more file(s) the sender attached could not be passed on.]", lost))
	}
	var refs []valueobject.Reference
	if in.Inline {
		refs = a.inlineReferences(ctx, text, body, shift)
	}
	// A message while the room is working joins the running turn instead
	// of waiting behind it. Steering carries text only, so a message with
	// files or references queues its own turn.
	if convID != "" && len(files) == 0 && len(refs) == 0 && a.runner.Busy(convID) && a.runner.Steer(convID, body) {
		return
	}
	a.runTurn(ctx, in, convID, body, files, refs)
}

// runTurn opens (or continues) the chat's room and runs one turn there,
// then routes the final reply.
func (a *App) runTurn(ctx context.Context, in dservice.InboundMessage, convID, body string, files []valueobject.Attachment, refs []valueobject.Reference) {
	chat := in.Chat
	fresh := convID == ""
	rt := a.takeNext(convID)
	if fresh {
		if pick := a.takeNext("chat:" + chat.Key()); !pick.empty() {
			rt = pick
		}
		conv, err := aggregate.NewConversation("")
		if err != nil {
			a.reply(ctx, chat, "Could not open a thread: "+err.Error())
			return
		}
		convID = conv.ID()
		if rt.empty() {
			if rt, err = a.catalog.Default(ctx); err != nil || rt.empty() {
				a.reply(ctx, chat, "No model or agent is set up yet. Add a provider or an agent in NusaShell first.")
				return
			}
		}
		if err := a.link(ctx, in, convID); err != nil {
			a.reply(ctx, chat, "Could not link this chat: "+err.Error())
			return
		}
	}
	routing := valueobject.DefaultRouting()
	if !fresh {
		if info, err := a.rooms.Info(ctx, convID); err == nil && info.Routing != nil {
			routing = *info.Routing
		}
	}
	ch, ok := a.Channel(chat.Channel())
	if !ok {
		return
	}
	stopTyping := ch.Typing(ctx, chat)
	// The turn's questions reach the chat it came from when the room
	// notifies; one answered elsewhere retires its poll.
	observe := func(ev event.TurnEvent) {
		switch ev.Kind() {
		case enum.TurnEventApproval:
			if routing.NotifyApprovals {
				a.askApproval(ctx, chat, in.SenderID, convID, ev.Approval())
			}
		case enum.TurnEventElicitation:
			if routing.NotifyApprovals {
				a.askElicitation(ctx, chat, in.SenderID, convID, ev.Elicitation())
			}
		case enum.TurnEventApprovalResolved:
			id := ev.Approval().ID()
			a.dropPolls(func(w *pollWaiter) bool { return w.approval == id })
		case enum.TurnEventElicitationResolved:
			id := ev.Elicitation().ID()
			a.dropPolls(func(w *pollWaiter) bool { return w.elicitation == id })
		}
	}
	opts := appjob.SendOptions{Agent: rt.AgentID, References: refs}
	a.mu.Lock()
	a.origins[convID] = chat
	a.mu.Unlock()
	_, _, err := a.runner.Run(ctx, convID, rt.ProviderID, rt.ModelID, body, files, opts, observe)
	a.mu.Lock()
	if a.origins[convID] == chat {
		delete(a.origins, convID)
	}
	a.mu.Unlock()
	a.dropPolls(func(w *pollWaiter) bool { return w.convID == convID })
	stopTyping()
	if fresh && err == nil {
		title := chatTitle(in)
		_ = a.rooms.Annotate(ctx, convID, appconversation.Annotation{Title: &title})
	}
	if err != nil {
		if apperror.As(err).StatusCode() == 408 {
			a.reply(ctx, chat, "Stopped.")
			return
		}
		a.reply(ctx, chat, "That did not go through: "+apperror.As(err).Error())
		return
	}
	a.deliver(ctx, convID, chat, routing)
	a.syncMenus(ctx, convID)
}

// link points the chat at a room.
func (a *App) link(ctx context.Context, in dservice.InboundMessage, convID string) error {
	display := strings.TrimSpace(in.ChatName)
	if display == "" {
		display = strings.TrimSpace(in.SenderName)
	}
	l, err := aggregate.NewChannelLink(in.Chat, convID, display, in.Group, time.Now())
	if err != nil {
		return err
	}
	return a.links.Save(ctx, l)
}

// deliver sends the room's final reply where its routing says: back to the
// origin chat, and to the room's other chats when mirroring. origin may be
// the zero value for a turn that came from the web.
func (a *App) deliver(ctx context.Context, convID string, origin valueobject.ChatRef, routing valueobject.Routing) {
	var chats []valueobject.ChatRef
	if origin.Channel() != "" && routing.ReplyToOrigin {
		chats = append(chats, origin)
	}
	if routing.MirrorFinal {
		if links, err := a.links.ListByConversation(ctx, convID); err == nil {
			for _, l := range links {
				if l.Chat().Key() != origin.Key() {
					chats = append(chats, l.Chat())
				}
			}
		}
	}
	if len(chats) > 0 {
		_ = a.sendFinal(ctx, convID, chats)
	}
}

// sendFinal sends the room's final reply to chats and notes the trip on
// the reply: the files that went with it, kept as uploads so the web shows
// them, and the ids receipts will name. It fails when the room has no
// reply, or when every chat failed.
func (a *App) sendFinal(ctx context.Context, convID string, chats []valueobject.ChatRef) error {
	msg, ok := a.finalReply(ctx, convID)
	if !ok {
		return apperror.New(409, "the thread has no reply to send", nil)
	}
	out := a.prepare(ctx, convID, strings.TrimSpace(msg.Content()), msg.Media())
	sent := map[string]dservice.ChannelFile{}
	var errs []error
	for _, chat := range chats {
		ids, files, err := a.sendReply(ctx, chat, out)
		if err != nil {
			a.log.Warn("could not reply on channel", "channel", chat.Channel(), "error", err)
			errs = append(errs, err)
		}
		a.rememberSent(chat.Channel(), ids, convID, msg.ID())
		for _, f := range files {
			sent[f.Name] = f
		}
	}
	var atts []valueobject.Attachment
	for _, f := range out.files { // in the reply's order
		if _, ok := sent[f.Name]; !ok {
			continue
		}
		_, att, err := a.uploads.Save(ctx, f.Name, f.MediaType, f.Data)
		if err != nil {
			a.log.Warn("could not keep a file a reply sent", "file", f.Name, "error", err)
			continue
		}
		atts = append(atts, att)
	}
	if len(atts) > 0 {
		if err := a.rooms.RecordDelivery(ctx, convID, msg.ID(), atts, valueobject.ReceiptNone); err != nil {
			a.log.Warn("could not note the files a reply sent", "conversation", convID, "error", err)
		}
	}
	if len(errs) == len(chats) && len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// ForgetRoom unlinks every chat of a deleted room, so their next message
// opens a new thread instead of reviving the old id.
func (a *App) ForgetRoom(convID string) {
	if err := a.links.DeleteByConversation(context.Background(), convID); err != nil {
		a.log.Warn("could not unlink a deleted room's chats", "conversation", convID, "error", err)
	}
	a.mu.Lock()
	delete(a.next, convID)
	a.mu.Unlock()
}

// LinkView is a chat linked to a room, for the web.
type LinkView struct {
	Channel string    `json:"channel"`
	ChatID  string    `json:"chatId"`
	Thread  string    `json:"thread,omitempty"`
	Key     string    `json:"key"`
	Display string    `json:"display"`
	Group   bool      `json:"group"`
	Since   time.Time `json:"since"`
}

// Links lists the chats linked to a room.
func (a *App) Links(ctx context.Context, convID string) ([]LinkView, error) {
	links, err := a.links.ListByConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	return linkViews(links), nil
}

// Chats lists every chat linked to any room — the chats NusaShell can
// write to.
func (a *App) Chats(ctx context.Context) ([]LinkView, error) {
	links, err := a.links.List(ctx)
	if err != nil {
		return nil, err
	}
	return linkViews(links), nil
}

func linkViews(links []aggregate.ChannelLink) []LinkView {
	out := []LinkView{}
	for _, l := range links {
		c := l.Chat()
		out = append(out, LinkView{Channel: c.Channel(), ChatID: c.ChatID(), Thread: c.Thread(), Key: c.Key(),
			Display: l.Display(), Group: l.IsGroup(), Since: l.CreatedAt()})
	}
	return out
}

// Send writes text (Markdown) to a chat.
func (a *App) Send(ctx context.Context, chat valueobject.ChatRef, text string) error {
	ch, ok := a.Channel(chat.Channel())
	if !ok {
		return apperror.New(400, "channel "+chat.Channel()+" is not available", nil)
	}
	_, err := ch.Send(ctx, chat, text)
	return err
}

// SendFinal writes a room's final reply to a chat that need not be linked
// to the room — an automation's result chat.
func (a *App) SendFinal(ctx context.Context, convID string, chat valueobject.ChatRef) error {
	if _, ok := a.Channel(chat.Channel()); !ok {
		return apperror.New(400, "channel "+chat.Channel()+" is not available", nil)
	}
	return a.sendFinal(ctx, convID, []valueobject.ChatRef{chat})
}

// Unlink removes one chat's link to a room by the chat's key.
func (a *App) Unlink(ctx context.Context, convID, key string) error {
	links, err := a.links.ListByConversation(ctx, convID)
	if err != nil {
		return err
	}
	for _, l := range links {
		if l.Chat().Key() == key {
			return a.links.Delete(ctx, l.Chat())
		}
	}
	return apperror.NewNotFoundError("chat link "+key, nil)
}

// TurnFinished mirrors the final reply of a send turn that did not come
// from a chat (the web) to the room's linked chats, when the room mirrors.
// Edit and resume turns regenerate replies, not new answers — they are
// not mirrored.
func (a *App) TurnFinished(convID, kind string, external bool, err error) {
	if external {
		return
	}
	ctx := context.Background()
	a.syncMenus(ctx, convID)
	if kind != "send" || err != nil {
		return
	}
	info, ierr := a.rooms.Info(ctx, convID)
	if ierr != nil || info.Routing == nil || !info.Routing.MirrorFinal {
		return
	}
	a.deliver(ctx, convID, valueobject.ChatRef{}, *info.Routing)
}

// finalReply is the room's last assistant message with text, when the
// room's history ends on one.
func (a *App) finalReply(ctx context.Context, convID string) (entity.Message, bool) {
	msgs, err := a.rooms.History(ctx, convID, -40, 0)
	if err != nil {
		return entity.Message{}, false
	}
	for _, m := range slices.Backward(msgs) {
		if m.Role() == enum.RoleUser && m.Editable() {
			return entity.Message{}, false
		}
		if m.Role() == enum.RoleAssistant && (strings.TrimSpace(m.Content()) != "" || len(m.Media()) > 0) {
			return m, true
		}
	}
	return entity.Message{}, false
}

func (a *App) takeNext(convID string) Runtime {
	if convID == "" {
		return Runtime{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rt := a.next[convID]
	delete(a.next, convID)
	return rt
}

func approvalPing(r valueobject.ApprovalRequest) string {
	short := shortID(r.ID())
	what := r.Title()
	if what == "" {
		what = r.Action().String()
	}
	return fmt.Sprintf("🔐 The agent asks to run: %s\nReply /approve %s or /deny %s — or answer on the web.", what, short, short)
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "apr_")
	if len(id) > 6 {
		id = id[:6]
	}
	return strings.ToLower(id)
}

func chatTitle(in dservice.InboundMessage) string {
	name := strings.TrimSpace(in.ChatName)
	if name == "" {
		name = strings.TrimSpace(in.SenderName)
	}
	if name == "" {
		name = in.Chat.ChatID()
	}
	label := in.Chat.Channel()
	switch label {
	case "whatsapp":
		label = "WhatsApp"
	case "telegram":
		label = "Telegram"
	}
	return label + " · " + name
}

// syncMenus puts the room's agent commands on the "/" menu of each chat
// linked to it, for chats that have their own menu.
func (a *App) syncMenus(ctx context.Context, convID string) {
	links, err := a.links.ListByConversation(ctx, convID)
	if err != nil || len(links) == 0 {
		return
	}
	cmds := a.rooms.AgentCommands(convID)
	for _, l := range links {
		a.syncMenu(ctx, l.Chat(), cmds)
	}
}

// syncMenu sets one chat's menu to cmds; none puts it back on the
// channel's default menu.
func (a *App) syncMenu(ctx context.Context, chat valueobject.ChatRef, cmds []dservice.AgentCommand) {
	ch, ok := a.Channel(chat.Channel())
	if !ok {
		return
	}
	if menu, ok := ch.(dservice.CommandMenu); ok {
		menu.SyncCommands(ctx, chat, cmds)
	}
}
