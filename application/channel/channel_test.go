package appchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
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
	repochannel "github.com/jahrulnr/nusashell/infrastructure/repository/channel"
	repoproject "github.com/jahrulnr/nusashell/infrastructure/repository/project"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	repoupload "github.com/jahrulnr/nusashell/infrastructure/repository/upload"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	"github.com/jahrulnr/nusashell/migration"
)

// fakeChannel records what it was asked to send.
type fakeChannel struct {
	mu   sync.Mutex
	sent []string // "chatID: text"
}

func (f *fakeChannel) Name() string                                         { return "telegram" }
func (f *fakeChannel) Start(context.Context, dservice.InboundHandler) error { return nil }
func (f *fakeChannel) Stop() error                                          { return nil }
func (f *fakeChannel) Status() dservice.ChannelStatus {
	return dservice.ChannelStatus{Name: "telegram"}
}
func (f *fakeChannel) Send(_ context.Context, chat valueobject.ChatRef, text string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, chat.ChatID()+": "+text)
	return []string{fmt.Sprintf("m%d", len(f.sent))}, nil
}
func (f *fakeChannel) SendFile(_ context.Context, chat valueobject.ChatRef, file dservice.ChannelFile) error {
	f.mu.Lock()
	f.sent = append(f.sent, chat.ChatID()+": file "+file.Name+" "+file.MediaType+" "+string(file.Data))
	f.mu.Unlock()
	return nil
}
func (f *fakeChannel) Typing(context.Context, valueobject.ChatRef) func() { return func() {} }
func (f *fakeChannel) Connect(context.Context) error                      { return nil }
func (f *fakeChannel) Disconnect() error                                  { return nil }
func (f *fakeChannel) Logout(context.Context) error                       { return nil }
func (f *fakeChannel) QR(context.Context) ([]byte, error)                 { return nil, nil }
func (f *fakeChannel) Settings(context.Context) (json.RawMessage, error)  { return nil, nil }
func (f *fakeChannel) UpdateSettings(context.Context, json.RawMessage) error {
	return nil
}
func (f *fakeChannel) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

// fakeRooms keeps rooms in memory; a run appends the user line and a reply.
type fakeRooms struct {
	mu       sync.Mutex
	infos    map[string]drepo.ConversationInfo
	history  map[string][]entity.Message
	pending  map[string][]valueobject.ApprovalRequest
	approved []string
	roots    []string
	commands []dservice.AgentCommand
	// deliveries are "msgID receipt file,file" notes, in order.
	deliveries []string
}

func (r *fakeRooms) RecordDelivery(_ context.Context, _, msgID string, files []valueobject.Attachment, receipt valueobject.Receipt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	r.deliveries = append(r.deliveries, strings.TrimSpace(msgID+" "+string(receipt)+" "+strings.Join(names, ",")))
	return nil
}

func (r *fakeRooms) Roots(context.Context, appconversation.Place) (drepo.Workspace, []string, error) {
	return repoworkspace.NewLocal(), r.roots, nil
}

func (r *fakeRooms) Info(_ context.Context, id string) (drepo.ConversationInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.infos[id]
	if !ok {
		return info, apperror.NewNotFoundError("conversation", nil)
	}
	return info, nil
}
func (r *fakeRooms) List(context.Context) ([]drepo.ConversationInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []drepo.ConversationInfo
	for _, i := range r.infos {
		out = append(out, i)
	}
	return out, nil
}
func (r *fakeRooms) History(_ context.Context, id string, _, _ int) ([]entity.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.history[id], nil
}
func (r *fakeRooms) Annotate(_ context.Context, id string, a appconversation.Annotation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	info := r.infos[id]
	if a.Title != nil {
		info.Title = *a.Title
	}
	r.infos[id] = info
	return nil
}
func (r *fakeRooms) PendingApprovals(id string) []valueobject.ApprovalRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[id]
}
func (r *fakeRooms) Approve(_, requestID, optionID string) error {
	r.mu.Lock()
	r.approved = append(r.approved, requestID+"="+optionID)
	r.mu.Unlock()
	return nil
}
func (r *fakeRooms) AgentCommands(string) []dservice.AgentCommand {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commands
}
func (r *fakeRooms) AnswerElicitation(_, id string, a valueobject.ElicitationAnswer) error {
	r.mu.Lock()
	r.approved = append(r.approved, id+"="+string(a.Content))
	r.mu.Unlock()
	return nil
}

type runCall struct {
	convID, provider, model, agent, content string
	origin                                  valueobject.TurnOrigin
}

// fakeRunner "runs" a turn by writing a reply into fakeRooms.
type fakeRunner struct {
	rooms  *fakeRooms
	mu     sync.Mutex
	calls  []runCall
	busy   map[string]bool
	steers []string
	files  [][]valueobject.Attachment  // attachments of each run
	refs   [][]valueobject.Reference   // references of each run
	events []event.TurnEvent           // emitted to observe on every run
	during func()                      // runs after the events, while the turn is open
	shape  func(reply *entity.Message) // shapes each reply, an agent's media for one
	fail   error
}

func (f *fakeRunner) Run(_ context.Context, convID, providerID, modelID, content string, attachments []valueobject.Attachment, opts appjob.SendOptions, observe event.TurnEventHandler) (string, appconversation.SendResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, runCall{convID, providerID, modelID, opts.Agent, content, opts.Origin})
	f.files = append(f.files, attachments)
	f.refs = append(f.refs, opts.References)
	evs, fail := f.events, f.fail
	f.mu.Unlock()
	for _, ev := range evs {
		observe(ev)
	}
	if f.during != nil {
		f.during()
	}
	if fail != nil {
		return convID, appconversation.SendResult{}, fail
	}
	user, _ := entity.NewMessage(enum.RoleUser, content)
	reply, _ := entity.NewMessage(enum.RoleAssistant, "reply to "+content)
	if f.shape != nil {
		f.shape(&reply)
	}
	f.rooms.mu.Lock()
	info := f.rooms.infos[convID]
	info.ID = convID
	if info.AgentID == "" && info.ProviderID == "" {
		info.AgentID, info.ProviderID, info.ModelID = opts.Agent, providerID, modelID
	}
	f.rooms.infos[convID] = info
	f.rooms.history[convID] = append(f.rooms.history[convID], user, reply)
	f.rooms.mu.Unlock()
	return convID, appconversation.SendResult{UserMessagePersisted: true}, nil
}
func (f *fakeRunner) Steer(convID, text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.busy[convID] {
		return false
	}
	f.steers = append(f.steers, text)
	return true
}
func (f *fakeRunner) Cancel(convID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy[convID]
}
func (f *fakeRunner) Busy(convID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy[convID]
}

type fakeCatalog struct{}

func (fakeCatalog) Default(context.Context) (Runtime, error) {
	return Runtime{ProviderID: "or", ModelID: "auto"}, nil
}
func (fakeCatalog) Choices(context.Context) ([]Choice, error) {
	return []Choice{{Runtime{ProviderID: "or", ModelID: "auto"}, "OpenRouter Auto"}, {Runtime{AgentID: "claude"}, "Claude Agent"}}, nil
}

func setup(t *testing.T) (*App, *fakeChannel, *fakeRunner, *fakeRooms, *repochannel.Repository) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := repochannel.New(db)
	rooms := &fakeRooms{infos: map[string]drepo.ConversationInfo{}, history: map[string][]entity.Message{}, pending: map[string][]valueobject.ApprovalRequest{}}
	runner := &fakeRunner{rooms: rooms, busy: map[string]bool{}}
	uploads, err := repoupload.New(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	app := New(repo, repo, uploads, repoproject.New(db), rooms, runner, fakeCatalog{}, nil)
	ch := &fakeChannel{}
	app.Register(ch)
	return app, ch, runner, rooms, repo
}

func msg(chatID, id, text string) dservice.InboundMessage {
	chat, _ := valueobject.NewChatRef("telegram", chatID, "")
	return dservice.InboundMessage{Chat: chat, MessageID: id, SenderName: "Ana", ChatName: "Ana", Text: text}
}

func TestFirstMessageOpensALinkedThread(t *testing.T) {
	app, ch, runner, rooms, repo := setup(t)
	ctx := context.Background()
	app.process(ctx, msg("42", "42:1", "hello"))
	if len(runner.calls) != 1 || runner.calls[0].provider != "or" || runner.calls[0].convID == "" {
		t.Fatalf("runs = %+v", runner.calls)
	}
	if got := runner.calls[0].origin; got.Kind != enum.TurnOriginChannel || got.Name != "telegram" {
		t.Fatalf("origin = %+v, want the telegram channel", got)
	}
	convID := runner.calls[0].convID
	chat, _ := valueobject.NewChatRef("telegram", "42", "")
	if l, err := repo.Find(ctx, chat); err != nil || l.ConversationID() != convID {
		t.Fatalf("link = %+v, %v", l, err)
	}
	if got := ch.take(); len(got) != 1 || got[0] != "42: reply to hello" {
		t.Fatalf("sent = %q", got)
	}
	if rooms.infos[convID].Title != "Telegram · Ana" {
		t.Fatalf("title = %q", rooms.infos[convID].Title)
	}
	// A redelivered message is ignored; a new one continues the same room.
	app.process(ctx, msg("42", "42:1", "hello"))
	app.process(ctx, msg("42", "42:2", "again"))
	if len(runner.calls) != 2 || runner.calls[1].convID != convID || runner.calls[1].provider != "" {
		t.Fatalf("runs = %+v", runner.calls)
	}
}

func TestBusyRoomSteersAndCommandsStayLocal(t *testing.T) {
	app, ch, runner, rooms, _ := setup(t)
	ctx := context.Background()
	app.process(ctx, msg("42", "1", "hello"))
	convID := runner.calls[0].convID
	ch.take()

	runner.busy[convID] = true
	app.process(ctx, msg("42", "2", "actually, use Go"))
	if len(runner.steers) != 1 || runner.steers[0] != "actually, use Go" || len(runner.calls) != 1 {
		t.Fatalf("steers = %q runs = %d", runner.steers, len(runner.calls))
	}
	app.process(ctx, msg("42", "3", "/stop"))
	if got := ch.take(); len(got) != 1 || !strings.Contains(got[0], "Stopping") {
		t.Fatalf("stop reply = %q", got)
	}
	runner.busy[convID] = false

	// Approvals: a lone request is answered without naming it.
	req, _ := valueobject.NewApprovalRequest("apr_ABCDEF123", "c1", "Run tests", "", enum.ToolActionExecute, []valueobject.ApprovalOption{
		{ID: "yes", Name: "Allow once", Choice: enum.ApprovalAllowOnce}, {ID: "no", Name: "Reject", Choice: enum.ApprovalRejectOnce}})
	rooms.pending[convID] = []valueobject.ApprovalRequest{req}
	app.process(ctx, msg("42", "4", "/deny"))
	if len(rooms.approved) != 1 || rooms.approved[0] != "apr_ABCDEF123=no" {
		t.Fatalf("approved = %q", rooms.approved)
	}

	// /use picks the next reply's runtime; /status reports it.
	app.process(ctx, msg("42", "5", "/use claude"))
	app.process(ctx, msg("42", "6", "/status"))
	got := strings.Join(ch.take(), "\n")
	if !strings.Contains(got, "next reply runs on claude (ACP)") || !strings.Contains(got, "Next reply: claude (ACP)") {
		t.Fatalf("use/status replies = %q", got)
	}
	app.process(ctx, msg("42", "7", "go"))
	if last := runner.calls[len(runner.calls)-1]; last.agent != "claude" {
		t.Fatalf("run after /use = %+v", last)
	}
	// An unknown slash command reaches the thread — agents have their own.
	app.process(ctx, msg("42", "8", "/compact now"))
	if last := runner.calls[len(runner.calls)-1]; last.content != "/compact now" {
		t.Fatalf("unknown command run = %+v", last)
	}
}

func TestRoomCommandAndNewRelinkTheChat(t *testing.T) {
	app, ch, runner, rooms, repo := setup(t)
	ctx := context.Background()
	rooms.infos["c_other"] = drepo.ConversationInfo{ID: "c_other", Title: "Release notes", Label: "fox"}
	app.process(ctx, msg("42", "1", "/room @FOX"))
	chat, _ := valueobject.NewChatRef("telegram", "42", "")
	if l, err := repo.Find(ctx, chat); err != nil || l.ConversationID() != "c_other" {
		t.Fatalf("link after /room = %+v, %v", l, err)
	}
	app.process(ctx, msg("42", "2", "status of the notes?"))
	if runner.calls[0].convID != "c_other" {
		t.Fatalf("run went to %q", runner.calls[0].convID)
	}
	app.process(ctx, msg("42", "3", "/new"))
	if _, err := repo.Find(ctx, chat); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("/new must unlink, got %v", err)
	}
	app.process(ctx, msg("42", "4", "fresh start"))
	if c := runner.calls[len(runner.calls)-1].convID; c == "c_other" || c == "" {
		t.Fatalf("after /new the chat must open a new thread, got %q", c)
	}
	_ = ch
}

func TestRoutingApprovalPingAndMirror(t *testing.T) {
	app, ch, runner, rooms, repo := setup(t)
	ctx := context.Background()
	req, _ := valueobject.NewApprovalRequest("apr_XYZ987", "c1", "Edit main.go", "", enum.ToolActionEdit, []valueobject.ApprovalOption{
		{ID: "yes", Name: "Allow", Choice: enum.ApprovalAllowOnce}})
	runner.events = []event.TurnEvent{event.NewApprovalEvent(req)}
	app.process(ctx, msg("42", "1", "fix it"))
	got := ch.take()
	if len(got) != 2 || !strings.Contains(got[0], "/approve xyz987") || got[1] != "42: reply to fix it" {
		t.Fatalf("sent = %q", got)
	}
	convID := runner.calls[0].convID

	// A second chat on the same room, mirroring on: a web turn reaches both.
	other, _ := valueobject.NewChatRef("telegram", "77", "")
	link, _ := repo.Find(ctx, msg("42", "", "").Chat)
	_ = link
	l2, _ := newLink(other, convID)
	if err := repo.Save(ctx, l2); err != nil {
		t.Fatal(err)
	}
	info := rooms.infos[convID]
	info.Routing = &valueobject.Routing{ReplyToOrigin: true, MirrorFinal: true}
	rooms.infos[convID] = info
	app.TurnFinished(convID, "send", false, nil)
	if got := ch.take(); len(got) != 2 {
		t.Fatalf("mirror sent = %q", got)
	}
	// An edit or resume regenerates a reply — it is not a new answer and
	// does not mirror to the linked chats.
	app.TurnFinished(convID, "edit", false, nil)
	app.TurnFinished(convID, "resume", false, nil)
	if got := ch.take(); len(got) != 0 {
		t.Fatalf("an edit turn must not mirror: %q", got)
	}
	// A chat turn with mirroring answers its origin and copies the other.
	runner.events = nil
	app.process(ctx, msg("77", "2", "and docs"))
	got = ch.take()
	if len(got) != 2 || got[0] != "77: reply to and docs" || got[1] != "42: reply to and docs" {
		t.Fatalf("mirrored chat turn = %q", got)
	}
}

func TestFailedTurn(t *testing.T) {
	app, ch, runner, _, _ := setup(t)
	runner.fail = apperror.New(408, "turn canceled", nil)
	app.process(context.Background(), msg("42", "1", "hi"))
	if got := ch.take(); len(got) != 1 || got[0] != "42: Stopped." {
		t.Fatalf("canceled = %q", got)
	}
}

// Files a chat sends become the turn's attachments; a media-only message
// runs a turn of its own, and files never fold into a running turn.
func TestInboundFiles(t *testing.T) {
	app, _, runner, _, repo := setup(t)
	ctx := context.Background()
	in := msg("42", "1", "")
	in.Files = []dservice.ChannelFile{{Name: "shot.png", MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nfake")}}
	app.process(ctx, in)
	if len(runner.calls) != 1 || runner.calls[0].content != "" || len(runner.files[0]) != 1 {
		t.Fatalf("media-only run = %+v files=%v", runner.calls, runner.files)
	}
	if got := runner.files[0][0].MediaType(); got != "image/png" {
		t.Fatalf("media type = %q", got)
	}
	l, err := repo.Find(ctx, in.Chat)
	if err != nil {
		t.Fatal(err)
	}
	convID := l.ConversationID()

	// Over the cap: the rest are named in the text instead of dropped.
	runner.busy[convID] = true
	in = msg("42", "2", "look")
	for i := range 8 {
		in.Files = append(in.Files, dservice.ChannelFile{Name: fmt.Sprintf("f%d.txt", i), Data: []byte("x")})
	}
	app.process(ctx, in)
	if len(runner.steers) != 0 || len(runner.calls) != 2 {
		t.Fatalf("files steered: steers=%q calls=%d", runner.steers, len(runner.calls))
	}
	if n := len(runner.files[1]); n != maxInboundFiles {
		t.Fatalf("attached %d files", n)
	}
	if c := runner.calls[1].content; !strings.HasPrefix(c, "look") || !strings.Contains(c, "2 more file(s)") {
		t.Fatalf("content = %q", c)
	}
	if got := runner.files[1][0].MediaType(); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("sniffed media type = %q", got)
	}
}

func newLink(chat valueobject.ChatRef, convID string) (aggregate.ChannelLink, error) {
	return aggregate.NewChannelLink(chat, convID, "Bo", false, time.Now())
}

// A reply's links to files inside the room's folders go to the chat as
// files, and the links leave the text; links elsewhere stay as written.
func TestReplyFiles(t *testing.T) {
	app, ch, _, rooms, _ := setup(t)
	root, outside := t.TempDir(), t.TempDir()
	rooms.roots = []string{root}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	text := "see [the notes](" + filepath.Join(root, "notes.txt") + ") and file://" + filepath.Join(root, "notes.txt") +
		" but not [that](" + filepath.Join(outside, "secret.txt") + ")"
	app.process(context.Background(), msg("42", "1", text))
	got := ch.take()
	if len(got) != 2 {
		t.Fatalf("sent = %q", got)
	}
	if got[0] != "42: reply to see the notes and  but not [that]("+filepath.Join(outside, "secret.txt")+")" {
		t.Fatalf("text = %q", got[0])
	}
	if !strings.HasPrefix(got[1], "42: file notes.txt text/plain") || !strings.HasSuffix(got[1], " hello") {
		t.Fatalf("file = %q", got[1])
	}

	// The reply notes the file it took, once however it was linked, and
	// receipts for its text raise it: delivered, then read, never back.
	rooms.mu.Lock()
	notes := slices.Clone(rooms.deliveries)
	rooms.mu.Unlock()
	if len(notes) != 1 || !strings.HasSuffix(notes[0], " notes.txt") {
		t.Fatalf("deliveries = %q", notes)
	}
	msgID := strings.Fields(notes[0])[0]
	ctx := context.Background()
	app.HandleReceipt(ctx, "telegram", []string{"m1", "unknown"}, valueobject.ReceiptDelivered)
	app.HandleReceipt(ctx, "telegram", []string{"m1"}, valueobject.ReceiptDelivered)
	app.HandleReceipt(ctx, "whatsapp", []string{"m1"}, valueobject.ReceiptRead) // another channel's id
	app.HandleReceipt(ctx, "telegram", []string{"m1"}, valueobject.ReceiptRead)
	app.HandleReceipt(ctx, "telegram", []string{"m1"}, valueobject.ReceiptDelivered)
	rooms.mu.Lock()
	notes = slices.Clone(rooms.deliveries[1:])
	rooms.mu.Unlock()
	if want := []string{msgID + " delivered", msgID + " read"}; !slices.Equal(notes, want) {
		t.Fatalf("receipts = %q, want %q", notes, want)
	}
}

// A reply's media — what an agent put in it — goes to the chat in the
// reply's order, whether it sits in the room's folders or was kept as an
// upload; a kept file that changed since it was kept is not sent.
func TestAgentMediaGoesToTheChat(t *testing.T) {
	app, ch, runner, rooms, _ := setup(t)
	root := t.TempDir()
	rooms.roots = []string{root}
	if err := os.WriteFile(filepath.Join(root, "chart.txt"), []byte("in the folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploads, err := repoupload.New(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	_, kept, err := uploads.Save(context.Background(), "pixel.txt", "text/plain", []byte("kept bytes"))
	if err != nil {
		t.Fatal(err)
	}
	_, tampered, err := uploads.Save(context.Background(), "tampered.txt", "text/plain", []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimPrefix(tampered.URI(), "file://"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	inFolder, _ := valueobject.NewAttachment("file://"+filepath.Join(root, "chart.txt"), "text/plain")
	inFolder.SetName("chart.txt")
	runner.shape = func(reply *entity.Message) {
		reply.SetContent("")
		reply.SetParts([]entity.MessagePart{entity.NewMediaPart(kept), entity.NewMediaPart(tampered), entity.NewMediaPart(inFolder)})
	}
	app.process(context.Background(), msg("42", "1", "draw something"))
	got := ch.take()
	// A reply of media alone sends no text; the file that no longer matches
	// what was kept stays home.
	if len(got) != 2 || !strings.HasPrefix(got[0], "42: file pixel.txt") || !strings.HasSuffix(got[0], " kept bytes") ||
		!strings.HasPrefix(got[1], "42: file chart.txt") || !strings.HasSuffix(got[1], " in the folder") {
		t.Fatalf("sent = %q", got)
	}
}

// pollChannel is a chat surface with native polls.
type pollChannel struct {
	*fakeChannel
	polls []string // "question|opt1,opt2|max"
}

func (p *pollChannel) SendPoll(_ context.Context, _ valueobject.ChatRef, question string, options []string, maxSelectable int) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.polls = append(p.polls, fmt.Sprintf("%s|%s|%d", question, strings.Join(options, ","), maxSelectable))
	return fmt.Sprintf("poll%d", len(p.polls)), nil
}

func vote(chatID, sender, pollID string, picks ...string) dservice.InboundMessage {
	in := msg(chatID, "", "")
	in.SenderID, in.Vote = sender, &dservice.PollVote{PollID: pollID, Selected: picks}
	return in
}

// A request of a chat turn reaches a chat with polls as a poll; only the
// sender of the turn's message answers it, and a vote after the turn ends
// is told so.
func TestApprovalPoll(t *testing.T) {
	app, ch, runner, rooms, _ := setup(t)
	pc := &pollChannel{fakeChannel: ch}
	app.Register(pc)
	ctx := context.Background()
	req, _ := valueobject.NewApprovalRequest("apr_POLL1", "c1", "Run tests", "", enum.ToolActionExecute, []valueobject.ApprovalOption{
		{ID: "yes", Name: "Allow", Choice: enum.ApprovalAllowOnce}, {ID: "no", Name: "Reject", Choice: enum.ApprovalRejectOnce}})
	runner.events = []event.TurnEvent{event.NewApprovalEvent(req)}
	runner.during = func() {
		app.vote(ctx, vote("42", "intruder", "poll1", "Allow"))
		app.vote(ctx, vote("42", "u1", "poll1", "Bogus"))
		app.vote(ctx, vote("42", "u1", "poll1", "Reject"))
		app.wg.Wait()
	}
	in := msg("42", "1", "test it")
	in.SenderID = "u1"
	app.process(ctx, in)
	if len(pc.polls) != 1 || !strings.Contains(pc.polls[0], "Run tests") || !strings.HasSuffix(pc.polls[0], "|Allow,Reject|1") {
		t.Fatalf("polls = %q", pc.polls)
	}
	if len(rooms.approved) != 1 || rooms.approved[0] != "apr_POLL1=no" {
		t.Fatalf("approved = %q", rooms.approved)
	}
	got := ch.take()
	if len(got) != 3 || got[0] != "42: Only the person who asked can answer that." || got[1] != "42: Answered: Reject." {
		t.Fatalf("sent = %q", got)
	}
	app.vote(ctx, vote("42", "u1", "poll1", "Allow"))
	if got := ch.take(); len(got) != 1 || got[0] != "42: That request has already ended." {
		t.Fatalf("stale vote = %q", got)
	}
}

// A one-question choice becomes a poll; the picks answer the form in the
// schema's shape. Any other question points at the web.
func TestElicitationPoll(t *testing.T) {
	app, ch, runner, rooms, _ := setup(t)
	pc := &pollChannel{fakeChannel: ch}
	app.Register(pc)
	ctx := context.Background()
	e, err := valueobject.NewElicitation("el_1", "form", "Deploy where?",
		json.RawMessage(`{"type":"object","properties":{"env":{"type":"string","enum":["staging","prod"]}}}`), "")
	if err != nil {
		t.Fatal(err)
	}
	free, _ := valueobject.NewElicitation("el_2", "form", "Your name?",
		json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`), "")
	runner.events = []event.TurnEvent{event.NewElicitationEvent(e), event.NewElicitationEvent(free)}
	runner.during = func() {
		app.vote(ctx, vote("42", "", "poll1", "staging"))
		app.wg.Wait()
	}
	app.process(ctx, msg("42", "1", "ship"))
	if len(pc.polls) != 1 || pc.polls[0] != "Deploy where?|staging,prod|1" {
		t.Fatalf("polls = %q", pc.polls)
	}
	if len(rooms.approved) != 1 || rooms.approved[0] != `el_1={"env":"staging"}` {
		t.Fatalf("answers = %q", rooms.approved)
	}
	got := ch.take()
	if len(got) != 3 || !strings.Contains(got[0], "Your name?") || got[1] != "42: Answered: staging." {
		t.Fatalf("sent = %q", got)
	}
}

// menuChannel is a chat surface with a per-chat "/" menu.
type menuChannel struct {
	*fakeChannel
	menus []string // "chatID: name,name" or "chatID: default"
}

func (m *menuChannel) SyncCommands(_ context.Context, chat valueobject.ChatRef, cmds []dservice.AgentCommand) {
	names := []string{}
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	entry := chat.ChatID() + ": " + strings.Join(names, ",")
	if len(cmds) == 0 {
		entry = chat.ChatID() + ": default"
	}
	m.menus = append(m.menus, entry)
}

// A chat's menu follows its room's agent commands after each turn, and
// goes back to the default when the chat starts over.
func TestCommandMenu(t *testing.T) {
	app, ch, _, rooms, _ := setup(t)
	mc := &menuChannel{fakeChannel: ch}
	app.Register(mc)
	ctx := context.Background()
	rooms.commands = []dservice.AgentCommand{{Name: "review"}, {Name: "plan"}}
	app.process(ctx, msg("42", "1", "hi"))
	app.process(ctx, msg("42", "2", "/new"))
	if len(mc.menus) != 2 || mc.menus[0] != "42: review,plan" || mc.menus[1] != "42: default" {
		t.Fatalf("menus = %q", mc.menus)
	}
}

// Mentions suggest labeled rooms, then projects by handle; a message from a
// chat's mention picker turns its @tokens into references on the text the
// room gets, after a group's sender prefix.
func TestMentionsAndInlineReferences(t *testing.T) {
	app, _, runner, rooms, _ := setup(t)
	ctx := context.Background()
	rooms.infos["c_rel"] = drepo.ConversationInfo{ID: "c_rel", Title: "Release prep", Label: "release"}
	rooms.infos["c_none"] = drepo.ConversationInfo{ID: "c_none", Title: "Release notes"}
	p, err := aggregate.NewProject("p_web", "Web Site", []string{t.TempDir()}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := app.projects.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	got := app.Mentions(ctx, "@rel")
	if len(got) != 1 || got[0].Token != "@release" || got[0].Kind != "conversation" {
		t.Fatalf("mentions(rel) = %+v", got)
	}
	got = app.Mentions(ctx, "")
	if len(got) != 2 || got[1].Token != "@web-site" || got[1].ID != "p_web" {
		t.Fatalf("mentions() = %+v", got)
	}

	in := msg("-100", "1", "@release ask @web-site and @nobody")
	in.Group, in.Inline, in.SenderName = true, true, "Ana"
	app.process(ctx, in)
	if c := runner.calls[0].content; c != "Ana: @release ask @web-site and @nobody" {
		t.Fatalf("content = %q", c)
	}
	refs := runner.refs[0]
	if len(refs) != 2 || refs[0].ID() != "c_rel" || refs[0].Start() != 5 || refs[0].End() != 13 ||
		refs[1].Kind().String() != "project" || refs[1].Label() != "@web-site" {
		t.Fatalf("refs = %+v", refs)
	}

	// Without the picker, the same text names nothing.
	plain := msg("42", "2", "@release hi")
	app.process(ctx, plain)
	if len(runner.refs[1]) != 0 {
		t.Fatalf("plain refs = %+v", runner.refs[1])
	}
}

func TestReplyQuoteReachesTheRoom(t *testing.T) {
	app, _, runner, _, _ := setup(t)
	ctx := context.Background()

	// A reply names what it answers, on one line ahead of the text; in a
	// group the sender's name leads that line.
	in := msg("42", "1", "yes, go ahead")
	in.Group = true
	in.Quote = "Shall I\n  rebase now?"
	app.process(ctx, in)
	if got, want := runner.calls[0].content, "Ana: [In reply to: Shall I rebase now?]\nyes, go ahead"; got != want {
		t.Fatalf("group reply = %q, want %q", got, want)
	}

	// A reply with no text of its own still carries its quote.
	in = msg("42", "2", "")
	in.Quote = "a video"
	in.Files = []dservice.ChannelFile{{Name: "clip.mp4", MediaType: "video/mp4", Data: []byte("x")}}
	app.process(ctx, in)
	if got, want := runner.calls[1].content, "[In reply to: a video]"; got != want {
		t.Fatalf("media reply = %q, want %q", got, want)
	}

	// A long quote is cut, and a message that replies to nothing is as before.
	in = msg("42", "3", "ok")
	in.Quote = strings.Repeat("x", 300)
	app.process(ctx, in)
	want := "[In reply to: " + strings.Repeat("x", maxQuoteRunes-1) + "…]\nok"
	if got := runner.calls[2].content; got != want {
		t.Fatalf("long quote = %q", got)
	}
	app.process(ctx, msg("42", "4", "plain"))
	if got := runner.calls[3].content; got != "plain" {
		t.Fatalf("plain message = %q", got)
	}
}
