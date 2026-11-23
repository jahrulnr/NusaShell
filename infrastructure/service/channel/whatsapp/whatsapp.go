// Package whatsapp is the WhatsApp channel: a linked device (whatsmeow)
// paired by QR code. It lets through messages its allowlist and group
// policy accept and sends replies in WhatsApp markup. The device keys live
// in their own SQLite file; settings live in the channel's settings
// document. Every message the account sees or sends is kept in the chat
// index for search, and sends to one chat are paced.
package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/channel/markup"
)

const (
	name          = "whatsapp"
	maxReplyRunes = 30000
	maxMessageAge = 2 * time.Hour
	typingEvery   = 8 * time.Second
	blockedLimit  = 12
)

// Channel is the WhatsApp adapter.
type Channel struct {
	dbPath   string
	settings drepo.ChannelSettings
	index    drepo.ChatIndex
	log      *slog.Logger
	appName  string
	pacer    *pacer

	mu         sync.Mutex
	handler    dservice.InboundHandler
	cfg        Settings
	container  *sqlstore.Container
	client     *whatsmeow.Client
	state      string
	detail     string
	qr         string
	connecting bool
	stopQR     context.CancelFunc
	blocked    []dservice.BlockedSender
	updated    time.Time
	life       context.Context
	groupNames map[string]string

	// polls keeps the option names of polls seen or sent, by chat:message,
	// so votes read back as names.
	pollMu sync.Mutex
	polls  map[string][]string
}

// New builds the channel. dbPath is the device store file; appName is how
// the device is named in the phone's linked-devices list; index keeps the
// account's messages for search.
func New(dbPath, appName string, settings drepo.ChannelSettings, index drepo.ChatIndex, log *slog.Logger) *Channel {
	if log == nil {
		log = slog.Default()
	}
	return &Channel{dbPath: dbPath, appName: appName, settings: settings, index: index, log: log.With("channel", name),
		cfg: failClosed(), state: "unpaired", life: context.Background(), groupNames: map[string]string{},
		polls: map[string][]string{}, pacer: newPacer()}
}

// current is the settings in force.
func (c *Channel) current() Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

var _ dservice.Channel = (*Channel)(nil)

func (c *Channel) Name() string { return name }

// Start opens the device store and connects a paired, enabled device.
func (c *Channel) Start(ctx context.Context, handler dservice.InboundHandler) error {
	c.mu.Lock()
	c.handler = handler
	c.life = ctx
	c.cfg = c.load(ctx, c.cfg)
	err := c.ensureClientLocked(ctx)
	paired := c.client != nil && c.client.Store.ID != nil
	enabled := c.cfg.Enabled
	c.mu.Unlock()
	if err != nil {
		c.setState("error", err.Error())
		return err
	}
	go c.retain(ctx)
	if enabled && paired {
		return c.Connect(ctx)
	}
	return nil
}

// ensureClientLocked opens the device store and client once.
func (c *Channel) ensureClientLocked(ctx context.Context) error {
	if c.client != nil {
		return nil
	}
	if c.container == nil {
		dsn := "file:" + c.dbPath + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(8000)&_pragma=journal_mode(WAL)"
		container, err := sqlstore.New(ctx, "sqlite", dsn, waLogger{log: c.log, module: "store"})
		if err != nil {
			return fmt.Errorf("open the WhatsApp device store: %w", err)
		}
		c.container = container
		wastore.SetOSInfo(c.appName, [3]uint32{0, 1, 0})
		wastore.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
	}
	device, err := c.container.GetFirstDevice(ctx)
	if err != nil {
		return err
	}
	client := whatsmeow.NewClient(device, waLogger{log: c.log, module: "client"})
	client.EnableAutoReconnect = true
	client.AddEventHandler(c.handleEvent)
	c.client = client
	if device.ID != nil {
		c.state, c.detail = "offline", "Linked as +"+device.ID.User+". Connect to receive messages."
	} else {
		c.state, c.detail = "unpaired", "Connect, then scan the QR code from WhatsApp → Linked devices."
	}
	c.updated = time.Now()
	return nil
}

// Stop fails the sends still queued, disconnects, and closes the device
// store.
func (c *Channel) Stop() error {
	c.stopPacer()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopQR != nil {
		c.stopQR()
		c.stopQR = nil
	}
	if c.client != nil {
		c.client.Disconnect()
		c.client = nil
	}
	if c.container != nil {
		_ = c.container.Close()
		c.container = nil
	}
	return nil
}

// Connect connects a paired device, or starts QR pairing.
func (c *Channel) Connect(ctx context.Context) error {
	c.mu.Lock()
	if !c.cfg.Enabled {
		c.mu.Unlock()
		return errors.New("enable WhatsApp first")
	}
	if err := c.ensureClientLocked(ctx); err != nil {
		c.mu.Unlock()
		return err
	}
	client := c.client
	if client.IsConnected() || c.connecting {
		c.mu.Unlock()
		return nil
	}
	c.connecting = true
	c.qr = ""
	c.state, c.detail = "connecting", "Connecting to WhatsApp…"
	c.updated = time.Now()
	qrCtx, cancel := context.WithCancel(c.life)
	c.stopQR = cancel
	c.mu.Unlock()
	go c.connect(qrCtx, client)
	return nil
}

func (c *Channel) connect(ctx context.Context, client *whatsmeow.Client) {
	defer c.finishConnect()
	if client.Store.ID != nil {
		if err := client.Connect(); err != nil && !client.IsConnected() {
			c.setState("error", "Could not connect to WhatsApp: "+err.Error())
		}
		return
	}
	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		c.setState("error", "Could not start pairing: "+err.Error())
		return
	}
	if err := client.Connect(); err != nil && !client.IsConnected() {
		c.setState("error", "Could not connect to WhatsApp: "+err.Error())
		return
	}
	for item := range qrChan {
		switch item.Event {
		case "code":
			c.mu.Lock()
			c.qr = item.Code
			c.state, c.detail = "pairing", "Scan the QR code from WhatsApp → Linked devices → Link a device."
			c.updated = time.Now()
			c.mu.Unlock()
		case "success":
			// Linked, not yet logged in: the client reconnects with the new
			// identity, and events.Connected marks it usable.
			c.setState("connecting", "Linked; logging in…")
		case "timeout":
			c.setState("unpaired", "Pairing timed out. Connect again for a fresh QR code.")
		case "err-scanned-without-multidevice":
			c.setState("unpaired", "Turn on linked devices on your phone, then scan again.")
		case "err-client-outdated":
			c.setState("error", "This WhatsApp client is outdated; update NusaShell.")
		default:
			if item.Error != nil {
				c.setState("error", "Pairing failed: "+item.Error.Error())
			}
		}
	}
}

func (c *Channel) finishConnect() {
	c.mu.Lock()
	c.connecting = false
	c.qr = ""
	c.mu.Unlock()
}

// Disconnect drops the connection; the device stays linked.
func (c *Channel) Disconnect() error {
	c.mu.Lock()
	if c.stopQR != nil {
		c.stopQR()
		c.stopQR = nil
	}
	if c.client != nil {
		c.client.Disconnect()
	}
	c.connecting, c.qr = false, ""
	c.state, c.detail = "disconnected", "Disconnected. Connect to resume."
	c.updated = time.Now()
	c.mu.Unlock()
	return nil
}

// Logout unlinks the device from the phone.
func (c *Channel) Logout(ctx context.Context) error {
	c.mu.Lock()
	if c.stopQR != nil {
		c.stopQR()
		c.stopQR = nil
	}
	client := c.client
	c.client = nil
	c.mu.Unlock()
	if client != nil {
		lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := client.Logout(lctx); err != nil {
			_ = client.Store.Delete(context.Background())
		}
		cancel()
		client.Disconnect()
	}
	c.mu.Lock()
	err := c.ensureClientLocked(ctx)
	c.state, c.detail = "unpaired", "Device unlinked. Connect to pair again."
	c.updated = time.Now()
	c.mu.Unlock()
	return err
}

// QR renders the current pairing code as a PNG.
func (c *Channel) QR(context.Context) ([]byte, error) {
	c.mu.Lock()
	code := c.qr
	c.mu.Unlock()
	if code == "" {
		return nil, errors.New("no pairing code yet — press Connect")
	}
	return qrcode.Encode(code, qrcode.Medium, 512)
}

func (c *Channel) setState(state, detail string) {
	c.mu.Lock()
	c.state, c.detail = state, detail
	if state != "pairing" {
		c.qr = ""
	}
	c.updated = time.Now()
	c.mu.Unlock()
	if state == "error" {
		c.log.Warn("WhatsApp error", "detail", detail)
	}
}

// Status reports the channel.
func (c *Channel) Status() dservice.ChannelStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := dservice.ChannelStatus{Name: name, Label: "WhatsApp", Enabled: c.cfg.Enabled, State: c.state, Detail: c.detail,
		Connected: c.state == "connected", HasQR: c.qr != "",
		Blocked: append([]dservice.BlockedSender{}, c.blocked...), UpdatedAt: c.updated}
	if c.client != nil && c.client.Store.ID != nil {
		st.Account = "+" + c.client.Store.ID.User
	}
	return st
}

func (c *Channel) handleEvent(evt any) {
	switch v := evt.(type) {
	case *events.Message:
		// Kept here, in event order, so an edit or a revoke lands after its
		// original. The chat name is what is known without asking
		// WhatsApp: a cached group name, or the sender's push name.
		chatName := ""
		if v.Info.IsGroup {
			c.mu.Lock()
			chatName = c.groupNames[v.Info.Chat.ToNonAD().String()]
			c.mu.Unlock()
		} else if !v.Info.IsFromMe {
			chatName = strings.TrimSpace(v.Info.PushName)
		}
		if err := c.indexMessage(context.Background(), v, chatName, true, false); err != nil {
			c.log.Warn("could not keep a WhatsApp message for search", "error", err)
		}
		go c.handleMessage(v)
	case *events.HistorySync:
		go c.indexHistory(v)
	case *events.Receipt:
		c.handleReceipt(v)
	case *events.Connected:
		c.setState("connected", "")
		c.log.Info("WhatsApp connected")
	case *events.Disconnected:
		c.setState("reconnecting", "Connection lost; reconnecting.")
	case *events.LoggedOut:
		c.setState("unpaired", "This device was unlinked from the phone.")
	case *events.StreamReplaced:
		c.setState("disconnected", "Another process connected with the same WhatsApp session.")
	case *events.ConnectFailure:
		c.setState("error", "WhatsApp connection failure: "+v.Message)
	case *events.TemporaryBan:
		c.setState("error", "WhatsApp temporarily banned this connection: "+v.String())
	}
}

// handleReceipt records how far messages this account sent got, in the
// chat index and for the rooms whose replies they were. Only delivered and
// read count: "sender" receipts acknowledge the account's own other
// devices, "read-self" is the owner reading on their phone, and "retry" is
// a decryption failure.
func (c *Channel) handleReceipt(v *events.Receipt) {
	var receipt valueobject.Receipt
	switch v.Type {
	case types.ReceiptTypeDelivered:
		receipt = valueobject.ReceiptDelivered
	case types.ReceiptTypeRead:
		receipt = valueobject.ReceiptRead
	default:
		return
	}
	if len(v.MessageIDs) == 0 {
		return
	}
	ids := make([]string, 0, len(v.MessageIDs))
	for _, id := range v.MessageIDs {
		ids = append(ids, string(id))
	}
	if c.index != nil {
		if err := c.index.MarkReceipt(context.Background(), name, v.Chat.ToNonAD().String(), ids, receipt); err != nil {
			c.log.Warn("could not record a WhatsApp receipt", "error", err)
		}
	}
	c.mu.Lock()
	handler := c.handler
	c.mu.Unlock()
	if handler != nil {
		go handler.HandleReceipt(context.Background(), c.Name(), ids, receipt)
	}
}

func (c *Channel) clientCopy() *whatsmeow.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

// handleMessage gates one message and hands it on.
func (c *Channel) handleMessage(evt *events.Message) {
	info := evt.Info
	if info.IsFromMe || info.Chat.IsEmpty() || info.Sender.IsEmpty() || evt.IsEdit {
		return
	}
	if info.Chat.Server == types.BroadcastServer || info.Chat.Server == types.NewsletterServer || info.Chat.User == "status" {
		return
	}
	c.mu.Lock()
	cfg, handler := c.cfg, c.handler
	c.mu.Unlock()
	if !cfg.Enabled || handler == nil {
		return
	}
	text := waText(evt.Message)
	if info.IsGroup {
		switch cfg.GroupPolicy {
		case "disabled":
			return
		case "mention":
			// Poll traffic cannot carry an @mention; the allowlist still
			// gates who reaches the room.
			if !isPollTraffic(evt.Message) && !c.mentionsMe(evt.Message, text) && !strings.HasPrefix(text, "/") {
				return
			}
		}
	}
	if !info.Timestamp.IsZero() && time.Since(info.Timestamp) > maxMessageAge {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !cfg.allows(c.aliases(ctx, info.Sender, info.SenderAlt), c.aliases(ctx, info.Chat)) {
		c.recordBlocked(info)
		return
	}
	text = c.stripOwnMentions(text)
	m := evt.Message
	var vote *dservice.PollVote
	if upd := m.GetPollUpdateMessage(); upd != nil {
		mk := upd.GetPollCreationMessageKey()
		// A poll the owner sent through a send request is ordinary chat
		// content: its votes reach the room as text, not as an answer.
		explicitNames, explicit := []string(nil), false
		if mk.GetFromMe() {
			if explicitNames, explicit = c.explicitPollOptions(ctx, info.Chat, mk.GetID()); explicit {
				c.rememberPoll(info.Chat.ToNonAD().String(), mk.GetID(), explicitNames)
			}
		}
		picked, ok := c.decodeVote(ctx, evt, mk)
		switch {
		case explicit && len(picked) > 0:
			text = joinText(text, "[Poll vote: "+strings.Join(picked, ", ")+"]\n[Poll ID: "+mk.GetID()+"]")
		case explicit:
			text = joinText(text, "[Poll vote]\n[Poll ID: "+mk.GetID()+"]")
		case mk.GetFromMe():
			// Only this channel sends polls from the linked account: the
			// vote answers a room's request.
			if !ok {
				picked = nil
			}
			vote = &dservice.PollVote{PollID: mk.GetID(), Selected: picked}
		case len(picked) > 0:
			text = joinText(text, "[Poll vote: "+strings.Join(picked, ", ")+"]")
		default:
			text = joinText(text, "[Poll vote]")
		}
	}
	if p := pollCreation(m); p != nil {
		text = joinText(text, pollText(p))
		names := []string{}
		for _, o := range p.GetOptions() {
			if n := strings.TrimSpace(o.GetOptionName()); n != "" {
				names = append(names, n)
			}
		}
		c.rememberPoll(info.Chat.String(), string(info.ID), names)
	}
	if add := m.GetPollAddOptionMessage(); add != nil {
		text = joinText(text, c.rememberPollOption(add))
	}
	files, text := c.inboundFiles(ctx, m, info.ID, text)
	if vote != nil {
		text, files = "", nil
	} else if strings.TrimSpace(text) == "" && len(files) == 0 {
		return
	}
	chatJID := info.Chat.ToNonAD().String()
	chat, err := valueobject.NewChatRef(name, chatJID, "")
	if err != nil {
		return
	}
	sender := strings.TrimSpace(info.PushName)
	if sender == "" {
		sender = "+" + info.Sender.User
	}
	chatName := sender
	if info.IsGroup {
		chatName = c.groupName(ctx, info.Chat)
	}
	if client := c.clientCopy(); client != nil && cfg.ReadReceipts {
		_ = client.MarkRead(ctx, []types.MessageID{info.ID}, time.Now(), info.Chat, info.Sender)
	}
	handler.HandleInbound(ctx, dservice.InboundMessage{
		Chat: chat, MessageID: chatJID + ":" + string(info.ID),
		SenderID: info.Sender.ToNonAD().String(), SenderName: sender, ChatName: chatName,
		Text: text, Quote: quoteOf(m), Group: info.IsGroup, Files: files, Vote: vote, At: info.Timestamp,
	})
}

// aliases expands JIDs into every form the allowlist may hold: the JID,
// its user part, and its phone-number/LID counterpart.
func (c *Channel) aliases(ctx context.Context, jids ...types.JID) []string {
	client := c.clientCopy()
	var out []string
	for _, j := range jids {
		if j.IsEmpty() {
			continue
		}
		out = append(out, j.ToNonAD().String(), j.User)
		if client != nil && client.Store != nil && client.Store.LIDs != nil {
			if alt, err := client.Store.GetAltJID(ctx, j); err == nil && !alt.IsEmpty() {
				out = append(out, alt.ToNonAD().String(), alt.User)
			}
		}
	}
	return out
}

// ownIDs are the number and LID of the linked account, normalized.
func (c *Channel) ownIDs() map[string]bool {
	client := c.clientCopy()
	out := map[string]bool{}
	if client == nil {
		return out
	}
	if client.Store.ID != nil {
		out[normalizeNumber(client.Store.ID.User)] = true
	}
	if !client.Store.LID.IsEmpty() {
		out[normalizeNumber(client.Store.LID.User)] = true
	}
	return out
}

// mentionsMe reports a group message that @mentions or replies to the
// linked account.
func (c *Channel) mentionsMe(msg *waE2E.Message, text string) bool {
	own := c.ownIDs()
	if len(own) == 0 {
		return false
	}
	for _, ci := range contextInfos(msg) {
		if ci == nil {
			continue
		}
		for _, jid := range ci.GetMentionedJID() {
			if own[normalizeNumber(jid)] {
				return true
			}
		}
		if p := ci.GetParticipant(); p != "" && own[normalizeNumber(p)] {
			return true
		}
	}
	lower := strings.ToLower(text)
	for id := range own {
		if id != "" && strings.Contains(lower, "@"+id) {
			return true
		}
	}
	return false
}

// contextInfos are the reply and mention contexts of every kind of message
// a chat can send.
func contextInfos(msg *waE2E.Message) []*waE2E.ContextInfo {
	if msg == nil {
		return nil
	}
	return []*waE2E.ContextInfo{
		msg.GetExtendedTextMessage().GetContextInfo(), msg.GetImageMessage().GetContextInfo(),
		msg.GetVideoMessage().GetContextInfo(), msg.GetDocumentMessage().GetContextInfo(),
		msg.GetAudioMessage().GetContextInfo(), msg.GetStickerMessage().GetContextInfo(),
		msg.GetPtvMessage().GetContextInfo(), msg.GetEventMessage().GetContextInfo(),
		msg.GetLiveLocationMessage().GetContextInfo(), msg.GetGroupInviteMessage().GetContextInfo(),
		msg.GetContactsArrayMessage().GetContextInfo(), msg.GetInteractiveResponseMessage().GetContextInfo(),
		msg.GetButtonsResponseMessage().GetContextInfo(), msg.GetListResponseMessage().GetContextInfo(),
		msg.GetContactMessage().GetContextInfo(), msg.GetLocationMessage().GetContextInfo(),
		pollCreation(msg).GetContextInfo(),
	}
}

// quoteOf is what a reply quotes: the quoted message's text, or a label for
// what it was when it had none ("an image"). It is empty for a message that
// replies to nothing.
func quoteOf(msg *waE2E.Message) string {
	for _, ci := range contextInfos(msg) {
		q := unwrapQuoted(ci.GetQuotedMessage())
		if q == nil {
			continue
		}
		if text := waText(q); text != "" {
			return text
		}
		return quotedKindLabel(q)
	}
	return ""
}

// unwrapQuoted peels view-once and ephemeral wrappers off a quoted message:
// quoted payloads arrive raw, unlike incoming events, which whatsmeow has
// already unwrapped.
func unwrapQuoted(m *waE2E.Message) *waE2E.Message {
	for m != nil {
		inner := m.GetViewOnceMessage().GetMessage()
		if inner == nil {
			inner = m.GetViewOnceMessageV2().GetMessage()
		}
		if inner == nil {
			inner = m.GetViewOnceMessageV2Extension().GetMessage()
		}
		if inner == nil {
			inner = m.GetEphemeralMessage().GetMessage()
		}
		if inner == nil {
			return m
		}
		m = inner
	}
	return nil
}

func quotedKindLabel(m *waE2E.Message) string {
	switch {
	case m.GetImageMessage() != nil:
		return "an image"
	case m.GetVideoMessage() != nil:
		return "a video"
	case m.GetAudioMessage() != nil:
		return "a voice message"
	case m.GetStickerMessage() != nil:
		return "a sticker"
	case m.GetDocumentMessage() != nil:
		return "a document"
	case pollCreation(m) != nil:
		return "a poll"
	case m.GetPollUpdateMessage() != nil:
		return "a poll vote"
	case m.GetPollAddOptionMessage() != nil:
		return "a poll option"
	case m.GetContactMessage() != nil, m.GetContactsArrayMessage() != nil:
		return "a contact"
	case m.GetEventMessage() != nil:
		return "an event"
	case m.GetPtvMessage() != nil:
		return "a video note"
	case m.GetLocationMessage() != nil, m.GetLiveLocationMessage() != nil:
		return "a location"
	case m.GetGroupInviteMessage() != nil:
		return "a group invite"
	}
	return "a message"
}

// stripOwnMentions removes "@<own number>" tokens so the room reads the
// request, not the address.
func (c *Channel) stripOwnMentions(text string) string {
	for id := range c.ownIDs() {
		if id != "" {
			text = strings.ReplaceAll(text, "@"+id, "")
		}
	}
	return strings.TrimSpace(text)
}

func (c *Channel) groupName(ctx context.Context, jid types.JID) string {
	key := jid.ToNonAD().String()
	c.mu.Lock()
	n, ok := c.groupNames[key]
	c.mu.Unlock()
	if ok {
		return n
	}
	n = "Group +" + jid.User
	if client := c.clientCopy(); client != nil && client.IsLoggedIn() {
		if info, err := client.GetGroupInfo(ctx, jid); err == nil && info != nil && strings.TrimSpace(info.GroupName.Name) != "" {
			n = strings.TrimSpace(info.GroupName.Name)
		}
	}
	c.mu.Lock()
	c.groupNames[key] = n
	c.mu.Unlock()
	return n
}

func (c *Channel) recordBlocked(info types.MessageInfo) {
	entry := dservice.BlockedSender{SenderID: info.Sender.ToNonAD().String(), ChatID: info.Chat.ToNonAD().String(),
		Name: strings.TrimSpace(info.PushName), Group: info.IsGroup, At: time.Now()}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.blocked[:0]
	for _, b := range c.blocked {
		if b.SenderID != entry.SenderID || b.ChatID != entry.ChatID {
			out = append(out, b)
		}
	}
	out = append(out, entry)
	if len(out) > blockedLimit {
		out = out[len(out)-blockedLimit:]
	}
	c.blocked = out
	c.updated = time.Now()
	c.log.Info("ignored a WhatsApp message from a sender not on the allowlist", "sender", entry.SenderID, "chat", entry.ChatID)
}

func parseJID(chat valueobject.ChatRef) (types.JID, error) {
	raw := chat.ChatID()
	if !strings.Contains(raw, "@") {
		raw += "@" + types.DefaultUserServer
	}
	return types.ParseJID(raw)
}

// Send renders Markdown as WhatsApp markup and sends it.
func (c *Channel) Send(ctx context.Context, chat valueobject.ChatRef, md string) ([]string, error) {
	jid, err := parseJID(chat)
	if err != nil {
		return nil, err
	}
	client := c.clientCopy()
	if client == nil || !client.IsLoggedIn() {
		return nil, errors.New("WhatsApp is not connected")
	}
	text := markup.Render(strings.TrimSpace(md), markup.WhatsApp)
	if r := []rune(text); len(r) > maxReplyRunes {
		text = string(r[:maxReplyRunes-1]) + "…"
	}
	if text == "" {
		text = "Done."
	}
	msg := &waE2E.Message{Conversation: &text}
	var ids []string
	err = c.paced(ctx, jid.String(), func(sendCtx context.Context) error {
		resp, err := client.SendMessage(sendCtx, jid, msg)
		if err != nil {
			return err
		}
		ids = []string{string(resp.ID)}
		c.indexSent(sendCtx, jid, resp, msg, false)
		return nil
	})
	return ids, err
}

// Typing shows "typing…" until stop is called, when the settings allow it.
func (c *Channel) Typing(ctx context.Context, chat valueobject.ChatRef) func() {
	c.mu.Lock()
	enabled := c.cfg.SendTyping
	c.mu.Unlock()
	jid, err := parseJID(chat)
	client := c.clientCopy()
	if !enabled || err != nil || client == nil {
		return func() {}
	}
	tctx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			_ = client.SendChatPresence(context.Background(), jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
			select {
			case <-tctx.Done():
				_ = client.SendChatPresence(context.Background(), jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
				return
			case <-time.After(typingEvery):
			}
		}
	}()
	return cancel
}

// Group is one group the linked account has joined.
type Group struct {
	JID          string `json:"jid"`
	Name         string `json:"name"`
	Participants int    `json:"participants"`
	Allowed      bool   `json:"allowed"`
}

// Groups lists the joined groups for the allowlist picker.
func (c *Channel) Groups(ctx context.Context) ([]Group, error) {
	client := c.clientCopy()
	if client == nil || !client.IsLoggedIn() {
		return nil, errors.New("WhatsApp is not connected")
	}
	list, err := client.GetJoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	allowed := map[string]bool{}
	for _, g := range c.cfg.AllowedGroups {
		allowed[g] = true
	}
	c.mu.Unlock()
	out := []Group{}
	for _, g := range list {
		if g == nil || g.JID.IsEmpty() {
			continue
		}
		jid := g.JID.ToNonAD().String()
		out = append(out, Group{JID: jid, Name: g.GroupName.Name, Participants: g.ParticipantCount, Allowed: allowed[jid]})
	}
	return out, nil
}

// waText extracts the readable text of a message: its body or caption, or
// a short marker for structured messages.
func waText(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	for _, s := range []string{
		msg.GetConversation(), msg.GetExtendedTextMessage().GetText(),
		msg.GetImageMessage().GetCaption(), msg.GetVideoMessage().GetCaption(), msg.GetDocumentMessage().GetCaption(),
		msg.GetButtonsResponseMessage().GetSelectedDisplayText(), msg.GetListResponseMessage().GetTitle(),
	} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	if x := msg.GetLocationMessage(); x != nil {
		return fmt.Sprintf("[Location: %.6f, %.6f]", x.GetDegreesLatitude(), x.GetDegreesLongitude())
	}
	if x := msg.GetContactMessage(); x != nil {
		return "[Contact card: " + x.GetDisplayName() + "]"
	}
	return ""
}

// GroupsJSON is Groups for the HTTP boundary.
func (c *Channel) GroupsJSON(ctx context.Context) (json.RawMessage, error) {
	groups, err := c.Groups(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"data": groups})
}
