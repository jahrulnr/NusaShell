package appcontrol

import (
	"context"
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
	mu    sync.Mutex
	infos []drepo.ConversationInfo
	msgs  map[string][]entity.Message
	named map[string]string
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

func (f *fakeRooms) PendingApprovals(string) []valueobject.ApprovalRequest { return nil }

func (f *fakeRooms) Annotate(_ context.Context, id string, a appconversation.Annotation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.named[id] = *a.Title
	return nil
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
}

type fakeTurns struct {
	mu    sync.Mutex
	runs  chan run
	busy  map[string]bool
	steer []string
	rooms *fakeRooms
	gate  chan struct{}
}

func (f *fakeTurns) Run(_ context.Context, convID, provider, model, text string, _ []valueobject.Attachment, opts appjob.SendOptions, _ event.TurnEventHandler) (string, appconversation.SendResult, error) {
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
	f.runs <- run{convID, provider, model, opts.Agent, opts.Project, text, opts.Origin}
	if f.gate != nil {
		<-f.gate
	}
	return convID, appconversation.SendResult{}, nil
}

func (f *fakeTurns) Steer(convID, text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steer = append(f.steer, convID+": "+text)
	return true
}

func (f *fakeTurns) Cancel(convID string) bool { return f.Busy(convID) }

func (f *fakeTurns) Busy(convID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy[convID]
}

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
	rooms := &fakeRooms{msgs: map[string][]entity.Message{}, named: map[string]string{}, infos: []drepo.ConversationInfo{
		{ID: "c_self", Label: "otter", Title: "Planner", ProviderID: "prov", ModelID: "m1"},
		{ID: "c_other", Label: "crow", Title: "Builder", AgentID: "claude", ProjectID: "p1"},
	}}
	rooms.add("c_other", enum.RoleUser, "build it")
	rooms.add("c_other", enum.RoleAssistant, "built")
	turns := &fakeTurns{runs: make(chan run, 4), busy: map[string]bool{}, rooms: rooms}
	return New(context.Background(), rooms, turns, fakeProjects{}, nil, nil, nil), rooms, turns
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
	app := New(context.Background(), rooms, turns, fakeProjects{}, chats, outputs, nil)
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
	if r.conv != "c_other" || r.provider != "" || r.agent != "" || !strings.HasPrefix(r.text, "[From thread @otter Planner (c_self)]\n[Your answer here does not reach that thread. If it needs a reply, send one to c_self with the conversation MCP tool send.]\n\nship it") {
		t.Fatalf("send ran %+v", r)
	}
	if r.origin.Kind != enum.TurnOriginConversation {
		t.Fatalf("send origin = %+v, want another thread", r.origin)
	}

	// A busy thread takes the text into its running turn.
	turns.busy["c_other"] = true
	out, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "also this"})
	if err != nil || out["delivery"] != "steer" || len(turns.steer) != 1 {
		t.Fatalf("send to busy = %v, %v, steer %v", out, err, turns.steer)
	}
	stop, _ := app.Stop(ctx, "c_self", StopInput{Thread: "c_other"})
	if stop["stopped"] != true {
		t.Fatalf("stop = %v", stop)
	}

	// Spawn runs on the caller's runtime unless told otherwise, in a
	// project — and the name is already on the thread while its first
	// turn still runs, not after the turn ends.
	turns.gate = make(chan struct{})
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
	turns.gate = nil
}

func TestRelayHeadNeverEchoesAPromptTitle(t *testing.T) {
	app, rooms, turns := setup(t)
	ctx := context.Background()
	// A room whose default title was a relayed spawn prompt: it leads
	// with an embedded relay head and drags the prompt's lines behind.
	rooms.mu.Lock()
	rooms.infos = append(rooms.infos,
		drepo.ConversationInfo{ID: "c_echo", Title: "[From thread @bear Nested echo (c_W1ABC)]\n" +
			"[Your answer here does not reach that thread. If it needs a reply, send one to c_W1ABC with the conversation MCP tool send.]\n\n" +
			"A long spawn prompt spanning " + strings.Repeat("many ", 40) + "lines\nlast"},
		drepo.ConversationInfo{ID: "c_void", Title: "[From thread x (c_1)]"})
	rooms.mu.Unlock()

	if _, err := app.Send(ctx, "c_echo", SendInput{Thread: "c_self", Text: "report"}); err != nil {
		t.Fatal(err)
	}
	r := <-turns.runs
	head, body, _ := strings.Cut(r.text, "\n")
	if !strings.HasPrefix(head, "[From thread ") || !strings.HasSuffix(head, " (c_echo)]") {
		t.Fatalf("head shape: %q", head)
	}
	if strings.Count(head, "[From thread") != 1 || strings.Contains(head, "[Your answer") || len([]rune(head)) > 110 {
		t.Fatalf("head carries an echo: %q", head)
	}
	if !strings.HasSuffix(body, "report") {
		t.Fatalf("body: %q", body)
	}

	// A title that is nothing but relay blocks falls back to the id.
	if _, err := app.Send(ctx, "c_void", SendInput{Thread: "c_self", Text: "ping"}); err != nil {
		t.Fatal(err)
	}
	r = <-turns.runs
	if head, _, _ := strings.Cut(r.text, "\n"); head != "[From thread c_void (c_void)]" {
		t.Fatalf("fallback head: %q", head)
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
