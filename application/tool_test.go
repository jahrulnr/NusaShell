package application

import (
	"context"
	"encoding/json"
	"fmt"
	"nusashell/application/service/tooloutput"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/domain/turndiff"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- from tool_stop_test.go ---

// cancellableStreamToolbox models exec: it does not return until the context
// passed to the individual tool call is cancelled.
type cancellableStreamToolbox struct {
	started chan struct{}
	once    sync.Once
}

func (b *cancellableStreamToolbox) ListTools() []ToolInfo { return nil }

func (b *cancellableStreamToolbox) Execute(context.Context, string, []byte) (string, error) {
	return "", fmt.Errorf("unexpected non-streamed execution")
}

func (b *cancellableStreamToolbox) ExecuteStreamed(ctx context.Context, _ string, _ []byte, _ func(string)) (string, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return "", fmt.Errorf("exec cancelled: %w\npartial output:\nline-1", ctx.Err())
}

func TestHandleToolStopInterruptsOnlyTheSelectedTool(t *testing.T) {
	box := &cancellableStreamToolbox{started: make(chan struct{})}
	run := &TurnRun{ID: "run-1", ConversationID: "conv-1", Ctx: context.Background()}
	app := &App{
		Bus:     NewBus(),
		Toolbox: box,
		runs:    map[string]*TurnRun{run.ID: run},
	}

	done := make(chan toolExecResult, 1)
	go func() {
		done <- app.runOneTool(run, "msg-1", domain.ToolCall{ID: "tool-1", Name: "exec", Args: `{}`}, ModelCapabilities{}, domain.Settings{}, 1)
	}()

	select {
	case <-box.started:
	case <-time.After(time.Second):
		t.Fatal("exec did not start")
	}

	if _, rpcErr := app.handleToolStop(contracts.ToolStopRequest{RunID: run.ID, ToolCallID: "tool-1"}); rpcErr != nil {
		t.Fatalf("handleToolStop: %v", rpcErr)
	}

	select {
	case result := <-done:
		if result.Status != domain.ToolInterrupted {
			t.Fatalf("tool status = %q, want %q", result.Status, domain.ToolInterrupted)
		}
		if !strings.Contains(result.Output, "interrupted by user") {
			t.Fatalf("tool output = %q, want an explicit user interruption marker", result.Output)
		}
	case <-time.After(time.Second):
		t.Fatal("tool did not return after per-tool cancellation")
	}
	if err := run.Ctx.Err(); err != nil {
		t.Fatalf("per-tool stop cancelled the enclosing turn: %v", err)
	}
}

func TestExecuteTurnToolsContinuesAfterPerToolStop(t *testing.T) {
	box := &cancellableStreamToolbox{started: make(chan struct{})}
	conv := &domain.Conversation{
		ID:       "conv-1",
		Messages: []domain.Message{{ID: "msg-1", ToolCalls: []domain.ToolCall{{ID: "tool-1", Name: "exec", Args: `{}`}}}},
	}
	run := &TurnRun{ID: "run-1", ConversationID: conv.ID, Ctx: context.Background()}
	app := &App{
		Bus:           NewBus(),
		Toolbox:       box,
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{conv.ID: conv}},
		runs:          map[string]*TurnRun{run.ID: run},
	}

	done := make(chan error, 1)
	go func() {
		done <- app.executeTurnTools(run, "msg-1", conv.Messages[0].ToolCalls, ModelCapabilities{}, domain.Settings{}, 1)
	}()
	select {
	case <-box.started:
	case <-time.After(time.Second):
		t.Fatal("exec did not start")
	}
	if _, rpcErr := app.handleToolStop(contracts.ToolStopRequest{RunID: run.ID, ToolCallID: "tool-1"}); rpcErr != nil {
		t.Fatalf("handleToolStop: %v", rpcErr)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeTurnTools returned an error after per-tool stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tool round did not continue after per-tool stop")
	}
	if got := conv.Messages[0].ToolCalls[0].Status; got != domain.ToolInterrupted {
		t.Fatalf("persisted tool status = %q, want %q", got, domain.ToolInterrupted)
	}
	if got := conv.Messages[0].ToolCalls[0].Output; !strings.Contains(got, "interrupted by user") {
		t.Fatalf("persisted tool output = %q, want an explicit user interruption marker", got)
	}
	if err := run.Ctx.Err(); err != nil {
		t.Fatalf("per-tool stop cancelled the enclosing turn: %v", err)
	}
}

func TestHandleToolStopRejectsUnknownToolCall(t *testing.T) {
	run := &TurnRun{ID: "run-1", Ctx: context.Background()}
	app := &App{runs: map[string]*TurnRun{run.ID: run}}
	if _, rpcErr := app.handleToolStop(contracts.ToolStopRequest{RunID: run.ID, ToolCallID: "missing"}); rpcErr == nil || rpcErr.Code != contracts.CodeNotFound {
		t.Fatalf("unknown tool stop error = %+v, want NOT_FOUND", rpcErr)
	}
}

// --- from toolfactory_test.go ---

// factoryStubToolbox advertises a fixed tool set standing in for the real
// toolbox (which the factory only reads via ListTools).
type factoryStubToolbox struct{ tools []ToolInfo }

func (s *factoryStubToolbox) ListTools() []ToolInfo { return s.tools }

func (s *factoryStubToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	return "", nil
}

type countingToolbox struct {
	calls []string
}

func (s *countingToolbox) ListTools() []ToolInfo { return nil }

func (s *countingToolbox) Execute(_ context.Context, name string, _ []byte) (string, error) {
	s.calls = append(s.calls, name)
	return "unexpected execution", nil
}

func TestLearningToolCallsRejectBannedTools(t *testing.T) {
	for _, kind := range []AgentKind{
		AgentLearner,
		AgentMemoryConsolidator,
		AgentSkillEvolver,
		AgentSkillEvaluator,
	} {
		t.Run(string(kind), func(t *testing.T) {
			box := &countingToolbox{}
			app := &App{Bus: NewBus(), Toolbox: box}
			run := &TurnRun{ID: "learning-run", ToolKind: kind, Ctx: context.Background()}
			allowed := []struct {
				name string
				args string
			}{
				{"file_write", `{}`},
				{"skill", `{"op":"list"}`},
				{"skill", `{"op":"search","query":"learned"}`},
				{"automation", `{"op":"list"}`},
				{"conversation", `{"op":"list"}`},
			}
			for _, call := range allowed {
				res := app.runOneTool(run, "", domain.ToolCall{
					ID:   "call-" + call.name,
					Name: call.name,
					Args: call.args,
				}, ModelCapabilities{}, domain.Settings{}, 1)
				if res.Status == domain.ToolFailed {
					t.Fatalf("learning tool %q was rejected: %s", call.name, res.Output)
				}
			}
			got := append([]string(nil), box.calls...)
			want := make([]string, 0, len(allowed))
			for _, call := range allowed {
				want = append(want, call.name)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("executed learning tools = %v, want %v", got, want)
			}
			for _, call := range []domain.ToolCall{
				{Name: "skill", Args: `{"op":"save"}`},
				{Name: "skill", Args: `{"op":"delete"}`},
			} {
				res := app.runOneTool(run, "", call, ModelCapabilities{}, domain.Settings{}, 1)
				if res.Status != domain.ToolFailed {
					t.Fatalf("learner skill mutation %s must fail, got %+v", call.Args, res)
				}
			}

			banned := []string{
				"memory_project", "subagent", "subagent_steer", "delegate", "mcp_call", "tool_list",
			}
			for _, name := range banned {
				before := len(box.calls)
				res := app.runOneTool(run, "", domain.ToolCall{
					ID:   "call-ban-" + name,
					Name: name,
					Args: `{}`,
				}, ModelCapabilities{}, domain.Settings{}, 1)
				if res.Status != domain.ToolFailed {
					t.Fatalf("banned learning tool %q status=%s output=%s", name, res.Status, res.Output)
				}
				if len(box.calls) != before {
					t.Fatalf("banned tool %q must not hit the toolbox", name)
				}
			}

			learn := app.runOneTool(run, "", domain.ToolCall{
				ID:   "call-learn",
				Name: learnerResultToolName,
				Args: `{"stage_reached":"consolidate","consolidate":{"action":"no_op","reason_for_no_op":"nothing durable"}}`,
			}, ModelCapabilities{}, domain.Settings{}, 1)
			if learn.Status == domain.ToolFailed {
				t.Fatalf("learn() was rejected: %s", learn.Output)
			}
			if !reflect.DeepEqual(box.calls, want) {
				t.Fatalf("learn() must not hit the conversation toolbox, calls=%v", box.calls)
			}
		})
	}
}

func TestLearnerResultToolRejectedForConversationAgent(t *testing.T) {
	app := &App{Bus: NewBus(), Toolbox: &countingToolbox{}}
	run := &TurnRun{ID: "conv-run", ToolKind: AgentConversation, Ctx: context.Background()}
	res := app.runOneTool(run, "", domain.ToolCall{
		ID:   "call-learn",
		Name: learnerResultToolName,
		Args: `{"stage_reached":"consolidate","consolidate":{"action":"no_op","reason_for_no_op":"x"}}`,
	}, ModelCapabilities{}, domain.Settings{}, 1)
	if res.Status != domain.ToolFailed {
		t.Fatalf("conversation agent must not execute learn(), status=%s output=%s", res.Status, res.Output)
	}
}

// --- from tool_contracts_test.go ---

func TestToolContractsFollowExecutionRoster(t *testing.T) {
	box := &factoryStubToolbox{tools: []ToolInfo{
		{Name: "file_read", Description: "Read a file", InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		}},
		{Name: "read_media", InputSchema: map[string]any{"type": "object"}},
		{Name: "delegate", InputSchema: map[string]any{"type": "object"}},
	}}
	a := &App{Toolbox: box}

	result, rpcErr := a.handleToolContracts(contracts.ToolContractsRequest{Workspace: ""})
	if rpcErr != nil {
		t.Fatalf("handleToolContracts: %v", rpcErr)
	}
	got, ok := result.(contracts.ToolContractsResult)
	if !ok {
		t.Fatalf("result type = %T, want contracts.ToolContractsResult", result)
	}
	if got.Version != contracts.ToolContractVersion {
		t.Fatalf("catalog version = %d, want %d", got.Version, contracts.ToolContractVersion)
	}
	fileRead, ok := findToolContract(got.Tools, "file_read")
	if !ok {
		t.Fatalf("file_read missing from catalog: %+v", got.Tools)
	}
	if fileRead.ID != "tool.file_read.v1" || fileRead.CSSClass != "agent-tool-file-read" {
		t.Fatalf("file_read identity = %+v", fileRead)
	}
	if len(fileRead.Presentation.RequestFields) != 1 || fileRead.Presentation.RequestFields[0] != "path" {
		t.Fatalf("file_read request fields = %+v", fileRead.Presentation.RequestFields)
	}
	if _, ok := findToolContract(got.Tools, "memory_project"); ok {
		t.Fatal("memory_project must not be advertised without a workspace")
	}
	dispatcher, ok := findToolContract(got.Tools, "memory")
	if !ok {
		t.Fatal("memory dispatcher missing from catalog")
	}
	if len(dispatcher.Presentation.Variants) != 3 {
		t.Fatalf("memory variants = %+v", dispatcher.Presentation.Variants)
	}
	automation, ok := findToolContract(got.Tools, "automation")
	if !ok {
		t.Fatal("automation dispatcher missing from catalog")
	}
	if len(automation.Presentation.Variants) != 3 || len(automation.Presentation.Formats) != 3 {
		t.Fatalf("automation presentation = %+v", automation.Presentation)
	}
	schedule, ok := findToolContract(got.Tools, "automation_schedule")
	if !ok {
		t.Fatal("automation_schedule dispatcher missing from catalog")
	}
	if len(schedule.Presentation.Variants) != 1 || schedule.Presentation.Variants[0] != "status" {
		t.Fatalf("automation_schedule presentation = %+v", schedule.Presentation)
	}

	media := buildToolContract(ToolInfo{Name: "generate_image"})
	if media.InputSchema == nil {
		t.Fatal("tool contracts must expose an object schema even when a definition omits one")
	}
	if !containsContractString(media.Presentation.ResultFields, "attachments") {
		t.Fatalf("media result fields = %+v, want attachments", media.Presentation.ResultFields)
	}

	dispatched, rpcErr := a.Dispatch(context.Background(), contracts.MethodToolContracts, json.RawMessage(`{"workspace":""}`))
	if rpcErr != nil {
		t.Fatalf("Dispatch(%s): %v", contracts.MethodToolContracts, rpcErr)
	}
	if _, ok := dispatched.(contracts.ToolContractsResult); !ok {
		t.Fatalf("dispatched result type = %T, want contracts.ToolContractsResult", dispatched)
	}
}

func containsContractString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func findToolContract(tools []contracts.ToolContractDTO, name string) (contracts.ToolContractDTO, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return contracts.ToolContractDTO{}, false
}

// --- from tool_output_test.go ---

// TestFilterToolAttachmentsByCapsNotesInsideUntrustedEnvelope verifies that
// capability-filter notes (e.g. "[Audio ... was loaded but cannot be played]")
// are appended to the tool content BEFORE wrapping in the untrusted envelope,
// not after. If notes land outside </untrusted_tool_result>, a malicious tool
// could craft an attachment file path containing injection instructions that
// the model would treat as trusted/system-level content.
func TestFilterToolAttachmentsByCapsNotesInsideUntrustedEnvelope(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "audio", Name: "speech.wav", FilePath: "/tmp/speech.wav"},
	}
	caps := ModelCapabilities{Audio: false} // audio not supported → note appended
	content := "Speech generated and saved."
	filtered, result := filterToolAttachmentsByCaps(atts, content, caps)
	if len(filtered) != 0 {
		t.Fatalf("audio attachment should be stripped, got %d attachments", len(filtered))
	}
	wrapped := tooloutput.WrapToolOutput("generate_media", result)
	// The note must be INSIDE the envelope, not after </untrusted_tool_result>.
	closeIdx := strings.Index(wrapped, "</untrusted_tool_result>")
	noteIdx := strings.Index(wrapped, "[Audio")
	if closeIdx < 0 {
		t.Fatalf("missing closing tag in: %s", wrapped)
	}
	if noteIdx < 0 {
		t.Fatalf("missing audio note in: %s", wrapped)
	}
	if noteIdx > closeIdx {
		t.Fatalf("audio note is OUTSIDE the untrusted envelope (note at %d, close at %d):\n%s", noteIdx, closeIdx, wrapped)
	}
}

// --- from parallel_tools_test.go ---

// barrierToolbox blocks each Execute until `want` calls are in flight at once,
// then releases them all. If tools ran serially, the barrier would never be
// reached and executeTurnTools would hang (the test guards this with a timeout).
type barrierToolbox struct {
	want      int
	mu        sync.Mutex
	active    int
	maxActive int
	gate      chan struct{}
}

func (b *barrierToolbox) ListTools() []ToolInfo { return nil }

func (b *barrierToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	b.mu.Lock()
	b.active++
	if b.active > b.maxActive {
		b.maxActive = b.active
	}
	reached := b.active >= b.want
	b.mu.Unlock()
	if reached {
		select {
		case <-b.gate:
		default:
			close(b.gate)
		}
	}
	<-b.gate // wait until enough calls are concurrent
	b.mu.Lock()
	b.active--
	b.mu.Unlock()
	return "ok:" + name, nil
}

func newBarrierApp(t *testing.T, toolCalls []domain.ToolCall, box ToolExecutor) (*App, *domain.Conversation, *TurnRun) {
	t.Helper()
	conv := &domain.Conversation{
		ID:       "c1",
		Messages: []domain.Message{{ID: "m1", ToolCalls: toolCalls}},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       box,
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background(), Cancel: func() {}}
	return app, conv, run
}

// The tool calls of one round must run concurrently (bounded), not one-by-one.
func TestExecuteTurnToolsRunsConcurrently(t *testing.T) {
	want := 3
	box := &barrierToolbox{want: want, gate: make(chan struct{})}
	toolCalls := []domain.ToolCall{{ID: "t1", Name: "a"}, {ID: "t2", Name: "b"}, {ID: "t3", Name: "c"}}
	app, conv, run := newBarrierApp(t, toolCalls, box)

	done := make(chan error, 1)
	go func() {
		err := app.executeTurnTools(run, "m1", toolCalls, ModelCapabilities{Vision: true}, domain.Settings{}, 1)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeTurnTools: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executeTurnTools did not complete — tools appear to run serially, not in parallel")
	}

	if box.maxActive < want {
		t.Fatalf("max concurrent tools = %d, want >= %d (parallel execution)", box.maxActive, want)
	}
	for i, tc := range conv.Messages[0].ToolCalls {
		if tc.Status != domain.ToolOK {
			t.Fatalf("tool %d status = %v, want ok", i, tc.Status)
		}
	}
}

// TestExecuteTurnToolsRespectsMaxParallelTools: when settings.MaxParallelTools
// is set below the number of tool calls, the semaphore bounds concurrency to
// that value (maxActive never exceeds it). This proves the cap is configurable
// and not a hardcoded constant.
func TestExecuteTurnToolsRespectsMaxParallelTools(t *testing.T) {
	cap := 2
	box := &barrierToolbox{want: cap, gate: make(chan struct{})}
	toolCalls := []domain.ToolCall{
		{ID: "t1", Name: "a"}, {ID: "t2", Name: "b"},
		{ID: "t3", Name: "c"}, {ID: "t4", Name: "d"},
	}
	app, conv, run := newBarrierApp(t, toolCalls, box)
	settings := domain.Settings{MaxParallelTools: cap}

	done := make(chan error, 1)
	go func() {
		err := app.executeTurnTools(run, "m1", toolCalls, ModelCapabilities{Vision: true}, settings, 1)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeTurnTools: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executeTurnTools did not complete")
	}

	if box.maxActive > cap {
		t.Fatalf("max concurrent tools = %d, want <= %d (cap not respected)", box.maxActive, cap)
	}
	if box.maxActive < cap {
		t.Fatalf("max concurrent tools = %d, want exactly %d (cap should allow this many)", box.maxActive, cap)
	}
	for i, toolCall := range conv.Messages[0].ToolCalls {
		if toolCall.Status != domain.ToolOK {
			t.Fatalf("tool %d (%s) status = %q, want ok; calls above the cap must be queued, not dropped", i, toolCall.Name, toolCall.Status)
		}
	}
}

// orderedToolbox returns the tool name as output so we can assert results are
// persisted in tool-call order regardless of completion order.
type orderedToolbox struct{}

func (orderedToolbox) ListTools() []ToolInfo { return nil }
func (orderedToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	// Make later tools finish first to shuffle completion order.
	switch name {
	case "first":
		time.Sleep(30 * time.Millisecond)
	case "second":
		time.Sleep(15 * time.Millisecond)
	}
	return "out:" + name, nil
}

// Even when tools finish out of order, results are persisted in tool-call order.
func TestExecuteTurnToolsPersistsResultsInOrder(t *testing.T) {
	toolCalls := []domain.ToolCall{
		{ID: "t1", Name: "first"},
		{ID: "t2", Name: "second"},
		{ID: "t3", Name: "third"},
	}
	app, conv, run := newBarrierApp(t, toolCalls, orderedToolbox{})

	if err := app.executeTurnTools(run, "m1", toolCalls, ModelCapabilities{Vision: true}, domain.Settings{}, 1); err != nil {
		t.Fatalf("executeTurnTools: %v", err)
	}
	got := conv.Messages[0].ToolCalls
	for i, name := range []string{"first", "second", "third"} {
		want := fmt.Sprintf("out:%s", name)
		if got[i].ID != toolCalls[i].ID || got[i].Output != want {
			t.Fatalf("tool %d = {id:%s out:%q}, want {id:%s out:%q}", i, got[i].ID, got[i].Output, toolCalls[i].ID, want)
		}
	}
}

// --- from context_usage_test.go ---

// ContextTokens is the authoritative per-round context fill. Provider
// total_tokens wins when present because cache detail overlap varies by API.
// The normalized component sum remains the fallback for providers that omit
// total_tokens.
func TestChatUsageContextTokens(t *testing.T) {
	cases := []struct {
		name string
		u    ChatUsage
		want int
	}{
		{"provider total wins over overlapping details", ChatUsage{InputTokens: 80, OutputTokens: 50, CacheRead: 920, CacheWrite: 100, TotalTokens: 1050}, 1050},
		{"provider total wins when normalized components are inconsistent", ChatUsage{InputTokens: 1, OutputTokens: 4, CacheRead: 2, CacheWrite: 5, TotalTokens: 7}, 7},
		{"openai style no cache", ChatUsage{InputTokens: 1200, OutputTokens: 300}, 1500},
		{"openai style with cache (post-normalization)", ChatUsage{InputTokens: 80, CacheRead: 920, OutputTokens: 50}, 1050},
		{"anthropic with cache", ChatUsage{InputTokens: 200, CacheRead: 800, CacheWrite: 100, OutputTokens: 150}, 1250},
		{"empty", ChatUsage{}, 0},
	}
	for _, tc := range cases {
		if got := tc.u.ContextTokens(); got != tc.want {
			t.Errorf("%s: ContextTokens()=%d, want %d", tc.name, got, tc.want)
		}
	}
}

// The last round's ContextTokens must be used for the badge, not the sum of
// per-round usage: summing InputTokens across tool rounds double counts the
// prompt (each round re-sends the growing history) and can exceed the window.
func TestContextTokensUsesLastRoundNotSum(t *testing.T) {
	round1 := ChatUsage{InputTokens: 1000, OutputTokens: 50}  // prompt + first answer
	round2 := ChatUsage{InputTokens: 1200, OutputTokens: 120} // prompt grew (tool result), final answer

	summed := mergeUsage(round1, round2)
	if summed.ContextTokens() != 2370 {
		t.Fatalf("sanity: merged sum = %d", summed.ContextTokens())
	}
	// Authoritative context fill is the last round only.
	if got := round2.ContextTokens(); got != 1320 {
		t.Fatalf("last-round context tokens = %d, want 1320", got)
	}
	if round2.ContextTokens() >= summed.ContextTokens() {
		t.Fatal("last-round context must be smaller than the summed usage (no double counting)")
	}
}

// --- from turndiff_round_test.go ---

type turnDiffToolbox struct{}

func (turnDiffToolbox) ListTools() []ToolInfo { return nil }

func (turnDiffToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	var args map[string]any
	_ = json.Unmarshal(argsJSON, &args)
	switch name {
	case "file_write":
		path, _ := args["path"].(string)
		content, _ := args["content"].(string)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
		turndiff.Record(ctx, turndiff.AddFile(path, content, nil))
		return "ok", nil
	case "file_patch":
		path, _ := args["path"].(string)
		oldS, _ := args["old_string"].(string)
		newS, _ := args["new_string"].(string)
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		out := strings.Replace(string(raw), oldS, newS, 1)
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			return "", err
		}
		turndiff.Record(ctx, turndiff.UpdateFile(path, string(raw), out, nil, nil))
		return "ok", nil
	case "exec":
		return "hello-exec\n", nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func waitTurnDiff(t *testing.T, events <-chan contracts.Event) contracts.TurnDiffEvent {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev := <-events:
			if ev.Type != contracts.EventTurnDiff {
				continue
			}
			var payload contracts.TurnDiffEvent
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("payload: %v", err)
			}
			return payload
		case <-deadline.C:
			t.Fatal("timed out waiting for agent.turn.diff")
		}
	}
}

func TestRunOneToolFileWriteEmitsTurnDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foo.txt")
	bus := NewBus()
	_, events, unsub := bus.Subscribe()
	defer unsub()
	app := &App{Bus: bus, Toolbox: turnDiffToolbox{}}
	run := &TurnRun{
		ID:             "run1",
		ConversationID: "conv1",
		Workspace:      dir,
		Ctx:            context.Background(),
		TurnDiff:       turndiff.New(turndiff.WithDisplayRoot(dir)),
	}
	res := app.runOneTool(run, "m1", domain.ToolCall{
		ID:   "tc1",
		Name: "file_write",
		Args: `{"path":"` + jsonEscape(path) + `","content":"hello\n"}`,
	}, ModelCapabilities{}, domain.Settings{}, 1)
	if res.Status != domain.ToolOK {
		t.Fatalf("status = %v output=%q", res.Status, res.Output)
	}
	payload := waitTurnDiff(t, events)
	if payload.RunID != "run1" || payload.ConversationID != "conv1" {
		t.Fatalf("ids = %+v", payload)
	}
	if !strings.Contains(payload.UnifiedDiff, "diff --git a/foo.txt b/foo.txt") {
		t.Fatalf("unified_diff = %q", payload.UnifiedDiff)
	}
	if !strings.Contains(payload.UnifiedDiff, "+hello") {
		t.Fatalf("unified_diff missing content: %q", payload.UnifiedDiff)
	}
}

func TestRunOneToolFilePatchEmitsTurnDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	bus := NewBus()
	app := &App{Bus: bus, Toolbox: turnDiffToolbox{}}
	run := &TurnRun{
		ID:             "run1",
		ConversationID: "conv1",
		Workspace:      dir,
		Ctx:            context.Background(),
		TurnDiff:       turndiff.New(turndiff.WithDisplayRoot(dir)),
	}
	write := app.runOneTool(run, "m1", domain.ToolCall{
		ID: "tc1", Name: "file_write", Args: `{"path":"` + jsonEscape(path) + `","content":"line1\n"}`,
	}, ModelCapabilities{}, domain.Settings{}, 1)
	if write.Status != domain.ToolOK {
		t.Fatalf("write status = %v output=%q", write.Status, write.Output)
	}
	_, events, unsub := bus.Subscribe()
	defer unsub()
	patch := app.runOneTool(run, "m1", domain.ToolCall{
		ID: "tc2", Name: "file_patch", Args: `{"path":"` + jsonEscape(path) + `","old_string":"line1\n","new_string":"line2\n"}`,
	}, ModelCapabilities{}, domain.Settings{}, 1)
	if patch.Status != domain.ToolOK {
		t.Fatalf("patch status = %v output=%q", patch.Status, patch.Output)
	}
	payload := waitTurnDiff(t, events)
	if !strings.Contains(payload.UnifiedDiff, "+line2") {
		t.Fatalf("unified_diff = %q", payload.UnifiedDiff)
	}
}

func TestRunOneToolExecDoesNotCreateJournal(t *testing.T) {
	dataDir := t.TempDir()
	ws := filepath.Join(dataDir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	app := &App{DataDir: dataDir, Bus: NewBus(), Toolbox: turnDiffToolbox{}}
	run := &TurnRun{
		ID:             "run1",
		ConversationID: "conv1",
		Workspace:      ws,
		Ctx:            context.Background(),
		TurnDiff:       turndiff.New(turndiff.WithDisplayRoot(ws)),
	}
	res := app.runOneTool(run, "m1", domain.ToolCall{
		ID:   "tc1",
		Name: "exec",
		Args: `{"command":"echo hello-exec"}`,
	}, ModelCapabilities{}, domain.Settings{}, 1)
	if res.Status != domain.ToolOK {
		t.Fatalf("exec status = %v output=%q", res.Status, res.Output)
	}
	matches, err := filepath.Glob(filepath.Join(dataDir, "conversations", "*.journal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("exec created journal sidecars: %v", matches)
	}
	var ok bool
	run.WithTurnDiff(func() {
		_, ok = run.TurnDiff.UnifiedDiff()
	})
	if ok {
		t.Fatal("exec must not produce a turn diff")
	}
}

func jsonEscape(p string) string {
	b, _ := json.Marshal(p)
	s := string(b)
	return s[1 : len(s)-1]
}
