package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// validTestSummary is a summary string long enough to pass the compaction
// quality guard (>= compactionSummaryMinChars). Used by tests that need
// compactConversation to succeed without triggering retries.
var validTestSummary = strings.Repeat("handoff checkpoint with enough detail to pass the guard. ", 6)

func TestUpdateToolResultUpdatesChronologicalToolCallStep(t *testing.T) {
	app := &App{}
	conversation := &domain.Conversation{Messages: []domain.Message{{
		ID:        "message-1",
		ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "web_fetch", Status: domain.ToolRunning}},
		Steps: []domain.MessageStep{{
			Type:      domain.StepToolCalls,
			ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "web_fetch", Status: domain.ToolRunning}},
		}},
	}}}

	got := app.updateToolResult(conversation, "message-1", "call-1", domain.ToolOK, "first\nsecond", nil)

	if got.Messages[0].ToolCalls[0].Status != domain.ToolOK || got.Messages[0].ToolCalls[0].Output != "first\nsecond" {
		t.Fatalf("flat tool call = %+v, want completed result", got.Messages[0].ToolCalls[0])
	}
	stepCall := got.Messages[0].Steps[0].ToolCalls[0]
	if stepCall.Status != domain.ToolOK || stepCall.Output != "first\nsecond" {
		t.Fatalf("chronological tool call = %+v, want completed result", stepCall)
	}
}

func TestShouldContinueFailedTurn(t *testing.T) {
	if !shouldContinueFailedTurn(domain.Message{Content: "partial"}) {
		t.Fatal("partial text without tools should continue")
	}
	if shouldContinueFailedTurn(domain.Message{Content: "partial", ToolCalls: []domain.ToolCall{{ID: "t1"}}}) {
		t.Fatal("partial text with tool calls must restart from scratch")
	}
	if shouldContinueFailedTurn(domain.Message{}) {
		t.Fatal("empty failed turn must restart from scratch")
	}
}

func TestHydrationCheckpointPersistsOnceUntilCompaction(t *testing.T) {
	conversation := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "first"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conversation}},
		Toolbox:       &recordingToolbox{},
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	adapter := &reviewStubAdapter{}
	callRound := func(messageID string) {
		t.Helper()
		if _, err := app.streamTurnRoundOnce(run, adapter, conversation, messageID, "model", "", nil, domain.Settings{}, false, 0, true, nil, ModelCapabilities{}); err != nil {
			t.Fatal(err)
		}
	}
	countCheckpoints := func() int {
		count := 0
		for _, message := range conversation.Messages {
			if isHydrationMessage(message) {
				count++
			}
		}
		return count
	}

	callRound("a1")
	if got := countCheckpoints(); got != 1 {
		t.Fatalf("initial checkpoints = %d, want 1", got)
	}
	conversation.Messages = append(conversation.Messages,
		domain.Message{ID: "u2", Role: domain.RoleUser, Content: "follow up"},
		domain.Message{ID: "a2", Role: domain.RoleAssistant},
	)
	callRound("a2")
	if got := countCheckpoints(); got != 1 {
		t.Fatalf("checkpoints after later user message = %d, want 1", got)
	}

	conversation.Messages = filterHydrationDomainMessages(conversation.Messages)
	conversation.Messages = append(conversation.Messages,
		domain.Message{ID: "u3", Role: domain.RoleUser, Content: "after compaction"},
		domain.Message{ID: "a3", Role: domain.RoleAssistant},
	)
	callRound("a3")
	if got := countCheckpoints(); got != 1 {
		t.Fatalf("post-compaction checkpoints = %d, want 1 fresh checkpoint", got)
	}
}

func TestChatMessagesKeepsReasoningOnlyAssistantTurns(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "hi"},
		{ID: "a1", Role: domain.RoleAssistant, Reasoning: "thinking", Status: domain.StatusDone},
		{ID: "pending", Role: domain.RoleAssistant},
	}}
	got := chatMessages(c, "pending", ModelCapabilities{Vision: true})
	if len(got) != 2 {
		t.Fatalf("got %d messages, want user + reasoning-only assistant", len(got))
	}
	if got[1].Reasoning != "thinking" {
		t.Fatalf("assistant reasoning = %q", got[1].Reasoning)
	}
}

func TestCompactionTriggerUsesThresholdCappedByWindow(t *testing.T) {
	// Explicit threshold: capped at 80% of available budget (window - maxOutput).
	settings := domain.Settings{CompactionThreshold: 40000}
	if got := compactionTriggerTokens(1000, 0, settings); got != 800 {
		t.Fatalf("small window: got %d, want 800 (80%% of 1000)", got)
	}
	if got := compactionTriggerTokens(200000, 0, settings); got != 40000 {
		t.Fatalf("large window: got %d, want threshold 40000", got)
	}
	settings.CompactionThreshold = 5000
	if got := compactionTriggerTokens(200000, 0, settings); got != 5000 {
		t.Fatalf("custom threshold: got %d, want 5000", got)
	}
}

func TestCompactionTriggerAutoUsesWindowPercentage(t *testing.T) {
	// Threshold=0 means "auto": compaction triggers at 80% of the model's
	// available input budget (contextWindow - maxOutput), not at a flat
	// token count. This is the default for new installations and after
	// migration from the old 40k default.
	settings := domain.Settings{CompactionThreshold: 0}
	if got := compactionTriggerTokens(1048576, 0, settings); got != 838860 {
		t.Fatalf("1M window auto: got %d, want 838860 (80%% of 1M)", got)
	}
	if got := compactionTriggerTokens(200000, 0, settings); got != 160000 {
		t.Fatalf("200k window auto: got %d, want 160000 (80%% of 200k)", got)
	}
	if got := compactionTriggerTokens(1000, 0, settings); got != 800 {
		t.Fatalf("1k window auto: got %d, want 800", got)
	}
}

func TestCompactionTodoContext(t *testing.T) {
	port := &fakeTodoPort{
		items: map[string][]domain.TodoItem{
			"conv_1": {
				{ID: "1", Content: "Finish auth", Status: domain.TodoInProgress},
				{ID: "2", Content: "Write tests", Status: domain.TodoPending},
				{ID: "3", Content: "Done item", Status: domain.TodoCompleted},
			},
		},
		briefs: map[string]string{"conv_1": "Build a CLI tool that converts Markdown"},
	}
	ctx := (&App{Todos: port}).compactionTodoContext("conv_1")
	for _, want := range []string{"Build a CLI tool that converts Markdown", "[in_progress] Finish auth", "[pending] Write tests"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("compaction todo context missing %q: %s", want, ctx)
		}
	}
	if strings.Contains(ctx, "Done item") {
		t.Error("completed todos must not appear in the compaction context")
	}

	// No todo store configured.
	if got := (&App{}).compactionTodoContext("x"); got != "" {
		t.Errorf("no todo store: got %q, want empty", got)
	}
	// Goal present but no open items.
	doneOnly := &fakeTodoPort{
		items:  map[string][]domain.TodoItem{"c": {{ID: "1", Content: "done", Status: domain.TodoCompleted}}},
		briefs: map[string]string{"c": "goal text"},
	}
	if got := (&App{Todos: doneOnly}).compactionTodoContext("c"); got != "User goal: goal text" {
		t.Errorf("goal-only context: got %q, want %q", got, "User goal: goal text")
	}
}

func TestCompactionTriggerSubtractsMaxOutput(t *testing.T) {
	// 256k window, 64k output → available = 196608 → trigger = 80% × 196608 = 157286
	settings := domain.Settings{CompactionThreshold: 0}
	if got := compactionTriggerTokens(262144, 65536, settings); got != 157286 {
		t.Fatalf("with maxOutput: got %d, want 157286", got)
	}
}

func TestResolveCompactionAdapter_defaultUsesCurrentModel(t *testing.T) {
	// When CompactionModel is empty, the current adapter+model are used as-is.
	app := &App{
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"chat-prov": {ID: "chat-prov", Enabled: true, Kind: domain.ProviderChat},
		}},
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
			return &fakeVisionAdapter{description: "default"}, nil
		},
	}
	defaultAdapter := &fakeVisionAdapter{description: "default"}
	settings := domain.Settings{}
	gotAdapter, gotModel, gotWindow := app.resolveCompactionAdapter(context.Background(), defaultAdapter, "chat-prov:gpt-5", 200000, settings)
	if gotAdapter != defaultAdapter {
		t.Fatal("expected default adapter when CompactionModel empty")
	}
	if gotModel != "chat-prov:gpt-5" {
		t.Fatalf("expected default model, got %q", gotModel)
	}
	if gotWindow != 200000 {
		t.Fatalf("expected default window, got %d", gotWindow)
	}
}

func TestResolveCompactionAdapter_overrideUsesSeparateModel(t *testing.T) {
	// When CompactionModel is set, a separate adapter is built for that model.
	app := &App{
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"chat-prov": {ID: "chat-prov", Enabled: true, Kind: domain.ProviderChat},
			"cheap-prov": {ID: "cheap-prov", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{
				{ID: "haiku", Context: 128000},
			}},
		}},
		Credentials: &fakeVisionCredStore{creds: map[string]string{"cheap-prov": "key"}},
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
			return &fakeVisionAdapter{description: "compaction-adapter"}, nil
		},
	}
	defaultAdapter := &fakeVisionAdapter{description: "default"}
	settings := domain.Settings{CompactionModel: "cheap-prov:haiku"}
	gotAdapter, gotModel, gotWindow := app.resolveCompactionAdapter(context.Background(), defaultAdapter, "chat-prov:gpt-5", 200000, settings)
	if gotAdapter == defaultAdapter {
		t.Fatal("expected a different adapter for compaction model override")
	}
	if gotModel != "haiku" {
		t.Fatalf("expected override model 'haiku', got %q", gotModel)
	}
	if gotWindow != 128000 {
		t.Fatalf("expected override model window 128000, got %d", gotWindow)
	}
}

func TestResolveCompactionAdapter_overrideFallsBackOnResolveError(t *testing.T) {
	// If the override model cannot be resolved, fall back to the default
	// adapter+model so compaction still runs with the chat model.
	app := &App{
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"chat-prov": {ID: "chat-prov", Enabled: true, Kind: domain.ProviderChat},
		}},
		Credentials: &fakeVisionCredStore{creds: map[string]string{"chat-prov": "key"}},
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
			return &fakeVisionAdapter{description: "default"}, nil
		},
	}
	defaultAdapter := &fakeVisionAdapter{description: "default"}
	settings := domain.Settings{CompactionModel: "no-such-prov:no-such-model"}
	gotAdapter, gotModel, gotWindow := app.resolveCompactionAdapter(context.Background(), defaultAdapter, "chat-prov:gpt-5", 200000, settings)
	if gotAdapter != defaultAdapter {
		t.Fatal("expected fallback to default adapter on resolve error")
	}
	if gotModel != "chat-prov:gpt-5" {
		t.Fatalf("expected fallback model, got %q", gotModel)
	}
	if gotWindow != 200000 {
		t.Fatalf("expected fallback window, got %d", gotWindow)
	}
}

func TestResolveContextWindowModelWinsOverGlobalCap(t *testing.T) {
	provider := &domain.Provider{Models: []domain.Model{
		{ID: "long-model", Context: 1_000_000},
		{ID: "small-model", Context: 128_000},
	}}
	settings := domain.Settings{MaxInputTokens: 200_000}
	// Catalog model window wins over the global cap — the cap is only a
	// fallback for models not in the catalog (avoids "1M model, why 200k?").
	app := &App{}
	if got := app.resolveContextWindow(provider, "long-model", settings); got != 1_000_000 {
		t.Fatalf("long model context = %d, want model window 1000000", got)
	}
	if got := app.resolveContextWindow(provider, "small-model", settings); got != 128_000 {
		t.Fatalf("small model context = %d, want model window 128000", got)
	}
	if got := app.resolveContextWindow(provider, "unknown", settings); got != 200_000 {
		t.Fatalf("unknown model context = %d, want fallback 200000", got)
	}
	if got := effectiveContextWindow(1_000_000, 0); got != 1_000_000 {
		t.Fatalf("uncapped model context = %d, want 1000000", got)
	}
	if got := effectiveContextWindow(1_000_000, 200_000); got != 1_000_000 {
		t.Fatalf("catalog model should ignore global cap, got %d", got)
	}
}

// stubCodexContextCache is a test double for CodexContextWindowCache.
type stubCodexContextCache map[string]int

func (s stubCodexContextCache) ContextWindow(modelID string) (int, bool) {
	cw, ok := s[modelID]
	return cw, ok
}

func TestResolveContextWindowClampsCodexToRuntimeCache(t *testing.T) {
	// providers.json stores 1.05M for Luna (stale catalog fallback), but
	// Codex runtime cache says 272K. Compaction must use the smaller
	// runtime value, not the stale stored value.
	provider := &domain.Provider{
		Kind: domain.ProviderCodex,
		Models: []domain.Model{
			{ID: "gpt-5.6-luna", Context: 1_050_000},
		},
	}
	settings := domain.Settings{MaxInputTokens: 200_000}
	cache := stubCodexContextCache{"gpt-5.6-luna": 272_000}
	app := &App{CodexContextWindowCache: cache}
	if got := app.resolveContextWindow(provider, "gpt-5.6-luna", settings); got != 272_000 {
		t.Fatalf("codex context = %d, want 272000 (clamped to runtime cache)", got)
	}
}

func TestResolveContextWindowNoClampForNonCodex(t *testing.T) {
	provider := &domain.Provider{
		Kind: domain.ProviderChat,
		Models: []domain.Model{
			{ID: "gpt-5.6-luna", Context: 1_050_000},
		},
	}
	settings := domain.Settings{MaxInputTokens: 200_000}
	cache := stubCodexContextCache{"gpt-5.6-luna": 272_000}
	app := &App{CodexContextWindowCache: cache}
	if got := app.resolveContextWindow(provider, "gpt-5.6-luna", settings); got != 1_050_000 {
		t.Fatalf("non-codex context = %d, want 1050000 (no clamp)", got)
	}
}

func TestResolveContextWindowNoClampWhenCacheMissing(t *testing.T) {
	provider := &domain.Provider{
		Kind: domain.ProviderCodex,
		Models: []domain.Model{
			{ID: "gpt-5.6-luna", Context: 1_050_000},
		},
	}
	settings := domain.Settings{MaxInputTokens: 200_000}
	app := &App{CodexContextWindowCache: stubCodexContextCache{}}
	if got := app.resolveContextWindow(provider, "gpt-5.6-luna", settings); got != 1_050_000 {
		t.Fatalf("codex context without cache hit = %d, want 1050000 (no clamp)", got)
	}
}

func TestResolveContextWindowNoClampWhenCacheNil(t *testing.T) {
	provider := &domain.Provider{
		Kind: domain.ProviderCodex,
		Models: []domain.Model{
			{ID: "gpt-5.6-luna", Context: 1_050_000},
		},
	}
	settings := domain.Settings{MaxInputTokens: 200_000}
	app := &App{}
	if got := app.resolveContextWindow(provider, "gpt-5.6-luna", settings); got != 1_050_000 {
		t.Fatalf("codex context with nil cache = %d, want 1050000 (no clamp)", got)
	}
}

func TestInterruptTurnKeepsReasoning(t *testing.T) {
	conv := &domain.Conversation{
		ID:       "c1",
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleAssistant}},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		runs:          map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	app.interruptTurn(run, "m1", streamedTurnRound{Content: "hello", Reasoning: "think"}, ChatUsage{InputTokens: 100, OutputTokens: 4}, ChatUsage{InputTokens: 100, OutputTokens: 4}.ContextTokens(), "model-x")

	got := conv.Messages[0]
	if got.Content != "hello" || got.Reasoning != "think" || got.Status != domain.StatusInterrupted {
		t.Fatalf("interrupted message = %+v", got)
	}
	if got.Model != "model-x" {
		t.Fatalf("model = %q", got.Model)
	}
	if conv.ContextTokens != 104 {
		t.Fatalf("interrupt should persist context tokens (input+output), got %d", conv.ContextTokens)
	}
}

func TestExecuteTurnToolsStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	box := &recordingToolbox{}
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{{
			ID:        "m1",
			ToolCalls: []domain.ToolCall{{ID: "t1", Name: "web_fetch"}, {ID: "t2", Name: "mcp_list"}},
		}},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       box,
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	if err := app.executeTurnTools(run, "m1", conv.Messages[0].ToolCalls, ModelCapabilities{Vision: true}, domain.Settings{}); err == nil {
		t.Fatal("want context error")
	}
	if len(box.names) != 0 {
		t.Fatalf("executed tools %v, want none after cancel", box.names)
	}
	if conv.Messages[0].ToolCalls[0].Status != domain.ToolInterrupted || conv.Messages[0].ToolCalls[1].Status != domain.ToolInterrupted {
		t.Fatalf("tool statuses = %+v", conv.Messages[0].ToolCalls)
	}
}

// TestRunOneToolKeepsPartialOutputInError verifies that when a streaming
// executor returns an error carrying the partial output received so far
// (the same shape executeExecToolChunks produces on idle/timeout/cancel
// paths), the runOneTool layer preserves the error text in the persisted
// tool call result rather than collapsing it to a generic prefix. The
// end-to-end "exec actually killed mid-stream" case is covered by
// TestExecStreamedCancellation in infrastructure/tools; this test pins the
// application-level contract so a reloaded conversation keeps the streamed
// lines for any non-OK exit.
func TestRunOneToolKeepsPartialOutputInError(t *testing.T) {
	partialErr := fmt.Errorf("no output for 800ms (idle timeout); partial output:\nline-1\nline-2\n")
	toolbox := &partialOutputToolbox{err: partialErr}
	app := &App{Bus: NewBus(), Toolbox: toolbox}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background(), Cancel: func() {}}
	res := app.runOneTool(run, domain.ToolCall{ID: "t1", Name: "exec", Args: `{}`}, ModelCapabilities{}, domain.Settings{})
	if res.status != domain.ToolFailed {
		t.Fatalf("status = %s, want failed", res.status)
	}
	if !strings.Contains(res.output, "line-1") {
		t.Fatalf("partial streamed output lost from persisted result: %q", res.output)
	}
	if !strings.HasPrefix(res.output, "error: ") {
		t.Fatalf("non-cancellation errors must still carry the error: prefix; got %q", res.output)
	}
}

// partialOutputToolbox implements the optional streaming capability and
// always returns the supplied error, so the test can assert the App keeps
// the rich error text verbatim in the tool result.
type partialOutputToolbox struct {
	err error
}

func (p *partialOutputToolbox) ListTools() []ToolInfo { return nil }
func (p *partialOutputToolbox) Execute(context.Context, string, []byte) (string, error) {
	return "", p.err
}
func (p *partialOutputToolbox) ExecuteStreamed(context.Context, string, []byte, func(string)) (string, error) {
	return "", p.err
}

type recordingToolbox struct {
	mu    sync.Mutex
	names []string
}

func (r *recordingToolbox) ListTools() []ToolInfo { return nil }
func (r *recordingToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
	return "ok", nil
}

// streamedRecordingToolbox records names and forwards output chunks for
// streaming-capable calls, mirroring the real Toolbox behavior. Execute and
// ExecuteStreamed are both pointer-receiver methods so embedding promotes
// them onto *streamedRecordingToolbox (the type App.Toolbox holds).
type streamedRecordingToolbox struct {
	*recordingToolbox
	chunks []string
}

func (s *streamedRecordingToolbox) ExecuteStreamed(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (string, error) {
	s.recordingToolbox.mu.Lock()
	s.recordingToolbox.names = append(s.recordingToolbox.names, name)
	s.recordingToolbox.mu.Unlock()
	if onChunk != nil {
		onChunk("line-1\n")
		onChunk("line-2\n")
	}
	if ctx.Err() != nil {
		// Mirrors the real exec executor: the cancellation error carries the
		// partial output received so far.
		return "", fmt.Errorf("exec cancelled: %w\npartial output:\nline-1\nline-2\n", ctx.Err())
	}
	return "streamed-ok", nil
}

// Execute shadows the embedded recordingToolbox.Execute so a streamed call
// still counts in the names log when the App falls through to the non-stream
// path; in practice every call here hits ExecuteStreamed and never reaches it.
func (s *streamedRecordingToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	return "streamed-ok", nil
}

// TestRunOneToolStreamsDeltas verifies that a streaming-capable toolbox
// emits one agent.tool.delta event per output chunk during tool execution,
// and that the final result is preserved.
func TestRunOneToolStreamsDeltas(t *testing.T) {
	var mu sync.Mutex
	var deltas []contracts.ToolDeltaEvent
	bus := NewBus()
	subscribeEvents := func(ch <-chan contracts.Event, done func(), onDelta func(contracts.ToolDeltaEvent)) {
		go func() {
			defer done()
			for ev := range ch {
				if ev.Type == contracts.EventToolDelta {
					var d contracts.ToolDeltaEvent
					b, _ := json.Marshal(ev.Payload)
					_ = json.Unmarshal(b, &d)
					onDelta(d)
				}
			}
		}()
	}
	_, ch, unsubscribe := bus.Subscribe()
	done := make(chan struct{})
	subscribeEvents(ch, func() { close(done) }, func(d contracts.ToolDeltaEvent) {
		mu.Lock()
		deltas = append(deltas, d)
		mu.Unlock()
	})
	defer unsubscribe()

	app := &App{Bus: bus, Toolbox: &streamedRecordingToolbox{recordingToolbox: &recordingToolbox{}}}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	res := app.runOneTool(run, domain.ToolCall{ID: "t1", Name: "exec", Args: `{"command":"echo hi"}`}, ModelCapabilities{}, domain.Settings{})
	if res.output != "streamed-ok" {
		t.Fatalf("output = %q", res.output)
	}
	unsubscribe()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("delta stream never closed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}
	for _, d := range deltas {
		if d.RunID != "r1" || d.ConversationID != "c1" || d.ToolCallID != "t1" || d.Name != "exec" {
			t.Fatalf("bad delta metadata: %+v", d)
		}
	}
	if deltas[0].Text != "line-1\n" || deltas[1].Text != "line-2\n" {
		t.Fatalf("delta text order wrong: %+v", deltas)
	}
}

// TestRunOneToolNoStreamFallback verifies toolboxes without the streaming
// capability still execute normally and emit no deltas.
func TestRunOneToolNoStreamFallback(t *testing.T) {
	bus := NewBus()
	var deltaCount int
	_, ch, unsubscribe := bus.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			if ev.Type == contracts.EventToolDelta {
				deltaCount++
			}
		}
	}()
	defer unsubscribe()

	app := &App{Bus: bus, Toolbox: &recordingToolbox{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	res := app.runOneTool(run, domain.ToolCall{ID: "t1", Name: "exec", Args: `{"command":"echo hi"}`}, ModelCapabilities{}, domain.Settings{})
	if res.output != "ok" {
		t.Fatalf("output = %q", res.output)
	}
	unsubscribe()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event stream never closed")
	}
	if deltaCount != 0 {
		t.Fatalf("expected no deltas, got %d", deltaCount)
	}
}

func TestRunTurnFailsWhenAllCodexAccountsBlocked(t *testing.T) {
	conv := &domain.Conversation{
		ID:       "c1",
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleAssistant}},
	}
	creds := &memCreds{m: map[string]string{
		"prov":              `{"access_token":"active","account_id":"acc1"}`,
		"prov:account:acc1": `{"access_token":"t1","account_id":"acc1"}`,
		"prov:account:acc2": `{"access_token":"t2","account_id":"acc2"}`,
	}}
	router := NewCodexAccountRouter()
	router.MarkCircuitOpen("acc1", time.Now().Add(time.Hour))
	router.MarkCircuitOpen("acc2", time.Now().Add(2*time.Hour))

	factoryCalled := false
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Credentials:   creds,
		CodexRouter:   router,
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
			factoryCalled = true
			return nil, errors.New("factory should not run when all Codex accounts are blocked")
		},
		runs: map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	provider := &domain.Provider{ID: "prov", Kind: domain.ProviderCodex}

	app.runTurn(run, provider, "active-token", "gpt-5.3-codex", "", "m1", false, ModelCapabilities{Vision: true})

	if factoryCalled {
		t.Fatal("factory must not run when every Codex account is blocked")
	}
	if conv.Messages[0].Status != domain.StatusError {
		t.Fatalf("status = %q, want error", conv.Messages[0].Status)
	}
	if !strings.Contains(conv.Messages[0].Error, "rate-limited") {
		t.Fatalf("error = %q, want rate-limited message", conv.Messages[0].Error)
	}
}

func TestDiscardQueuedSteerOnFail(t *testing.T) {
	conv := &domain.Conversation{
		ID:       "c1",
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleAssistant}},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		runs:          map[string]*TurnRun{},
	}
	_, events, unsub := app.Bus.Subscribe()
	defer unsub()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background(), Cancel: func() {}}
	if !run.queueSteer(&SteerEntry{ID: "s1", Text: "wait", Status: "queued"}) {
		t.Fatal("queue steer")
	}
	app.failTurn(run, "m1", context.Canceled)
	if run.queuedSteer() != nil {
		t.Fatal("queued steer should be discarded on fail")
	}
	gotCancel := false
	for i := 0; i < 8; i++ {
		select {
		case ev := <-events:
			if ev.Type == contracts.EventSteerCancelled {
				gotCancel = true
			}
		default:
			i = 8
		}
	}
	if !gotCancel {
		t.Fatal("expected steer cancelled event")
	}
}

func TestToolRoundSignatureStable(t *testing.T) {
	// Same tools, different order → same signature (set-based, not order-dependent).
	a := []domain.ToolCall{
		{Name: "mcp_enable", Args: `{"id":"x"}`},
		{Name: "tool_schema", Args: `{"server":"Files","tool":"read"}`},
	}
	b := []domain.ToolCall{
		{Name: "tool_schema", Args: `{"server":"Files","tool":"read"}`},
		{Name: "mcp_enable", Args: `{"id":"x"}`},
	}
	if sigA, sigB := toolRoundSignature(a), toolRoundSignature(b); sigA != sigB {
		t.Fatalf("order-independent signature mismatch:\n  A=%q\n  B=%q", sigA, sigB)
	}
}

func TestRepeatedToolGuardParallelLoop(t *testing.T) {
	// Simulate GPT-5.6 Luna behavior: 6 parallel tool calls, same set
	// every round, no text. The guard must fire after RepeatedToolLimit
	// consecutive identical rounds.
	const limit = 3
	g := &repeatedToolGuard{limit: limit}
	tools := []domain.ToolCall{
		{Name: "mcp_enable", Args: `{"id":"nusashell.files"}`},
		{Name: "mcp_enable", Args: `{"id":"nusashell.terminal"}`},
		{Name: "mcp_enable", Args: `{"id":"nusashell.kanban"}`},
		{Name: "mcp_enable", Args: `{"id":"nusashell.notes"}`},
		{Name: "tool_schema", Args: `{"server":"Files","tool":"read"}`},
		{Name: "tool_schema", Args: `{"server":"Terminal","tool":"exec"}`},
	}

	for i := 1; i <= limit-1; i++ {
		if fired := g.check(tools, ""); fired {
			t.Fatalf("round %d: guard fired too early (limit=%d)", i, limit)
		}
	}
	if !g.check(tools, "") {
		t.Fatal("guard should fire on the limit-th identical parallel round")
	}
	// After firing, the guard resets — a new identical round should not fire immediately.
	if fired := g.check(tools, ""); fired {
		t.Fatal("guard should reset after firing, not fire again immediately")
	}
}

func TestRepeatedToolGuardResetsOnContent(t *testing.T) {
	g := &repeatedToolGuard{limit: 3}
	tools := []domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"x"}`}}

	for i := 1; i <= 2; i++ {
		g.check(tools, "")
	}
	// A round with text content resets the streak.
	g.check(tools, "here is my answer")
	if fired := g.check(tools, ""); fired {
		t.Fatal("guard should reset after a round with content, not fire")
	}
}

func TestRepeatedToolGuardDifferentArgsResets(t *testing.T) {
	g := &repeatedToolGuard{limit: 3}
	g.check([]domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"a"}`}}, "")
	g.check([]domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"a"}`}}, "")
	// Different args → new signature → streak resets.
	g.check([]domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"b"}`}}, "")
	if fired := g.check([]domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"b"}`}}, ""); fired {
		t.Fatal("different args should reset streak, not fire after 2 rounds")
	}
}

type overflowThenOKAdapter struct {
	mu        sync.Mutex
	streams   int
	completes int
	secondReq ChatRequest
	streamErr error
	summary   string
}

func (a *overflowThenOKAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }
func (a *overflowThenOKAdapter) Stream(_ context.Context, req ChatRequest, _, _ func(string)) (ChatResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streams++
	if a.streams == 1 {
		err := a.streamErr
		if err == nil {
			err = &UpstreamError{StatusCode: 400, Err: errors.New("maximum context length exceeded")}
		}
		return ChatResponse{}, err
	}
	a.secondReq = req
	return ChatResponse{Content: "ok after compact"}, nil
}
func (a *overflowThenOKAdapter) Complete(context.Context, ChatRequest) (ChatResponse, error) {
	a.mu.Lock()
	a.completes++
	a.mu.Unlock()
	summary := a.summary
	if summary == "" {
		summary = validTestSummary
	}
	return ChatResponse{Content: summary}, nil
}

func bulkyHistory(pendingID string) []domain.Message {
	msgs := []domain.Message{{
		ID: "u0", Role: domain.RoleUser, Content: strings.Repeat("user-goal ", 80), Status: domain.StatusDone,
	}}
	for i := 0; i < 20; i++ {
		msgs = append(msgs,
			domain.Message{ID: fmt.Sprintf("a%d", i), Role: domain.RoleAssistant, Content: strings.Repeat("assistant-work ", 80), Status: domain.StatusDone},
			domain.Message{ID: fmt.Sprintf("u%d", i+1), Role: domain.RoleUser, Content: strings.Repeat("follow-up ", 80), Status: domain.StatusDone},
		)
	}
	msgs = append(msgs, domain.Message{ID: pendingID, Role: domain.RoleAssistant})
	return msgs
}

func TestEmergencyCompactionReinjectsHydration(t *testing.T) {
	conv := &domain.Conversation{ID: "c1", Messages: bulkyHistory("m1")}
	adapter := &overflowThenOKAdapter{}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	settings.CompactionThreshold = 1
	settings.MaxInputTokens = 8000
	settings.MaxOutputTokens = 256
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		Settings:      &fakeSettingsStore{settings: settings},
		Factory: func(context.Context, *domain.Provider, string) (AIProvider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "m1", false, ModelCapabilities{Vision: true})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.streams < 2 {
		t.Fatalf("streams = %d, want retry after emergency compaction", adapter.streams)
	}
	if adapter.completes == 0 {
		t.Fatal("expected compaction Complete calls")
	}
	if !HasHydration(adapter.secondReq.Messages) {
		t.Fatal("post-compaction retry must include a fresh hydration checkpoint")
	}
}

func TestEmergencyCompactionSkippedWhenEstimateBelowTrigger(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "short", Status: domain.StatusDone},
			{ID: "m1", Role: domain.RoleAssistant},
		},
	}
	adapter := &overflowThenOKAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		Settings:      &fakeSettingsStore{settings: settings},
		Factory: func(context.Context, *domain.Provider, string) (AIProvider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "m1", false, ModelCapabilities{Vision: true})

	if adapter.completes != 0 {
		t.Fatalf("Complete calls = %d, want 0 (must not compact a small transcript)", adapter.completes)
	}
	if adapter.streams != 1 {
		t.Fatalf("streams = %d, want 1 failed attempt", adapter.streams)
	}
	// Locate the turn's assistant message by ID: the hydration checkpoint is
	// inserted before the placeholder, so its index depends on injection.
	var asst *domain.Message
	for i := range conv.Messages {
		if conv.Messages[i].ID == "m1" {
			asst = &conv.Messages[i]
			break
		}
	}
	if asst == nil {
		t.Fatalf("assistant message m1 missing from transcript (%d messages)", len(conv.Messages))
	}
	if asst.Status != domain.StatusError {
		t.Fatalf("status = %q, want error", asst.Status)
	}
}

// midTurnCompactionAdapter returns a tool call on the first Stream and
// final text on the second. It tracks whether compaction ran (Complete
// calls) and whether any stream returned an overflow error (proactive
// compaction should prevent the 400 that triggers emergency compaction).
type midTurnCompactionAdapter struct {
	mu         sync.Mutex
	streams    int
	completes  int
	overflowed bool
}

func (a *midTurnCompactionAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }
func (a *midTurnCompactionAdapter) Stream(_ context.Context, _ ChatRequest, _, _ func(string)) (ChatResponse, error) {
	a.mu.Lock()
	a.streams++
	n := a.streams
	a.mu.Unlock()
	if n == 1 {
		return ChatResponse{
			Content:   "let me read that file",
			ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "read_file", Args: `{"path":"/big"}`}},
		}, nil
	}
	return ChatResponse{Content: "done after compaction"}, nil
}
func (a *midTurnCompactionAdapter) Complete(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	a.mu.Lock()
	a.completes++
	a.mu.Unlock()
	return ChatResponse{Content: validTestSummary}, nil
}

// largeOutputToolbox returns a fixed large string for any tool call,
// simulating a tool result that grows the context past the compaction
// trigger mid-turn.
type largeOutputToolbox struct {
	mu     sync.Mutex
	names  []string
	output string
}

func (t *largeOutputToolbox) ListTools() []ToolInfo { return nil }
func (t *largeOutputToolbox) Execute(_ context.Context, name string, _ []byte) (string, error) {
	t.mu.Lock()
	t.names = append(t.names, name)
	t.mu.Unlock()
	return t.output, nil
}

// TestMidTurnProactiveCompaction verifies that compaction fires
// proactively between tool rounds when context grows past the trigger —
// not waiting for a 400 overflow (emergency compaction). This mirrors
// the Hermes pre-API pressure check: the round loop checks
// EstimateTokens before each API call, not just at turn start.
func TestMidTurnProactiveCompaction(t *testing.T) {
	// Initial conversation: small enough to be under the compaction
	// trigger at turn start (so initializeTurn does not compact).
	// Round 1's tool result will push context past the trigger; the
	// mid-turn check before round 2 must fire compaction proactively.
	conv := &domain.Conversation{ID: "c1", Messages: []domain.Message{
		{ID: "u0", Role: domain.RoleUser, Content: strings.Repeat("goal ", 100), Status: domain.StatusDone},
		{ID: "m1", Role: domain.RoleAssistant},
	}}
	adapter := &midTurnCompactionAdapter{}
	// ~13000 chars ≈ 3250 tokens — enough to push a ~125-token start
	// past the 1395-token trigger (80% of (2000-256)).
	toolbox := &largeOutputToolbox{output: strings.Repeat("tool result line of content. ", 500)}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	settings.CompactionThreshold = 0 // auto = 80% of (window - maxOutput)
	settings.MaxInputTokens = 2000
	settings.MaxOutputTokens = 256
	settings.MaxToolRounds = 10
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       toolbox,
		Settings:      &fakeSettingsStore{settings: settings},
		Factory: func(context.Context, *domain.Provider, string) (AIProvider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "m1", false, ModelCapabilities{})

	if adapter.completes == 0 {
		t.Fatal("expected proactive compaction (Complete calls > 0), got 0 — mid-turn check did not fire before round 2")
	}
	if adapter.overflowed {
		t.Fatal("unexpected 400 overflow — proactive compaction should prevent emergency compaction")
	}
	if adapter.streams != 2 {
		t.Fatalf("streams = %d, want 2 (round 1 tool call + round 2 final text after proactive compaction)", adapter.streams)
	}
}

type recordingCompleteAdapter struct {
	mu                sync.Mutex
	requests          []ChatRequest
	summaries         []string
	toolCallSummaries []string // if set, return summary() tool call instead of Content
}

func (a *recordingCompleteAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }
func (a *recordingCompleteAdapter) Stream(context.Context, ChatRequest, func(string), func(string)) (ChatResponse, error) {
	return ChatResponse{}, errors.New("stream not used")
}
func (a *recordingCompleteAdapter) Complete(_ context.Context, req ChatRequest) (ChatResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, req)
	idx := len(a.requests) - 1
	summary := "summary-pass"
	if idx < len(a.summaries) {
		summary = a.summaries[idx]
	} else if len(a.summaries) > 0 {
		summary = a.summaries[len(a.summaries)-1]
	}
	// If toolCallSummaries is configured, return a summary() tool call
	// instead of Content (simulates a reasoning model that uses the tool).
	if idx < len(a.toolCallSummaries) || (len(a.toolCallSummaries) > 0 && idx >= len(a.toolCallSummaries)) {
		tcSummary := a.toolCallSummaries[min(idx, len(a.toolCallSummaries)-1)]
		return ChatResponse{
			Content: "",
			ToolCalls: []domain.ToolCall{{
				ID:   fmt.Sprintf("call_%d", idx),
				Name: compactionSummaryToolName,
				Args: fmt.Sprintf(`{"text":%q}`, tcSummary),
			}},
		}, nil
	}
	return ChatResponse{Content: summary}, nil
}

func TestCompactionPassBudgetShrinksWithRunningSummary(t *testing.T) {
	// Window must leave room above the summary max-out floor
	// (compactionSummaryMaxOut=64000) so the shrink is observable.
	const window = 100_000
	empty := compactionPassAvailable(window, "", compactionSummaryMaxOut)
	grown := compactionPassAvailable(window, strings.Repeat("x", 8000), compactionSummaryMaxOut)
	if grown >= empty {
		t.Fatalf("available with large summary %d should be < empty %d", grown, empty)
	}
	if empty-grown < 1900 {
		t.Fatalf("expected ~2000 token shrink, empty=%d grown=%d", empty, grown)
	}
}

func TestExtractCompactionSummaryPrefersToolCall(t *testing.T) {
	// When the model calls summary(text="..."), extract from the tool call.
	resp := ChatResponse{
		Content: "plain text that should be ignored",
		ToolCalls: []domain.ToolCall{
			{Name: compactionSummaryToolName, Args: `{"text":"handoff checkpoint from tool call"}`},
		},
	}
	got := extractCompactionSummary(resp)
	if got != "handoff checkpoint from tool call" {
		t.Fatalf("extractCompactionSummary = %q, want tool call text", got)
	}
}

func TestExtractCompactionSummaryFallsBackToContent(t *testing.T) {
	// When the model doesn't call the tool, fall back to resp.Content.
	resp := ChatResponse{Content: "plain text fallback"}
	got := extractCompactionSummary(resp)
	if got != "plain text fallback" {
		t.Fatalf("extractCompactionSummary = %q, want content fallback", got)
	}
}

func TestExtractCompactionSummaryFallsBackOnBadArgs(t *testing.T) {
	// If the tool call args are malformed, fall back to resp.Content.
	resp := ChatResponse{
		Content:   "fallback after bad args",
		ToolCalls: []domain.ToolCall{{Name: compactionSummaryToolName, Args: `not json`}},
	}
	got := extractCompactionSummary(resp)
	if got != "fallback after bad args" {
		t.Fatalf("extractCompactionSummary = %q, want content fallback on bad args", got)
	}
}

func TestExtractCompactionSummaryIgnoresEmptyToolText(t *testing.T) {
	// If the tool call has empty text, fall back to resp.Content.
	resp := ChatResponse{
		Content:   "fallback when tool text empty",
		ToolCalls: []domain.ToolCall{{Name: compactionSummaryToolName, Args: `{"text":""}`}},
	}
	got := extractCompactionSummary(resp)
	if got != "fallback when tool text empty" {
		t.Fatalf("extractCompactionSummary = %q, want content fallback on empty tool text", got)
	}
}

func TestCompactionUsesSummaryTool(t *testing.T) {
	// Verify that compactConversation advertises the summary() tool and
	// extracts the summary from the tool call args, not resp.Content.
	var msgs []domain.Message
	body := strings.Repeat("abcdefghij", 80) // 800 chars ≈ 200 tokens
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{
		toolCallSummaries: []string{validTestSummary},
	}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800
	summary, err := app.compactConversation(context.Background(), adapter, conv, "model", 4000, settings)
	if err != nil {
		t.Fatal(err)
	}
	if summary != validTestSummary {
		t.Fatalf("summary = %q, want tool call text", summary)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) == 0 {
		t.Fatal("no Complete calls")
	}
	req := adapter.requests[0]
	found := false
	for _, tool := range req.Tools {
		if tool.Name == compactionSummaryToolName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("compaction request did not advertise %s tool", compactionSummaryToolName)
	}
}

func TestCompactionRetriesOnShortSummary(t *testing.T) {
	// First 2 passes return short summaries, 3rd returns a good one.
	// Verify the guard retries and eventually succeeds.
	var msgs []domain.Message
	body := strings.Repeat("abcdefghij", 80)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{
		toolCallSummaries: []string{"short", "also short", strings.Repeat("handoff checkpoint with enough detail ", 10)},
	}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800
	summary, err := app.compactConversation(context.Background(), adapter, conv, "model", 4000, settings)
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if len(summary) < compactionSummaryMinChars {
		t.Fatalf("summary len=%d, want >= %d", len(summary), compactionSummaryMinChars)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) < 3 {
		t.Fatalf("expected >= 3 Complete calls (retries), got %d", len(adapter.requests))
	}
}

func TestCompactionFailsAfterMaxRetries(t *testing.T) {
	// All passes return short summaries. Verify compaction fails with error.
	var msgs []domain.Message
	body := strings.Repeat("abcdefghij", 80)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{
		toolCallSummaries: []string{"short"},
	}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800
	_, err := app.compactConversation(context.Background(), adapter, conv, "model", 4000, settings)
	if err == nil {
		t.Fatal("expected error when all retries produce short summaries, got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Fatalf("error should mention 'too short', got: %v", err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	// 1 initial + 5 retries = 6 calls
	if len(adapter.requests) != compactionSummaryMaxRetries+1 {
		t.Fatalf("expected %d Complete calls (1 + %d retries), got %d",
			compactionSummaryMaxRetries+1, compactionSummaryMaxRetries, len(adapter.requests))
	}
}

func TestCompactionBudgetDoublesOnRetry(t *testing.T) {
	// Verify that each retry doubles the max_output_tokens budget.
	// Use a large context window so the clamp never triggers.
	var msgs []domain.Message
	body := strings.Repeat("abcdefghij", 80)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{
		toolCallSummaries: []string{"short"},
	}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800
	_, _ = app.compactConversation(context.Background(), adapter, conv, "model", 200000, settings)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	expectedBudget := 800
	for i, req := range adapter.requests {
		if req.MaxTokens != expectedBudget {
			t.Fatalf("attempt %d: MaxTokens=%d, want %d", i, req.MaxTokens, expectedBudget)
		}
		expectedBudget *= 2
	}
}

func TestCompactionBudgetClampedToContextWindow(t *testing.T) {
	// Verify that the retry budget is clamped to the context window so it
	// never exceeds what the model can accept.
	var msgs []domain.Message
	body := strings.Repeat("abcdefghij", 80)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{
		toolCallSummaries: []string{"short"},
	}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800
	// Small context window (2000) so the doubled budget hits the clamp.
	// maxBudget = 2000 - 300 (systemReserve) = 1700
	_, _ = app.compactConversation(context.Background(), adapter, conv, "model", 2000, settings)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	maxBudget := 2000 - compactionSystemReserve
	for i, req := range adapter.requests {
		if req.MaxTokens > maxBudget {
			t.Fatalf("attempt %d: MaxTokens=%d exceeds maxBudget=%d", i, req.MaxTokens, maxBudget)
		}
	}
}

func TestCompactionStripsMediaAttachments(t *testing.T) {
	// Compaction input must not carry media payloads: compaction models are
	// often not vision/audio-capable and providers reject the request
	// outright (OpenRouter HTTP 404 "No endpoints found that support image
	// input"), which made compaction fail and the turn die with a
	// context-overflow 400. Attachments are replaced with a text note.
	var msgs []domain.Message
	msgs = append(msgs, domain.Message{
		ID: "img", Role: domain.RoleUser, Status: domain.StatusDone,
		Content: "what do you think of this screenshot?",
		Attachments: []domain.Attachment{{
			Type: "image", Name: "shot.png", MediaType: "image/png",
			DataURL: "data:image/png;base64,AAAA",
		}},
	})
	body := strings.Repeat("abcdefghij", 100) // ~250 tokens each
	for i := 0; i < 120; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{
		summaries: []string{strings.Repeat("handoff checkpoint with enough detail ", 10)},
	}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	summary, err := app.compactConversation(context.Background(), adapter, conv, "model", 20000, settings)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if len(summary) < compactionSummaryMinChars {
		t.Fatalf("summary too short: %d", len(summary))
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) == 0 {
		t.Fatal("no compaction requests recorded")
	}
	for i, req := range adapter.requests {
		for _, m := range req.Messages {
			if len(m.Attachments) > 0 {
				t.Fatalf("request %d: message carries %d attachments, want stripped", i, len(m.Attachments))
			}
		}
	}
	noteSeen := false
	for _, req := range adapter.requests {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "shot.png") {
				noteSeen = true
			}
		}
	}
	if !noteSeen {
		t.Fatal("attachment note missing from compaction input")
	}
}

func TestCompactionCapsOversizedToolOutput(t *testing.T) {
	// A single oversized tool result (e.g. grep over huge lines) must be
	// truncated in the compaction input so the pass fits the compaction
	// model's context window; otherwise compaction overflows and the turn
	// dies with a context-overflow 400.
	var msgs []domain.Message
	bigOutput := strings.Repeat("tool-result-line ", 200_000) // ~3.4MB
	msgs = append(msgs, domain.Message{
		ID: "asst", Role: domain.RoleAssistant, Status: domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID: "call1", Name: "grep", Args: `{"pattern":"x","path":"."}`, Output: bigOutput, Status: domain.ToolOK,
		}},
	})
	body := strings.Repeat("abcdefghij", 100)
	for i := 0; i < 120; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{
		summaries: []string{strings.Repeat("handoff checkpoint with enough detail ", 10)},
	}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	// Small window keeps the per-call cap small so truncation is observable.
	_, err := app.compactConversation(context.Background(), adapter, conv, "model", 2000, settings)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	truncated := false
	for _, req := range adapter.requests {
		for _, m := range req.Messages {
			if m.ToolResult == nil {
				continue
			}
			if len(m.ToolResult.Content) > 2200 {
				t.Fatalf("tool result not capped: %d chars", len(m.ToolResult.Content))
			}
			if strings.Contains(m.ToolResult.Content, "[truncated:") {
				truncated = true
			}
		}
	}
	if !truncated {
		t.Fatal("oversized tool output was not truncated in compaction input")
	}
}

func TestCompactionSummaryMinCharsFromSettings(t *testing.T) {
	// Verify that CompactionSummaryMinChars from settings overrides the
	// built-in default. Set a high threshold so a normal summary fails.
	var msgs []domain.Message
	body := strings.Repeat("abcdefghij", 80)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	// validTestSummary is ~360 chars. Set min to 500 so it fails.
	adapter := &recordingCompleteAdapter{
		toolCallSummaries: []string{validTestSummary},
	}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800
	settings.CompactionSummaryMinChars = 500
	_, err := app.compactConversation(context.Background(), adapter, conv, "model", 4000, settings)
	if err == nil {
		t.Fatal("expected error when summary < settings min chars, got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Fatalf("error should mention 'too short', got: %v", err)
	}
}

func TestMultiPassCompactionShrinksLaterChunks(t *testing.T) {
	var msgs []domain.Message
	body := strings.Repeat("abcdefghij", 80) // 800 chars ≈ 200 tokens
	for i := 0; i < 30; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	hugeSummary := strings.Repeat("folded-summary-", 400) // ~1500+ tokens
	adapter := &recordingCompleteAdapter{summaries: []string{hugeSummary, validTestSummary}}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	// Use a small summary max tokens so the multi-pass shrinking is visible
	// with the small contextWindow=4000. The default (16000) would be clamped
	// to maxBudget=3700 and leave no room for message content after the
	// summary reserve.
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800
	if _, err := app.compactConversation(context.Background(), adapter, conv, "model", 4000, settings); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) < 2 {
		t.Fatalf("Complete calls = %d, want multi-pass", len(adapter.requests))
	}
	firstContent := 0
	for _, m := range adapter.requests[0].Messages {
		firstContent += domain.EstimateTokens(m.Content)
	}
	secondContent := 0
	for _, m := range adapter.requests[1].Messages {
		secondContent += domain.EstimateTokens(m.Content)
	}
	// Second pass carries the huge running summary, so remaining message
	// content must be smaller than the first pass's message payload.
	firstMsgs := 0
	for _, m := range adapter.requests[0].Messages {
		if !strings.HasPrefix(m.Content, compactionSummaryPrefix) {
			firstMsgs += domain.EstimateTokens(m.Content)
		}
	}
	secondMsgs := 0
	for _, m := range adapter.requests[1].Messages {
		if !strings.HasPrefix(m.Content, compactionSummaryPrefix) {
			secondMsgs += domain.EstimateTokens(m.Content)
		}
	}
	if secondMsgs >= firstMsgs {
		t.Fatalf("later pass message tokens %d should shrink vs first pass %d (total second %d first %d)", secondMsgs, firstMsgs, secondContent, firstContent)
	}
}

func TestCompactionArchiveStripsHydration(t *testing.T) {
	hydrate := domain.Message{
		ID:   "h1",
		Role: domain.RoleAssistant,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.HydrateToolCallPrefix + "abc_0",
			Name:   "runtime_context",
			Output: `{"currentDate":"2026-08-21"}`,
		}},
		Status: domain.StatusDone,
	}
	var msgs []domain.Message
	msgs = append(msgs, hydrate)
	body := strings.Repeat("archived-turn ", 80)
	for i := 0; i < 25; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("u%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
	}
	adapter := &recordingCompleteAdapter{summaries: []string{validTestSummary}}
	if _, err := app.compactConversation(context.Background(), adapter, conv, "model", 4000, domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	for _, m := range store.archived {
		if isHydrationMessage(m) {
			t.Fatal("archived chunk must not include hydration checkpoints")
		}
	}
	got, err := store.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Messages {
		if isHydrationMessage(m) {
			t.Fatal("live transcript must not keep hydration after compaction")
		}
	}
}

func TestCompactionStripsToolOutputImageAttachments(t *testing.T) {
	huge := "data:image/png;base64," + strings.Repeat("A", 8000)
	body := strings.Repeat("please draw a harbor scene in detail ", 80)
	msgs := []domain.Message{{
		ID: "a1", Role: domain.RoleAssistant, Content: "ok", Status: domain.StatusDone, ToolCalls: []domain.ToolCall{{
			ID: "tc1", Name: "generate_image", Output: "Image saved to /tmp/gen-tc1.png",
			OutputAttachments: []domain.Attachment{{
				Type: "image", Name: "gen-tc1.png", MediaType: "image/png",
				DataURL: huge, FilePath: "/tmp/gen-tc1.png",
			}},
		}},
	}}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("u%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{summaries: []string{validTestSummary}}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	if _, err := app.compactConversation(context.Background(), adapter, conv, "model", 4000, domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) == 0 {
		t.Fatal("expected compaction Complete call")
	}
	for _, req := range adapter.requests {
		for _, msg := range req.Messages {
			if msg.ToolResult == nil {
				continue
			}
			if len(msg.ToolResult.Attachments) > 0 {
				t.Fatalf("compaction must not replay image attachments: %+v", msg.ToolResult.Attachments)
			}
			if strings.Contains(msg.ToolResult.Content, huge) {
				t.Fatal("compaction payload still contains image data URL")
			}
		}
	}
}

func TestHealOrphanedRunningConversationHealsGhost(t *testing.T) {
	conv := &domain.Conversation{
		ID:     "c1",
		Status: "running",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "hi", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant, Content: "", Status: ""},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		runs:          map[string]*TurnRun{},
	}
	if !app.healOrphanedRunningConversation(conv) {
		t.Fatal("expected orphaned conversation to be healed")
	}
	if conv.Status != "idle" {
		t.Fatalf("status = %q, want idle", conv.Status)
	}
	if conv.Messages[1].Status != domain.StatusInterrupted {
		t.Fatalf("ghost message status = %q, want interrupted", conv.Messages[1].Status)
	}
}

func TestHealOrphanedRunningConversationSkipsWhenRunActive(t *testing.T) {
	conv := &domain.Conversation{
		ID:     "c1",
		Status: "running",
		Messages: []domain.Message{
			{ID: "a1", Role: domain.RoleAssistant, Content: "", Status: ""},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		runs: map[string]*TurnRun{
			"r1": {ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel},
		},
	}
	if app.healOrphanedRunningConversation(conv) {
		t.Fatal("should not heal when a run is active")
	}
	if conv.Status != "running" {
		t.Fatalf("status = %q, want running (untouched)", conv.Status)
	}
}

func TestHealOrphanedRunningConversationSkipsIdle(t *testing.T) {
	conv := &domain.Conversation{
		ID:     "c1",
		Status: "idle",
		Messages: []domain.Message{
			{ID: "a1", Role: domain.RoleAssistant, Content: "done", Status: domain.StatusDone},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		runs:          map[string]*TurnRun{},
	}
	if app.healOrphanedRunningConversation(conv) {
		t.Fatal("should not heal an idle conversation")
	}
}

func TestRecoverOrphanedTurnHealsAndEmitsError(t *testing.T) {
	conv := &domain.Conversation{
		ID:     "c1",
		Status: "running",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "hi", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant, Content: "", Status: ""},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		runs:          map[string]*TurnRun{},
	}
	_, events, unsub := app.Bus.Subscribe()
	defer unsub()
	ctx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}

	app.recoverOrphanedTurn(run)

	if conv.Status != "idle" {
		t.Fatalf("status = %q, want idle", conv.Status)
	}
	if conv.Messages[1].Status != domain.StatusInterrupted {
		t.Fatalf("ghost message status = %q, want interrupted", conv.Messages[1].Status)
	}
	if conv.Messages[1].Error != domain.OrphanedTurnError {
		t.Fatalf("error = %q, want OrphanedTurnError", conv.Messages[1].Error)
	}
	gotError := false
	for i := 0; i < 8; i++ {
		select {
		case ev := <-events:
			if ev.Type == contracts.EventTurnError {
				gotError = true
			}
		default:
			i = 8
		}
	}
	if !gotError {
		t.Fatal("expected EventTurnError to be emitted")
	}
}

func TestRecoverOrphanedTurnSkipsIdleConversation(t *testing.T) {
	conv := &domain.Conversation{
		ID:     "c1",
		Status: "idle",
		Messages: []domain.Message{
			{ID: "a1", Role: domain.RoleAssistant, Content: "done", Status: domain.StatusDone},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		runs:          map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}

	app.recoverOrphanedTurn(run)

	if conv.Status != "idle" {
		t.Fatalf("status = %q, want idle (unchanged)", conv.Status)
	}
	if conv.Messages[0].Status != domain.StatusDone {
		t.Fatalf("message status = %q, want done (unchanged)", conv.Messages[0].Status)
	}
}
