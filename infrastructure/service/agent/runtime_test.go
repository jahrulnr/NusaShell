package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
)

var (
	fakeOnce sync.Once
	fakePath string
	fakeErr  error
)

// buildFake compiles cmd/fake-acp-agent once for the package's tests.
func buildFake(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fake-acp-")
		if err != nil {
			fakeErr = err
			return
		}
		name := "fake-acp"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		fakePath = filepath.Join(dir, name)
		repo, err := filepath.Abs("../../..")
		if err != nil {
			fakeErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", fakePath, "./cmd/fake-acp-agent")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeErr = err
			t.Logf("%s", out)
		}
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	return fakePath
}

type recorder struct {
	mu     sync.Mutex
	events []event.TurnEvent
}

func (r *recorder) handle(ev event.TurnEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *recorder) kinds() map[enum.TurnEventKind]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[enum.TurnEventKind]int{}
	for _, ev := range r.events {
		out[ev.Kind()]++
	}
	return out
}

func setup(t *testing.T, text string) (*Runtime, dservice.AgentPrompt) {
	t.Helper()
	a, err := aggregate.NewAgent("fake", "Fake", buildFake(t), nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := aggregate.NewConversation("")
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.BindAgent(a.ID()); err != nil {
		t.Fatal(err)
	}
	user, err := entity.NewMessage(enum.RoleUser, text)
	if err != nil {
		t.Fatal(err)
	}
	conv.Append(user)
	rt := New(Options{ClientName: "nusashell-test", ClientVersion: "0"})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	return rt, dservice.AgentPrompt{Agent: &a, Conversation: conv, Workspace: t.TempDir(), Input: user}
}

func TestPromptAllowedToolTurn(t *testing.T) {
	rt, in := setup(t, "please inspect")
	rec := &recorder{}
	var asked []valueobject.ApprovalRequest
	approve := func(_ context.Context, req valueobject.ApprovalRequest) (string, error) {
		asked = append(asked, req)
		opt, _ := req.Option("allow-once")
		return opt.ID, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, rec.handle, approve); err != nil {
		t.Fatal(err)
	}
	if in.Conversation.AgentSession() == "" {
		t.Fatal("agent session was not recorded on the conversation")
	}
	if len(asked) != 1 || asked[0].Action() != enum.ToolActionRead || asked[0].CallID() != "fake-tool-1" {
		t.Fatalf("approval requests = %+v", asked)
	}
	last := in.Conversation.Last()
	if last.Role() != enum.RoleAssistant || last.FinishReason() != enum.FinishReasonStop {
		t.Fatalf("last message role=%s finish=%s", last.Role(), last.FinishReason())
	}
	if !strings.Contains(last.Content(), "README.md and AGENTS.md") {
		t.Fatalf("answer = %q", last.Content())
	}
	calls := last.ToolCalls()
	if len(calls) != 1 || calls[0].Type() != enum.TooltypeDelegated || calls[0].Output() != "README.md\nAGENTS.md" {
		t.Fatalf("tool calls = %+v", calls)
	}
	var kinds []string
	for _, p := range last.Parts() {
		kinds = append(kinds, p.Kind)
	}
	if strings.Join(kinds, ",") != "reasoning,plan,tool_call,approval,text" {
		t.Fatalf("parts = %v", kinds)
	}
	got := rec.kinds()
	if got[enum.TurnEventToolCall] != 1 || got[enum.TurnEventToolOutput] == 0 || got[enum.TurnEventFinished] != 1 || got[enum.TurnEventDelta] == 0 {
		t.Fatalf("events = %v", got)
	}
	// The agent's usage_update is its account of its context window.
	rec.mu.Lock()
	var reports []valueobject.AgentContext
	for _, ev := range rec.events {
		if ev.Kind() == enum.TurnEventContext {
			reports = append(reports, ev.AgentContext())
		}
	}
	rec.mu.Unlock()
	if len(reports) != 1 || reports[0].Used() != 1280 || reports[0].Size() != 200000 {
		t.Fatalf("context reports = %+v", reports)
	}
	if u := in.Conversation.Usage(); u.Total() != 0 {
		t.Fatalf("the adapter must not account usage on the conversation itself, got %d", u.Total())
	}
}

func TestPromptRejectedTool(t *testing.T) {
	rt, in := setup(t, "please inspect")
	approve := func(_ context.Context, req valueobject.ApprovalRequest) (string, error) {
		opt, _ := req.RejectAnswer()
		return opt.ID, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, nil, approve); err != nil {
		t.Fatal(err)
	}
	last := in.Conversation.Last()
	if !strings.Contains(last.Content(), "did not get permission") {
		t.Fatalf("answer = %q", last.Content())
	}
	if calls := last.ToolCalls(); len(calls) != 1 || !strings.Contains(calls[0].Output(), "permission denied") {
		t.Fatalf("tool calls = %+v", calls)
	}
}

func TestPromptCancelWhileAwaitingApproval(t *testing.T) {
	rt, in := setup(t, "please inspect")
	ctx, cancel := context.WithCancel(context.Background())
	approve := func(actx context.Context, _ valueobject.ApprovalRequest) (string, error) {
		cancel()
		<-actx.Done()
		return "", actx.Err()
	}
	err := rt.Prompt(ctx, in, nil, approve)
	if apperror.As(err).StatusCode() != 408 {
		t.Fatalf("err = %v, want 408", err)
	}
	last := in.Conversation.Last()
	if last.Role() != enum.RoleAssistant || last.FinishReason() != enum.FinishReasonInterrupted {
		t.Fatalf("last message role=%s finish=%s", last.Role(), last.FinishReason())
	}
	// The session survives the cancel: the next prompt reuses it.
	session := in.Conversation.AgentSession()
	user, _ := entity.NewMessage(enum.RoleUser, "write a poem")
	in.Conversation.Append(user)
	in.Input = user
	allow := func(_ context.Context, req valueobject.ApprovalRequest) (string, error) {
		opt, _ := req.AutoAnswer(enum.ApprovalPolicyAutoAll)
		return opt.ID, nil
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	if err := rt.Prompt(ctx2, in, nil, allow); err != nil {
		t.Fatal(err)
	}
	if in.Conversation.AgentSession() != session {
		t.Fatalf("session changed from %s to %s", session, in.Conversation.AgentSession())
	}
	if !strings.Contains(in.Conversation.Last().Content(), "quiet tide") {
		t.Fatalf("answer = %q", in.Conversation.Last().Content())
	}
}

func TestPromptRestartsAgentAfterConfigChange(t *testing.T) {
	rt, in := setup(t, "write a poem")
	allow := func(_ context.Context, req valueobject.ApprovalRequest) (string, error) {
		opt, _ := req.AutoAnswer(enum.ApprovalPolicyAutoAll)
		return opt.ID, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, nil, allow); err != nil {
		t.Fatal(err)
	}
	first := rt.procs["fake"]
	if err := in.Agent.SetEnv(map[string]string{"FAKE_ACP_UNUSED": "1"}); err != nil {
		t.Fatal(err)
	}
	user, _ := entity.NewMessage(enum.RoleUser, "another poem")
	in.Conversation.Append(user)
	in.Input = user
	if err := rt.Prompt(ctx, in, nil, allow); err != nil {
		t.Fatal(err)
	}
	if rt.procs["fake"] == first {
		t.Fatal("a changed launch configuration must start a new process")
	}
}

func TestModelsAndModelSwitch(t *testing.T) {
	rt, in := setup(t, "write a poem")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := rt.Models(ctx, in.Agent)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "fake-fast" || !models[0].Current || models[1].ID != "fake-smart" {
		t.Fatalf("models = %+v", models)
	}
	in.Model = "fake-smart"
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	s := rt.sessions[in.Conversation.ID()]
	if _, current, _ := modelOption(s.configs); current != "fake-smart" {
		t.Fatalf("session model = %q, want fake-smart", current)
	}
	in.Model = "no-such-model"
	if err := rt.Prompt(ctx, in, nil, nil); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unknown model err = %v, want 400", err)
	}
}

func TestDelegatedCallsReplayAsTextForModels(t *testing.T) {
	rt, in := setup(t, "please inspect")
	allow := func(_ context.Context, req valueobject.ApprovalRequest) (string, error) {
		opt, _ := req.AutoAnswer(enum.ApprovalPolicyAutoAll)
		return opt.ID, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, nil, allow); err != nil {
		t.Fatal(err)
	}
	replay := in.Conversation.ModelReplay()
	last := replay[len(replay)-1]
	if len(last.ToolCalls()) != 0 {
		t.Fatalf("a model must not replay agent-run calls as its own: %+v", last.ToolCalls())
	}
	if !strings.Contains(last.Content(), "[agent tool: Read workspace files]") || !strings.Contains(last.Content(), "README.md") {
		t.Fatalf("replayed content = %q", last.Content())
	}
	if len(in.Conversation.Last().ToolCalls()) != 1 {
		t.Fatal("the stored history must keep the agent's call")
	}
}

func TestSessionReopensWhenFoldersChange(t *testing.T) {
	rt, in := setup(t, "write a poem")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	first := rt.sessions[in.Conversation.ID()]
	moved := t.TempDir()
	in.Workspace = moved
	user, _ := entity.NewMessage(enum.RoleUser, "another poem")
	in.Conversation.Append(user)
	in.Input = user
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	second := rt.sessions[in.Conversation.ID()]
	if second == first || second.folders[0] != moved {
		t.Fatalf("session was not reopened under the new folder: %+v", second.folders)
	}
	if second.acpID != first.acpID {
		t.Fatalf("the agent-side session must be restored, not replaced: %s → %s", first.acpID, second.acpID)
	}
}

// Agents read and write files inside their session folders through the
// client, never secret files and never outside.
func TestClientFiles(t *testing.T) {
	rt, in := setup(t, "fs-demo")
	rt.opts.Workspace = repoworkspace.NewLocal()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	got := in.Conversation.Last().Content()
	if got != `read: "line two\n" env-refused: true outside-refused: true` {
		t.Fatalf("answer = %q", got)
	}
	if data, err := os.ReadFile(filepath.Join(in.Workspace, "notes", "hello.txt")); err != nil || string(data) != "line one\nline two\n" {
		t.Fatalf("file = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(in.Workspace, ".env")); err == nil {
		t.Fatal(".env must not be written")
	}
}

// A terminal the agent runs streams into the tool call showing it.
func TestClientTerminal(t *testing.T) {
	rt, in := setup(t, "timeline-terminal-test")
	rec := &recorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, rec.handle, nil); err != nil {
		t.Fatal(err)
	}
	calls := in.Conversation.Last().ToolCalls()
	if len(calls) != 1 || !strings.Contains(calls[0].Output(), "second") {
		t.Fatalf("tool calls = %+v", calls)
	}
}

// Options, legacy modes, and commands the agent announces at session start
// are known after the first turn; a picked option applies to the session.
func TestOptionsAndCommands(t *testing.T) {
	rt, in := setup(t, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts, err := rt.Options(ctx, in.Agent, in.Conversation.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) == 0 {
		t.Fatal("the agent's default options are listed before any turn")
	}
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	cmds := rt.Commands(in.Conversation.ID())
	if len(cmds) == 0 || cmds[0].Name != "compact" {
		t.Fatalf("commands = %+v", cmds)
	}
	live, _ := rt.Options(ctx, in.Agent, in.Conversation.ID())
	var mode *dservice.AgentOption
	for i := range live {
		if live[i].Category == "mode" {
			mode = &live[i]
		}
	}
	if mode == nil || len(mode.Choices) < 2 {
		t.Fatalf("options = %+v", live)
	}
	next := mode.Choices[len(mode.Choices)-1].Value
	if err := rt.SetOption(ctx, in.Conversation.ID(), mode.ID, next); err != nil {
		t.Fatal(err)
	}
	after, _ := rt.Options(ctx, in.Agent, in.Conversation.ID())
	for _, o := range after {
		if o.ID == mode.ID && o.Current != next {
			t.Fatalf("mode after set = %q, want %q", o.Current, next)
		}
	}
}

// liveMode answers the room's mode option after a turn opened its session —
// the option whose choices the agent announced.
func liveMode(t *testing.T, ctx context.Context, rt *Runtime, in dservice.AgentPrompt) dservice.AgentOption {
	t.Helper()
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	opts, err := rt.Options(ctx, in.Agent, in.Conversation.ID())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		if o.Category == "mode" && len(o.Choices) > 1 {
			return o
		}
	}
	t.Fatalf("no mode with choices in %+v", opts)
	return dservice.AgentOption{}
}

// currentOption answers one option's current value on the live session.
func currentOption(t *testing.T, ctx context.Context, rt *Runtime, in dservice.AgentPrompt, id string) string {
	t.Helper()
	opts, err := rt.Options(ctx, in.Agent, in.Conversation.ID())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		if o.ID == id {
			return o.Current
		}
	}
	t.Fatalf("option %q is gone from %+v", id, opts)
	return ""
}

// A value the option does not list is refused, not swallowed: a stale tab must
// not look like it applied, and the caller must not record what the session
// never took.
func TestSetOptionRefusesAValueTheOptionDoesNotOffer(t *testing.T) {
	rt, in := setup(t, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mode := liveMode(t, ctx, rt, in)
	before := currentOption(t, ctx, rt, in, mode.ID)

	err := rt.SetOption(ctx, in.Conversation.ID(), mode.ID, "ludicrous")
	if code := apperror.As(err).StatusCode(); code != 400 {
		t.Fatalf("a value the option does not list = %v (status %d), want 400", err, code)
	}
	if msg := err.Error(); !strings.Contains(msg, mode.ID) || !strings.Contains(msg, "ludicrous") {
		t.Fatalf("the refusal must name the option and the value: %q", msg)
	}
	// Nothing was applied: the session still carries the value it had.
	if got := currentOption(t, ctx, rt, in, mode.ID); got != before {
		t.Fatalf("mode after a refused pick = %q, want %q untouched", got, before)
	}
	// A value the option does list applies as before.
	next := ""
	for _, c := range mode.Choices {
		if c.Value != before {
			next = c.Value
			break
		}
	}
	if err := rt.SetOption(ctx, in.Conversation.ID(), mode.ID, next); err != nil {
		t.Fatal(err)
	}
	if got := currentOption(t, ctx, rt, in, mode.ID); got != next {
		t.Fatalf("mode after a listed pick = %q, want %q", got, next)
	}
}

// An option this session never announced is skipped, not refused: it belonged
// to another agent version, and refusing it would break a picker bound to a
// stale agent record.
func TestSetOptionSkipsAnOptionTheSessionDoesNotOffer(t *testing.T) {
	rt, in := setup(t, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	liveMode(t, ctx, rt, in) // open the session
	if err := rt.SetOption(ctx, in.Conversation.ID(), "from-another-agent-version", "x"); err != nil {
		t.Fatalf("an option the session does not offer = %v, want it skipped", err)
	}
}

// A config_option_update is the authoritative offered list: an option it
// does not name is gone, not merged into what the session reported before.
// Merge-by-id kept ghosts a later set_config_option refuses with -32602.
func TestConfigOptionUpdateReplacesTheOfferedList(t *testing.T) {
	rt, in := setup(t, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	liveMode(t, ctx, rt, in) // open the session: [mode, model, strict] offered
	s := rt.sessions[in.Conversation.ID()]
	if s == nil {
		t.Fatal("no live session")
	}
	if got := currentOption(t, ctx, rt, in, "strict"); got == "" {
		t.Fatal("precondition: strict is offered")
	}
	// The agent re-publishes its list without the strict option.
	update := json.RawMessage(`{"sessionUpdate":"config_option_update","configOptions":[` +
		`{"id":"mode","name":"Session Mode","category":"mode","type":"select","currentValue":"ask","options":[{"value":"ask","name":"Ask"}]},` +
		`{"id":"model","name":"Model","category":"model","type":"select","currentValue":"fake-fast","options":[{"value":"fake-fast","name":"Fake Fast"}]}]}`)
	h := &handler{runtime: rt, proc: s.proc}
	s.mu.Lock()
	seq := s.configSeq + 1
	s.mu.Unlock()
	h.OnNotification("session/update", s.acpID, update, seq)
	// The stored list is exactly what the update carried — not a union.
	var ids []struct {
		ID string `json:"id"`
	}
	s.mu.Lock()
	_ = json.Unmarshal(s.configs, &ids)
	s.mu.Unlock()
	if len(ids) != 2 || ids[0].ID != "mode" || ids[1].ID != "model" {
		t.Fatalf("session configs = %+v, want [mode model]", ids)
	}
	opts, err := rt.Options(ctx, in.Agent, in.Conversation.ID())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		if o.ID == "strict" {
			t.Fatalf("the dropped option is still offered: %+v", opts)
		}
	}
	// And an update carrying nothing at all does not wipe the list.
	h.OnNotification("session/update", s.acpID, json.RawMessage(`{"sessionUpdate":"config_option_update"}`), seq+1)
	s.mu.Lock()
	empty := len(s.configs) == 0
	s.mu.Unlock()
	if empty {
		t.Fatal("an update without configOptions must not clear the list")
	}
}

// B-041: the wire is ordered but notification handling and the waiting
// caller are not, so an update dispatched before a set_config_option
// response can still be processed after it. The wire sequence stamps every
// message in read order; state applies only forward, so the response —
// newer on the wire — wins deterministically whichever goroutine ran first.
func TestConfigUpdateCannotOvertakeANewerSetConfigResponse(t *testing.T) {
	rt, in := setup(t, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	liveMode(t, ctx, rt, in)
	s := rt.sessions[in.Conversation.ID()]
	if s == nil {
		t.Fatal("no live session")
	}
	h := &handler{runtime: rt, proc: s.proc}
	// Wire order: an update carrying model=fake-fast, then the
	// set_config_option(model=fake-smart) response. Processed in the order
	// the goroutines happen to run: the response first, the stale update
	// after.
	freshSeq := s.configSeq + 2
	stale := json.RawMessage(`{"sessionUpdate":"config_option_update","configOptions":[` +
		`{"id":"model","name":"Model","category":"model","type":"select","currentValue":"fake-fast","options":[{"value":"fake-fast","name":"Fake Fast"},{"value":"fake-smart","name":"Fake Smart"}]}]}`)
	s.mu.Lock()
	s.configs = json.RawMessage(`[{"id":"model","name":"Model","category":"model","type":"select","currentValue":"fake-smart","options":[{"value":"fake-fast"},{"value":"fake-smart"}]}]`)
	s.configSeq = freshSeq
	s.mu.Unlock()
	h.OnNotification("session/update", s.acpID, stale, freshSeq-1)
	if _, current, _ := modelOption(s.configs); current != "fake-smart" {
		t.Fatalf("an update older than the applied response rolled the model back to %q", current)
	}
	// The same rule covers modes: a current_mode_update older than the
	// applied session/set_mode response cannot roll the mode back either.
	s.mu.Lock()
	s.modes = &acp.SessionModeState{CurrentModeID: "accept-edits"}
	s.modesSeq = freshSeq
	s.mu.Unlock()
	h.OnNotification("session/update", s.acpID, json.RawMessage(`{"sessionUpdate":"current_mode_update","currentModeId":"ask"}`), freshSeq-1)
	s.mu.Lock()
	mode := s.modes.CurrentModeID
	s.mu.Unlock()
	if mode != "accept-edits" {
		t.Fatalf("a stale mode update rolled the mode back to %q", mode)
	}
	// And a genuinely newer update still applies — the guard orders, it
	// does not drop.
	h.OnNotification("session/update", s.acpID, stale, freshSeq+1)
	if _, current, _ := modelOption(s.configs); current != "fake-fast" {
		t.Fatalf("a newer update was ignored, model = %q", current)
	}
}

// A pick stored before the agent narrowed its options is stale, not fatal: the
// turn runs on the session's current value rather than failing on a record the
// owner can no longer see.
func TestPromptSkipsAStaleStoredPick(t *testing.T) {
	rt, in := setup(t, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mode := liveMode(t, ctx, rt, in)
	in.Options = map[string]string{mode.ID: "ludicrous"}
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatalf("a stale stored pick failed the turn: %v", err)
	}
}

// A boolean option takes "true" or "false" and nothing else. A stale tab that
// sends "yes" must be refused: coercing it to false would record a choice the
// owner never made, and look like it applied.
func TestSetOptionRefusesABooleanThatIsNotTrueOrFalse(t *testing.T) {
	rt, in := setup(t, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	liveMode(t, ctx, rt, in) // open the session
	before := currentOption(t, ctx, rt, in, "strict")

	err := rt.SetOption(ctx, in.Conversation.ID(), "strict", "yes")
	if code := apperror.As(err).StatusCode(); code != 400 {
		t.Fatalf("a boolean pick of %q = %v (status %d), want 400", "yes", err, code)
	}
	if msg := err.Error(); !strings.Contains(msg, "strict") || !strings.Contains(msg, "yes") {
		t.Fatalf("the refusal must name the option and the value: %q", msg)
	}
	if got := currentOption(t, ctx, rt, in, "strict"); got != before {
		t.Fatalf("strict after a refused pick = %q, want %q untouched", got, before)
	}
	// Both values it does take still apply.
	for _, want := range []string{"true", "false"} {
		if err := rt.SetOption(ctx, in.Conversation.ID(), "strict", want); err != nil {
			t.Fatal(err)
		}
		if got := currentOption(t, ctx, rt, in, "strict"); got != want {
			t.Fatalf("strict after picking %q = %q", want, got)
		}
	}
}

// A stored boolean pick that never was a boolean is stale like any other: the
// turn runs instead of failing on it.
func TestPromptSkipsAStaleStoredBoolean(t *testing.T) {
	rt, in := setup(t, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	liveMode(t, ctx, rt, in) // open the session
	in.Options = map[string]string{"strict": "yes"}
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatalf("a stale stored boolean failed the turn: %v", err)
	}
}

// An agent's form request reaches the owner through Elicit, and the
// owner's answer goes back to the agent. The record keeps the outcome,
// never the content.
func TestElicitation(t *testing.T) {
	rt, in := setup(t, "elicit-demo")
	var asked []valueobject.Elicitation
	in.Elicit = func(_ context.Context, e valueobject.Elicitation) (valueobject.ElicitationAnswer, error) {
		asked = append(asked, e)
		content := []byte(`{"environment":"staging","replicas":2}`)
		if err := e.Check(content); err != nil {
			t.Errorf("a valid answer is refused: %v", err)
		}
		return valueobject.ElicitationAnswer{Action: "accept", Content: content}, nil
	}
	rec := &recorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, rec.handle, nil); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0].Mode() != "form" || !strings.Contains(asked[0].Message(), "environment") {
		t.Fatalf("asked = %+v", asked)
	}
	last := in.Conversation.Last()
	if !strings.Contains(last.Content(), `"replicas":2`) {
		t.Fatalf("the agent did not get the answer: %q", last.Content())
	}
	var record string
	var kinds []string
	for _, p := range last.Parts() {
		kinds = append(kinds, p.Kind)
		if p.Kind == entity.MessagePartElicitation {
			record = p.Text
		}
	}
	// The question sits after the text that leads to it.
	if strings.Join(kinds, ",") != "text,elicitation,text" {
		t.Fatalf("parts = %v", kinds)
	}
	if !strings.Contains(record, `"action":"accept"`) || strings.Contains(record, "staging") {
		t.Fatalf("record = %s", record)
	}
}

// Without an elicitor the request is declined and the turn goes on.
func TestElicitationWithoutOwner(t *testing.T) {
	rt, in := setup(t, "elicit-demo url")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	if c := in.Conversation.Last().Content(); !strings.Contains(c, `"action":"decline"`) {
		t.Fatalf("answer = %q", c)
	}
}

// An agent that takes a system prompt gets NusaShell's guidance appended,
// including the builtin-skill files it must read with its own tools.
func TestSessionSystemPrompt(t *testing.T) {
	rt, in := setup(t, "hello")
	skillsDir := filepath.Join(t.TempDir(), "builtin-skills")
	rt.opts.BuiltinSkillsDir = skillsDir
	trace := filepath.Join(t.TempDir(), "trace")
	if err := in.Agent.SetEnv(map[string]string{"FAKE_ACP_SYSTEM_PROMPT": "1", "FAKE_ACP_TRACE_SESSION_PROMPT": "1", "FAKE_ACP_TRACE": trace}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(raw), `\\`, `\`)
	if !strings.Contains(string(raw), `"systemPrompt":{"mode":"append"`) || !strings.Contains(got, "NusaShell") {
		t.Fatalf("session/new = %s", raw)
	}
	for _, name := range []string{"nusashell", "coding-workflow"} {
		if path := filepath.Join(skillsDir, name, "SKILL.md"); !strings.Contains(got, path) {
			t.Fatalf("session instructions do not name %s: %s", path, raw)
		}
	}
	if strings.Contains(got, "read_skill") {
		t.Fatalf("session instructions name an API-only skill tool: %s", raw)
	}
}

// An agent that needs a sign-in reports it in Status, signs in with one of
// its methods, keeps its stderr across restarts, and signs out.
func TestAuthenticateAndStatus(t *testing.T) {
	rt, in := setup(t, "hello")
	if err := in.Agent.SetEnv(map[string]string{"FAKE_ACP_AUTH": "1"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if st := rt.Status("fake"); st.State != dservice.AgentStopped {
		t.Fatalf("status before any launch = %+v", st)
	}
	err := rt.Prompt(ctx, in, nil, nil)
	if apperror.As(err).StatusCode() != 401 {
		t.Fatalf("a turn before sign-in = %v", err)
	}
	st := rt.Status("fake")
	if st.State != dservice.AgentRunning || !st.AuthNeeded || len(st.AuthMethods) != 2 || !st.CanLogout {
		t.Fatalf("status = %+v", st)
	}
	if m := st.AuthMethods[1]; m.Type != "terminal" || len(m.Command) != 2 || m.Command[1] != "--login" {
		t.Fatalf("terminal method = %+v", m)
	}
	if err := rt.Authenticate(ctx, in.Agent, "fake-terminal"); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("a terminal method = %v", err)
	}
	if err := rt.Authenticate(ctx, in.Agent, "fake-login"); err != nil {
		t.Fatal(err)
	}
	if st := rt.Status("fake"); st.AuthNeeded || st.LastError != "" {
		t.Fatalf("status after sign-in = %+v", st)
	}
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := rt.Logout(ctx, in.Agent); err != nil {
		t.Fatal(err)
	}
	st = rt.Status("fake")
	if st.State != dservice.AgentStopped {
		t.Fatalf("state after sign-out = %s", st.State)
	}
	if logs := strings.Join(st.Logs, "\n"); !strings.Contains(logs, "signed in") || !strings.Contains(logs, "started") {
		t.Fatalf("logs = %q", logs)
	}
}

// Input steered into a running turn reaches an agent that takes
// _session/steering, lands in the reply where it was sent, and is
// announced before the agent's answer to it.
func TestSteerIntoRunningTurn(t *testing.T) {
	rt, in := setup(t, "steer-hold")
	trace := traceTo(t, in.Agent, map[string]string{"FAKE_ACP_STEERING": "1"})
	rt.opts.Workspace = repoworkspace.NewLocal()
	rec := &recorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	convID := in.Conversation.ID()
	png, err := os.ReadFile("../../../test/media/image.png")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(in.Workspace, "steer-note.png")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- rt.Prompt(ctx, in, rec.handle, nil) }()
	ref, err := valueobject.NewReference("conversation", "c_dog", "@dog", 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	att, err := valueobject.NewAttachment("file://"+path, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	text := "ask @dog"
	input := valueobject.UserInput{
		Text:        text,
		References:  []valueobject.Reference{ref},
		Attachments: []valueobject.Attachment{att},
	}
	delivered := 0
	deadline := time.Now().Add(3 * time.Second)
	for !rt.Steer(ctx, convID, input, func() { delivered++ }) {
		if time.Now().After(deadline) {
			t.Fatal("the running turn never took the steer")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTrace(t, trace), "steering_image image/png") {
		t.Fatal("native ACP steer did not receive inline image data")
	}
	if delivered != 1 {
		t.Fatalf("delivered ran %d times", delivered)
	}
	last := in.Conversation.Last()
	var kinds []string
	var steerPart entity.MessagePart
	for _, p := range last.Parts() {
		kinds = append(kinds, p.Kind)
		if p.Kind == entity.MessagePartSteer {
			steerPart = p
		}
	}
	if strings.Join(kinds, ",") != "text,steer,text" {
		t.Fatalf("parts = %v", kinds)
	}
	if len(steerPart.SteerAttachments) != 1 || steerPart.SteerAttachments[0].URI() != att.URI() {
		t.Fatalf("steer part attachments = %v", steerPart.SteerAttachments)
	}
	if !strings.Contains(steerPart.SteerContext, "@dog refers to the NusaShell conversation c_dog") {
		t.Fatalf("steer part context = %q", steerPart.SteerContext)
	}
	if replay := last.AsModelReplay().Content(); !strings.Contains(replay, "@dog refers to the NusaShell conversation c_dog") {
		t.Fatalf("model replay misses the reference context: %q", replay)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	steerAt, answerAt := -1, -1
	for i, ev := range rec.events {
		if ev.Kind() == enum.TurnEventSteer && ev.Delta() == text {
			if len(ev.SteerAttachments()) != 1 {
				t.Fatalf("steer event attachments = %v", ev.SteerAttachments())
			}
			steerAt = i
		}
		if ev.Kind() == enum.TurnEventDelta && strings.Contains(ev.Delta(), "saw-steer") && answerAt < 0 {
			if !strings.Contains(ev.Delta(), "@dog refers to the NusaShell conversation c_dog") {
				t.Fatalf("the reference note did not reach the agent: %q", ev.Delta())
			}
			answerAt = i
		}
	}
	if steerAt < 0 || answerAt < steerAt {
		t.Fatalf("steer at %d, answer at %d", steerAt, answerAt)
	}
}

// An agent without mid-turn input leaves the steer with the caller.
func TestSteerUnsupported(t *testing.T) {
	rt, in := setup(t, "steer-hold")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	convID := in.Conversation.ID()
	go func() { _ = rt.Prompt(ctx, in, nil, nil) }()
	time.Sleep(300 * time.Millisecond)
	if rt.Steer(ctx, convID, valueobject.UserInput{Text: "go left"}, func() { t.Error("delivered without support") }) {
		t.Fatal("steer taken by an agent without support")
	}
	cancel()
}
