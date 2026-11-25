package appcontrol

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
)

type fakeRooms struct {
	mu        sync.Mutex
	infos     []drepo.ConversationInfo
	msgs      map[string][]entity.Message
	named     map[string]string
	approvals map[string][]valueobject.ApprovalRequest
	// env is where the target's workspace runs; the zero value is "", which the
	// attachment path treats as remote/unknown.
	env enum.Environment
}

func (f *fakeRooms) List(context.Context) ([]drepo.ConversationInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]drepo.ConversationInfo(nil), f.infos...)
	for i := range out {
		out[i].MessageCount = len(f.msgs[out[i].ID])
	}
	return out, nil
}

func (f *fakeRooms) Info(_ context.Context, id string) (drepo.ConversationInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range f.infos {
		if i.ID == id {
			i.MessageCount = len(f.msgs[id])
			return i, nil
		}
	}
	return drepo.ConversationInfo{}, apperror.NewNotFoundError("conversation "+id, nil)
}

func (f *fakeRooms) History(_ context.Context, id string, start, end int) ([]entity.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]entity.Message(nil), f.msgs[id][start:end]...), nil
}

func (f *fakeRooms) Environment(context.Context, string) (enum.Environment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.env, nil
}

func (f *fakeRooms) PendingApprovals(convID string) []valueobject.ApprovalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.approvals == nil {
		return nil
	}
	return f.approvals[convID]
}

func (f *fakeRooms) Annotate(_ context.Context, id string, a appconversation.Annotation) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.named[id] = *a.Title
	return false, nil
}

func (f *fakeRooms) add(id string, role enum.Role, text string) {
	m, _ := entity.NewMessage(role, text)
	f.mu.Lock()
	f.msgs[id] = append(f.msgs[id], m)
	f.mu.Unlock()
}

type run struct {
	conv, provider, model, agent, project, text string
	origin                                      valueobject.TurnOrigin
	attachments                                 []valueobject.Attachment
	relay                                       *valueobject.Relay
}

type fakeTurns struct {
	mu    sync.Mutex
	runs  chan run
	busy  map[string]bool
	rooms *fakeRooms
	gate  chan struct{}
}

func (f *fakeTurns) Run(_ context.Context, convID, provider, model, text string, attachments []valueobject.Attachment, opts appjob.SendOptions, _ event.TurnEventHandler) (string, appconversation.SendResult, error) {
	f.rooms.mu.Lock()
	known := false
	for _, i := range f.rooms.infos {
		known = known || i.ID == convID
	}
	if !known {
		// A creation title names the fresh room from its first save —
		// the send job's Title option mirrors that ordering here.
		f.rooms.infos = append(f.rooms.infos, drepo.ConversationInfo{ID: convID, AgentID: opts.Agent, ProviderID: provider, ModelID: model, ProjectID: opts.Project, Title: opts.Title})
	}
	f.rooms.mu.Unlock()
	f.rooms.add(convID, enum.RoleUser, text)
	f.rooms.add(convID, enum.RoleAssistant, "done: "+text)
	f.runs <- run{convID, provider, model, opts.Agent, opts.Project, text, opts.Origin, attachments, opts.Relay}
	f.mu.Lock()
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return convID, appconversation.SendResult{}, nil
}

func (f *fakeTurns) Cancel(convID string) bool { return f.Busy(convID) }

func (f *fakeTurns) Busy(convID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy[convID]
}

// fakeQueue stands in for the room worker's FIFO: it accepts while the room is
// busy and counts what waits, so the four outcomes can be driven without a
// real worker.
type fakeQueue struct {
	mu       sync.Mutex
	enqueued []string
	full     bool
	stopping bool
	// accepted keeps the depth the way the worker's queue does: it grows on
	// each accepted message and resets when a test drains it.
	depth int
	// kept is what each accepted message carried, so a queued attachment can
	// be judged without a real worker.
	kept [][]valueobject.Attachment
	// relays is the provenance each accepted message carried.
	relays []*valueobject.Relay
}

func (q *fakeQueue) Enqueue(_ context.Context, convID, _, _, text string, attachments []valueobject.Attachment, opts appjob.SendOptions) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopping {
		return 0, apperror.New(503, "conversation application is shutting down", nil)
	}
	if q.full {
		return 0, apperror.New(429, "conversation queue full", nil)
	}
	q.enqueued = append(q.enqueued, convID+": "+text)
	q.kept = append(q.kept, attachments)
	q.relays = append(q.relays, opts.Relay)
	q.depth++
	return q.depth, nil
}

func (q *fakeQueue) QueueDepth(string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.depth
}

// perRoomQueue answers the depth per room, the way the worker does — the
// shared fake answers one number for every room.
type perRoomQueue struct {
	*fakeQueue
	waits map[string]int
}

func (q perRoomQueue) QueueDepth(convID string) int { return q.waits[convID] }

type fakeProjects struct{ drepo.Project }

func (fakeProjects) Get(_ context.Context, id string) (*aggregate.Project, error) {
	if id == "p1" {
		return aggregate.NewProject("p1", "Site", []string{"/tmp/site"}, time.Now())
	}
	return nil, apperror.NewNotFoundError("project", nil)
}

func (f fakeProjects) List(ctx context.Context) ([]aggregate.Project, error) {
	p, _ := f.Get(ctx, "p1")
	return []aggregate.Project{*p}, nil
}

func setup(t *testing.T) (*App, *fakeRooms, *fakeTurns) {
	t.Helper()
	return setupWith(t, nil, nil)
}

// setupWith builds the app through New with the queue and upload store the
// caller names, so a branch is exercised through the composition: a parameter
// New drops fails these tests instead of passing silently. A nil queue gets
// the fake one; nil uploads wires none.
func setupWith(t *testing.T, queue Queue, uploads drepo.Uploads) (*App, *fakeRooms, *fakeTurns) {
	t.Helper()
	rooms := &fakeRooms{msgs: map[string][]entity.Message{}, named: map[string]string{}, infos: []drepo.ConversationInfo{
		{ID: "c_self", Label: "otter", Title: "Planner", ProviderID: "prov", ModelID: "m1"},
		{ID: "c_other", Label: "crow", Title: "Builder", AgentID: "claude", ProjectID: "p1"},
	}}
	rooms.add("c_other", enum.RoleUser, "build it")
	rooms.add("c_other", enum.RoleAssistant, "built")
	turns := &fakeTurns{runs: make(chan run, 4), busy: map[string]bool{}, rooms: rooms}
	if queue == nil {
		queue = &fakeQueue{}
	}
	return New(context.Background(), rooms, turns, queue, uploads, fakeProjects{}, nil, nil, nil), rooms, turns
}

type fakeChats struct {
	origin map[string]valueobject.ChatRef
	sent   []string
}

func (f *fakeChats) Origin(convID string) (valueobject.ChatRef, bool) {
	c, ok := f.origin[convID]
	return c, ok
}

func (f *fakeChats) Send(_ context.Context, chat valueobject.ChatRef, text string) error {
	f.sent = append(f.sent, chat.Key()+": "+text)
	return nil
}

func TestSearchExportReport(t *testing.T) {
	_, rooms, turns := setup(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "c_self"), 0o700); err != nil {
		t.Fatal(err)
	}
	outputs, err := repoconversation.NewToolOutputRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	chat, _ := valueobject.NewChatRef("telegram", "42", "")
	chats := &fakeChats{origin: map[string]valueobject.ChatRef{"c_self": chat}}
	app := New(context.Background(), rooms, turns, &fakeQueue{}, nil, fakeProjects{}, chats, outputs, nil)
	ctx := context.Background()

	out, err := app.Search(ctx, "c_self", SearchInput{Query: "BUILT"})
	if err != nil {
		t.Fatal(err)
	}
	hits := out["hits"].([]Hit)
	if len(hits) != 1 || hits[0].ThreadID != "c_other" || hits[0].Seq != 1 || hits[0].Snippet != "built" {
		t.Fatalf("hits = %+v", hits)
	}
	if out, _ := app.Search(ctx, "c_self", SearchInput{Query: "nothing like this"}); len(out["hits"].([]Hit)) != 0 {
		t.Fatalf("no-match hits = %v", out["hits"])
	}

	exp, err := app.Export(ctx, "c_self", ExportInput{Project: "Site"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exp["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) != 2 || !strings.Contains(lines[1], `"text":"built"`) {
		t.Fatalf("export = %s", raw)
	}

	if r, err := app.Report(ctx, "c_self", "halfway there"); err != nil || r["sent"] != true || len(chats.sent) != 1 {
		t.Fatalf("report = %v, %v, sent %v", r, err, chats.sent)
	}
	if _, err := app.Report(ctx, "c_self", "again"); apperror.As(err).StatusCode() != 429 {
		t.Fatalf("second report within a minute = %v", err)
	}
	if r, _ := app.Report(ctx, "c_other", "hi"); r["sent"] != false {
		t.Fatalf("report from a web turn = %v", r)
	}
}

func TestListAndRead(t *testing.T) {
	app, _, _ := setup(t)
	ctx := context.Background()
	out, err := app.List(ctx, "c_self", ListInput{Project: "Site"})
	if err != nil {
		t.Fatal(err)
	}
	threads := out["threads"].([]Thread)
	if len(threads) != 1 || threads[0].ID != "c_other" || threads[0].Runtime != "agent claude" {
		t.Fatalf("list by project = %+v", threads)
	}
	out, _ = app.List(ctx, "c_self", ListInput{})
	if all := out["threads"].([]Thread); len(all) != 2 || !all[0].IsSelf {
		t.Fatalf("list = %+v", all)
	}

	read, err := app.Read(ctx, "c_self", ReadInput{Thread: "@crow"})
	if err != nil {
		t.Fatal(err)
	}
	msgs := read["messages"].([]Message)
	if len(msgs) != 2 || msgs[1].Text != "built" || msgs[1].Seq != 1 {
		t.Fatalf("read by label = %+v", msgs)
	}
	after := 0
	read, _ = app.Read(ctx, "c_self", ReadInput{Thread: "c_other", AfterSeq: &after})
	if msgs := read["messages"].([]Message); len(msgs) != 1 || msgs[0].Seq != 1 {
		t.Fatalf("read after 0 = %+v", msgs)
	}
	if _, err := app.Read(ctx, "c_self", ReadInput{Thread: "@nobody"}); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("unknown thread = %v", err)
	}
}

func TestSendStopAndSpawn(t *testing.T) {
	app, rooms, turns := setup(t)
	ctx := context.Background()

	if _, err := app.Send(ctx, "c_self", SendInput{Thread: "c_self", Text: "hi"}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("send to self = %v", err)
	}
	if _, err := app.Send(ctx, "c_self", SendInput{Thread: "@crow", Text: "ship it"}); err != nil {
		t.Fatal(err)
	}
	r := <-turns.runs
	if r.conv != "c_other" || r.provider != "" || r.agent != "" || r.text != "ship it" {
		t.Fatalf("send ran %+v — the body must be the owner's text alone", r)
	}
	if r.relay == nil || r.relay.ID != "c_self" || r.relay.Label != "otter" || r.relay.Title != "Planner" {
		t.Fatalf("relay = %+v, want the sending thread's identity", r.relay)
	}
	if r.origin.Kind != enum.TurnOriginConversation {
		t.Fatalf("send origin = %+v, want another thread", r.origin)
	}

	// A busy thread takes the message into its queue, never into the running
	// turn: the relay provenance only exists on a stored user-role message.
	turns.busy["c_other"] = true
	out, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "also this"})
	if err != nil || out["delivery"] != "queued" {
		t.Fatalf("send to busy = %v, %v, want queued", out, err)
	}
	stop, _ := app.Stop(ctx, "c_self", StopInput{Thread: "c_other"})
	if stop["stopped"] != true {
		t.Fatalf("stop = %v", stop)
	}

	// Spawn runs on the caller's runtime unless told otherwise, in a
	// project — and the name is already on the thread while its first
	// turn still runs, not after the turn ends.
	gate := make(chan struct{})
	turns.mu.Lock()
	turns.gate = gate
	turns.mu.Unlock()
	out, err = app.Spawn(ctx, "c_self", SpawnInput{Prompt: "research", Title: "Research", Project: "Site"})
	if err != nil {
		t.Fatal(err)
	}
	r = <-turns.runs
	if r.conv != out["threadId"] || r.provider != "prov" || r.model != "m1" || r.project != "p1" {
		t.Fatalf("spawn ran %+v (out %v)", r, out)
	}
	// The run is still blocked — the title must already be visible.
	info, err := rooms.Info(ctx, r.conv)
	if err != nil || info.Title != "Research" {
		t.Fatalf("spawned thread's title during its turn = %+v, %v", info, err)
	}
	close(turns.gate)
	turns.mu.Lock()
	turns.gate = nil
	turns.mu.Unlock()
}

func TestRelayHeadNeverEchoesAPromptTitle(t *testing.T) {
	// A room whose default title was a relayed spawn prompt: it leads with an
	// embedded relay head and drags the prompt's lines behind. The record keeps
	// one bounded line; the head renders from it alone.
	noisy := "[From thread @bear Nested echo (c_W1ABC)]\n" +
		"[Your answer here does not reach that thread. If it needs a reply, send one to c_W1ABC with the conversation MCP tool send.]\n\n" +
		"A long spawn prompt spanning " + strings.Repeat("many ", 40) + "lines\nlast"
	relay := valueobject.NewRelay("c_echo", "", noisy)
	head := relay.Head()
	first, body, _ := strings.Cut(head, "\n")
	if !strings.HasPrefix(first, "[From thread ") || !strings.HasSuffix(first, " (c_echo)]") {
		t.Fatalf("head shape: %q", first)
	}
	if strings.Count(head, "[From thread") != 1 || strings.Count(head, "[Your answer") != 1 || len([]rune(first)) > 110 {
		t.Fatalf("head carries an echo: %q", head)
	}
	if !strings.Contains(body, "Your answer here does not reach that thread") {
		t.Fatalf("the reply notice is part of the head: %q", head)
	}
	// A title that is nothing but a head leaves no name behind: the id names it.
	void := valueobject.NewRelay("c_void", "", "[From thread x (c_1)]")
	if first, _, _ := strings.Cut(void.Head(), "\n"); first != "[From thread c_void (c_void)]" {
		t.Fatalf("a title that is only a head must fall back to the id: %q", first)
	}
}

func TestWait(t *testing.T) {
	app, rooms, _ := setup(t)
	ctx := context.Background()
	after := 1
	go func() {
		time.Sleep(300 * time.Millisecond)
		rooms.add("c_other", enum.RoleAssistant, "later")
	}()
	out, err := app.Wait(ctx, "c_self", WaitInput{Thread: "c_other", AfterSeq: &after, Timeout: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out["timedOut"] != false || len(out["messages"].([]Message)) != 1 {
		t.Fatalf("wait = %v", out)
	}
	after = 2
	start := time.Now()
	out, _ = app.Wait(ctx, "c_self", WaitInput{Thread: "c_other", AfterSeq: &after, Timeout: 1})
	if out["timedOut"] != true || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("wait timeout = %v after %s", out, time.Since(start))
	}
}

// A busy room is not a refusal, and a relayed prompt never folds into the
// running turn: steer writes no user-role record, so the provenance that tells
// a tab where the message came from would be lost with it. The message waits in
// the room's own queue — arrival order, and the surface already shows how many
// wait.
func TestSendQueuesOnABusyThread(t *testing.T) {
	q := &fakeQueue{}
	app, _, turns := setupWith(t, q, nil)
	ctx := context.Background()
	turns.busy["c_other"] = true

	out, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "and this"})
	if err != nil {
		t.Fatal(err)
	}
	if out["delivery"] != "queued" || out["position"] != 1 {
		t.Fatalf("busy send = %v, want queued at position 1", out)
	}
	if _, ok := out["afterSeq"]; ok {
		t.Fatalf("queued carries afterSeq %v — an acceptance is not a promise", out["afterSeq"])
	}
	if len(q.enqueued) != 1 || q.enqueued[0] != "c_other: and this" {
		t.Fatalf("enqueued = %v, want the sender's text alone", q.enqueued)
	}
	if len(q.relays) != 1 || q.relays[0] == nil || q.relays[0].ID != "c_self" {
		t.Fatalf("relays = %v, want the provenance carried beside the text", q.relays)
	}

	// A second message queues behind the first: arrival order, not a race.
	out, err = app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "third"})
	if err != nil || out["delivery"] != "queued" || out["position"] != 2 {
		t.Fatalf("second queued = %v, %v, want position 2", out, err)
	}
}

// A thread waiting on the owner's permission answer is refused, not queued:
// a queue would wait behind a human.
func TestSendRefusesAWaitingApproval(t *testing.T) {
	app, rooms, _ := setup(t)
	rooms.mu.Lock()
	rooms.approvals = map[string][]valueobject.ApprovalRequest{"c_other": {{}}}
	rooms.mu.Unlock()
	_, err := app.Send(context.Background(), "c_self", SendInput{Thread: "c_other", Text: "hello"})
	if code := apperror.As(err).StatusCode(); code != 409 {
		t.Fatalf("waiting approval = %v (status %d), want 409", err, code)
	}
	if !strings.Contains(err.Error(), "permission answer") {
		t.Fatalf("refusal = %v, want it to name the reason", err)
	}
}

// A full queue is refused with the depth, and nothing is written.
func TestSendRefusesAFullQueue(t *testing.T) {
	q := &fakeQueue{full: true, depth: 7}
	app, _, turns := setupWith(t, q, nil)
	turns.busy["c_other"] = true
	_, err := app.Send(context.Background(), "c_self", SendInput{Thread: "c_other", Text: "hello"})
	if code := apperror.As(err).StatusCode(); code != 429 {
		t.Fatalf("full queue = %v (status %d), want 429", err, code)
	}
	if !strings.Contains(err.Error(), "7 waiting") {
		t.Fatalf("refusal = %v, want the depth named", err)
	}
	if len(q.enqueued) != 0 {
		t.Fatalf("a refused message was written: %v", q.enqueued)
	}
}

// A stopping server refuses instead of accepting a message it cannot keep.
func TestSendRefusesWhileStopping(t *testing.T) {
	app, _, turns := setupWith(t, &fakeQueue{stopping: true}, nil)
	turns.busy["c_other"] = true
	_, err := app.Send(context.Background(), "c_self", SendInput{Thread: "c_other", Text: "hello"})
	if code := apperror.As(err).StatusCode(); code != 503 {
		t.Fatalf("stopping = %v (status %d), want 503", err, code)
	}
}

// Stop cuts the live turn only: accepted messages keep waiting, and the count
// says so — a Stop that silently dropped them would be the bigger surprise.
func TestStopKeepsTheQueueAndReportsIt(t *testing.T) {
	q := &fakeQueue{depth: 3}
	app, _, turns := setupWith(t, q, nil)
	turns.busy["c_other"] = true
	out, err := app.Stop(context.Background(), "c_self", StopInput{Thread: "c_other"})
	if err != nil {
		t.Fatal(err)
	}
	if out["stopped"] != true || out["waiting"] != 3 {
		t.Fatalf("stop = %v, want stopped with 3 waiting", out)
	}
	// The thread row counts through the same queue, so the two can never
	// disagree about what survived the Stop.
	if th, ok := out["thread"].(Thread); !ok || th.Waiting != 3 {
		t.Fatalf("stop thread = %v, want the same 3 waiting", out["thread"])
	}
	if q.QueueDepth("c_other") != 3 {
		t.Fatal("Stop dropped the queue")
	}
}

// Every thread row names the room's wait, so a caller that never saw a
// conversation.queued frame still reads the count; an idle room carries no
// field at all.
func TestThreadRowsCarryTheRoomsWait(t *testing.T) {
	app, _, _ := setupWith(t, perRoomQueue{&fakeQueue{}, map[string]int{"c_other": 2}}, nil)
	out, err := app.List(context.Background(), "c_self", ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	rows := out["threads"].([]Thread)
	if len(rows) != 2 {
		t.Fatalf("threads = %v", rows)
	}
	for _, row := range rows {
		want := 0
		if row.ID == "c_other" {
			want = 2
		}
		if row.Waiting != want {
			t.Fatalf("%s waiting = %d, want %d", row.ID, row.Waiting, want)
		}
	}
	// The wire form is the contract: nothing waiting means no field.
	raw, err := json.Marshal(Thread{ID: "c_self", Title: "Planner"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "waiting") {
		t.Fatalf("an idle thread row carries a waiting field: %s", raw)
	}
}

// An idle room with someone already waiting queues behind them instead of
// starting a second turn ahead of the queue.
func TestSendQueuesBehindWaitingMessages(t *testing.T) {
	app, _, _ := setupWith(t, &fakeQueue{depth: 1}, nil)
	out, err := app.Send(context.Background(), "c_self", SendInput{Thread: "c_other", Text: "hello"})
	if err != nil || out["delivery"] != "queued" || out["position"] != 2 {
		t.Fatalf("idle with a queue = %v, %v", out, err)
	}
}
