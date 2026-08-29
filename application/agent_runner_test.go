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

	"nusashell/application/service/learnedparams"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
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

// TestHydrationCheckpointPersistsOnceUntilCompaction verifies the epoch-based
// hydration lifecycle: a fresh room gets one checkpoint via
// ensureFreshRoomHydration, follow-up user messages do NOT add another, and
// persistCompactedConversation rebuilds exactly one fresh checkpoint after
// compaction. The turn loop no longer touches hydration, so the checkpoint
// stays at its epoch anchor (after the first user / handover) across rounds.
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
		Bus:           NewBus(),
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	countCheckpoints := func() int {
		count := 0
		for _, message := range conversation.Messages {
			if isHydrationMessage(message) {
				count++
			}
		}
		return count
	}

	// Fresh room: one user, no checkpoint → ensureFreshRoomHydration builds one.
	app.ensureFreshRoomHydration(run, "a1", ModelCapabilities{})
	if got := countCheckpoints(); got != 1 {
		t.Fatalf("initial checkpoints = %d, want 1", got)
	}

	// Follow-up user message: two users now, checkpoint already present.
	// ensureFreshRoomHydration must skip (not fresh, and HasHydration is true).
	conversation.Messages = append(conversation.Messages,
		domain.Message{ID: "u2", Role: domain.RoleUser, Content: "follow up"},
		domain.Message{ID: "a2", Role: domain.RoleAssistant},
	)
	app.ensureFreshRoomHydration(run, "a2", ModelCapabilities{})
	if got := countCheckpoints(); got != 1 {
		t.Fatalf("checkpoints after later user message = %d, want 1", got)
	}

	// Compaction epoch: persistCompactedConversation strips the old checkpoint
	// and rebuilds a fresh one in the same Save.
	if err := app.persistCompactedConversation(conversation, "summary", 1_000_000); err != nil {
		t.Fatalf("persistCompactedConversation: %v", err)
	}
	if got := countCheckpoints(); got != 1 {
		t.Fatalf("post-compaction checkpoints = %d, want 1 fresh checkpoint", got)
	}
}

func TestChatMessagesSkipsWhitespaceOnlyAssistantTurns(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "hi"},
		{ID: "a1", Role: domain.RoleAssistant, Content: "\n\n", Status: domain.StatusDone},
		{ID: "a2", Role: domain.RoleAssistant, Content: "  \n", Status: domain.StatusDone},
		{ID: "a3", Role: domain.RoleAssistant, Content: "\n\nNow update main.go:\n\n", Status: domain.StatusDone},
		{ID: "a4", Role: domain.RoleAssistant, Content: "\n\n", ToolCalls: []domain.ToolCall{
			{ID: "call_1", Name: "file_read", Args: `{"path":"main.go"}`},
		}, Status: domain.StatusDone},
		{ID: "pending", Role: domain.RoleAssistant},
	}}
	got := chatMessages(c, "pending", ModelCapabilities{})
	if len(got) != 4 {
		t.Fatalf("got %d messages, want user + trimmed text + assistant + tool", len(got))
	}
	if got[0].Role != "user" || got[1].Role != "assistant" || got[1].Content != "Now update main.go:" {
		t.Fatalf("trimmed assistant = %+v", got[1])
	}
	if got[2].Role != "assistant" || got[2].Content != "" || len(got[2].ToolCalls) != 1 {
		t.Fatalf("tool round = %+v", got[2])
	}
	if got[3].Role != "tool" {
		t.Fatalf("tool result role = %s", got[3].Role)
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
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error) {
			return &fakeVisionAdapter{description: "default"}, nil
		},
	}
	defaultAdapter := stubProviderContext(&fakeVisionAdapter{description: "default"})
	settings := domain.Settings{}
	gotAdapter, gotModel, gotWindow := app.resolveCompactionAdapter(context.Background(), defaultAdapter, "chat-prov:gpt-5", 200000, settings)
	if gotAdapter.Provider != defaultAdapter.Provider {
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
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error) {
			return &fakeVisionAdapter{description: "compaction-adapter"}, nil
		},
	}
	defaultAdapter := stubProviderContext(&fakeVisionAdapter{description: "default"})
	settings := domain.Settings{CompactionModel: "cheap-prov:haiku"}
	gotAdapter, gotModel, gotWindow := app.resolveCompactionAdapter(context.Background(), defaultAdapter, "chat-prov:gpt-5", 200000, settings)
	if gotAdapter.Provider == defaultAdapter.Provider {
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
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error) {
			return &fakeVisionAdapter{description: "default"}, nil
		},
	}
	defaultAdapter := stubProviderContext(&fakeVisionAdapter{description: "default"})
	settings := domain.Settings{CompactionModel: "no-such-prov:no-such-model"}
	gotAdapter, gotModel, gotWindow := app.resolveCompactionAdapter(context.Background(), defaultAdapter, "chat-prov:gpt-5", 200000, settings)
	if gotAdapter.Provider != defaultAdapter.Provider {
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

// TestResolveContextWindowLearnedCap verifies that a cap_context learned
// from a provider 400 overflow error overrides the catalog value.
func TestResolveContextWindowLearnedCap(t *testing.T) {
	store := &fakeLearnedParamStore{}
	cache := learnedparams.New(store)
	cache.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`Requested token count exceeds the model's maximum context length of 262144 tokens.`)

	provider := &domain.Provider{ID: "tokenrouter", Models: []domain.Model{
		{ID: "qwen/qwen3.8-max-free", Context: 1_000_000},
	}}
	settings := domain.Settings{MaxInputTokens: 200_000}
	app := &App{learnedParams: cache}

	if got := app.resolveContextWindow(provider, "qwen/qwen3.8-max-free", settings); got != 262144 {
		t.Fatalf("learned cap should override catalog: got %d, want 262144", got)
	}
	// A larger cap observed later does not erase the smaller cap. The
	// registry keeps the smallest cap, so the 262144 limit stays in effect.
	cache.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`This model's maximum context length is 2_000_000 tokens.`)
	if got := app.resolveContextWindow(provider, "qwen/qwen3.8-max-free", settings); got != 262144 {
		t.Fatalf("larger cap must not erase smaller cap: got %d, want 262144", got)
	}

	// A cap larger than the catalog value is ignored entirely.
	bigCache := learnedparams.New(&fakeLearnedParamStore{})
	bigCache.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`This model's maximum context length is 2_000_000 tokens.`)
	bigApp := &App{learnedParams: bigCache}
	if got := bigApp.resolveContextWindow(provider, "qwen/qwen3.8-max-free", settings); got != 1_000_000 {
		t.Fatalf("larger learned cap must not override catalog: got %d, want 1000000", got)
	}
	// Unknown model still falls back to settings, ignoring unrelated learned cap.
	if got := app.resolveContextWindow(provider, "unknown", settings); got != 200_000 {
		t.Fatalf("unknown model context = %d, want fallback 200000", got)
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
	if err := app.executeTurnTools(run, "m1", conv.Messages[0].ToolCalls, ModelCapabilities{Vision: true}, domain.Settings{}, 1); err == nil {
		t.Fatal("want context error")
	}
	if len(box.names) != 0 {
		t.Fatalf("executed tools %v, want none after cancel", box.names)
	}
	if conv.Messages[0].ToolCalls[0].Status != domain.ToolInterrupted || conv.Messages[0].ToolCalls[1].Status != domain.ToolInterrupted {
		t.Fatalf("tool statuses = %+v", conv.Messages[0].ToolCalls)
	}
}

// briefMutatingToolbox executes the todo tool by writing a new brief to the
// injected todo port, simulating the real toolbox's SetBrief side effect.
type briefMutatingToolbox struct {
	todos    *fakeTodoPort
	newBrief string
	clearIt  bool
}

func (b *briefMutatingToolbox) ListTools() []ToolInfo { return nil }
func (b *briefMutatingToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	if name == "todo" {
		if b.clearIt {
			_ = b.todos.ClearBrief(ConversationIDFromContext(ctx))
		} else {
			b.todos.SetBrief(ConversationIDFromContext(ctx), b.newBrief)
		}
	}
	return "ok", nil
}

// hydrationCheckpointMessage builds a synthetic hydration checkpoint message:
// an assistant message carrying only hydration tool calls (IDs prefixed
// "hydrate-") and no content/reasoning — the exact shape
// FilterHydrationDomainMessages strips.
func hydrationCheckpointMessage() domain.Message {
	return domain.Message{
		ID:   "hydration-msg",
		Role: domain.RoleAssistant,
		ToolCalls: []domain.ToolCall{
			{ID: "hydrate-runtime_context", Name: "runtime_context", Status: domain.ToolOK, Output: "{}"},
			{ID: "hydrate-todo_list", Name: "todo_list", Status: domain.ToolOK, Output: "brief"},
		},
	}
}

// TestExecuteTurnToolsKeepsHydrationOnItemOnlyPatch verifies that a todo call
// that only patches items (brief unchanged) does NOT strip the hydration
// checkpoint — the checkpoint's todo_list brief is still accurate. With the
// cache-poison fix, the checkpoint is never stripped on a brief change either
// (see TestExecuteTurnToolsKeepsHydrationOnBriefChange in
// hydration_position_test.go), so the item-only case is the same invariant.
func TestExecuteTurnToolsKeepsHydrationOnItemOnlyPatch(t *testing.T) {
	todos := &fakeTodoPort{
		briefs: map[string]string{"c1": "stable brief"},
		items:  map[string][]domain.TodoItem{"c1": {{ID: "a", Content: "task", Status: domain.TodoPending}}},
	}
	// A toolbox that patches an item status but never touches the brief.
	box := &itemPatchToolbox{todos: todos}
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			hydrationCheckpointMessage(),
			{ID: "m1", Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{ID: "t1", Name: "todo", Args: `{}`}}},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       box,
		Todos:         todos,
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: WithConversationID(context.Background(), "c1"), Cancel: func() {}}

	if err := app.executeTurnTools(run, "m1", conv.Messages[1].ToolCalls, ModelCapabilities{Vision: true}, domain.Settings{}, 1); err != nil {
		t.Fatalf("executeTurnTools: %v", err)
	}
	// The hydration checkpoint must still be present.
	found := false
	for _, m := range conv.Messages {
		for _, tc := range m.ToolCalls {
			if domain.IsHydrationCallID(tc.ID) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("hydration checkpoint was stripped even though the brief did not change")
	}
}

// itemPatchToolbox executes the todo tool by patching an item status only,
// leaving the brief untouched.
type itemPatchToolbox struct {
	todos *fakeTodoPort
}

func (b *itemPatchToolbox) ListTools() []ToolInfo { return nil }
func (b *itemPatchToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	if name == "todo" {
		b.todos.Patch(ConversationIDFromContext(ctx), []domain.TodoItem{{ID: "a", Status: domain.TodoCompleted}})
	}
	return "ok", nil
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
	res := app.runOneTool(run, "m1", domain.ToolCall{ID: "t1", Name: "exec", Args: `{}`}, ModelCapabilities{}, domain.Settings{}, 1)
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
// stages one round-delta frame per output chunk into the round stream
// registry (SSE /stream) during tool execution, with ordered seq numbers,
// and that the final result is preserved.
func TestRunOneToolStreamsDeltas(t *testing.T) {
	reg := NewRoundStreamRegistry()
	app := &App{RoundStreams: reg, Bus: NewBus(), Toolbox: &streamedRecordingToolbox{recordingToolbox: &recordingToolbox{}}}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	res := app.runOneTool(run, "m1", domain.ToolCall{ID: "t1", Name: "exec", Args: `{"command":"echo hi"}`}, ModelCapabilities{}, domain.Settings{}, 1)
	if res.output != "streamed-ok" {
		t.Fatalf("output = %q", res.output)
	}

	sub, err := reg.Subscribe(context.Background(), "r1", "m1", 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()
	var frames []contracts.RoundDeltaFrame
	for {
		select {
		case f := <-sub.Frames():
			frames = append(frames, f)
			if len(frames) == 2 {
				goto collected
			}
		case <-time.After(2 * time.Second):
			t.Fatal("delta stream never delivered both frames")
		}
	}
collected:
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	for _, f := range frames {
		if f.Kind != contracts.RoundDeltaTool || f.ToolCallID != "t1" || f.Name != "exec" {
			t.Fatalf("bad frame metadata: %+v", f)
		}
	}
	if frames[0].Text != "line-1\n" || frames[1].Text != "line-2\n" {
		t.Fatalf("frame text order wrong: %+v", frames)
	}
	if frames[0].Seq != 1 || frames[1].Seq != 2 {
		t.Fatalf("frame seq order wrong: %+v", frames)
	}
}

// TestRunOneToolNoStreamFallback verifies toolboxes without the streaming
// capability still execute normally and stage no tool deltas.
func TestRunOneToolNoStreamFallback(t *testing.T) {
	reg := NewRoundStreamRegistry()
	app := &App{RoundStreams: reg, Bus: NewBus(), Toolbox: &recordingToolbox{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	res := app.runOneTool(run, "m1", domain.ToolCall{ID: "t1", Name: "exec", Args: `{"command":"echo hi"}`}, ModelCapabilities{}, domain.Settings{}, 1)
	if res.output != "ok" {
		t.Fatalf("output = %q", res.output)
	}
	// Streams are created lazily on publish; a non-streaming toolbox must
	// not have produced one.
	if reg.Exists("r1", "m1") {
		t.Fatal("expected no round stream for a non-streaming tool")
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
	gotError := false
	for i := 0; i < 8; i++ {
		select {
		case ev := <-events:
			if ev.Type == contracts.EventSteerCancelled {
				gotCancel = true
				var payload contracts.SteerEvent
				if err := json.Unmarshal(ev.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Text != "wait" {
					t.Fatalf("cancelled text = %q, want wait", payload.Text)
				}
				if payload.Reason != contracts.SteerCancelReasonDiscarded {
					t.Fatalf("cancelled reason = %q, want discarded", payload.Reason)
				}
			}
			if ev.Type == contracts.EventTurnError {
				gotError = true
				var payload contracts.TurnErrorEvent
				if err := json.Unmarshal(ev.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.MessageID != "m1" {
					t.Fatalf("error message id = %q, want m1", payload.MessageID)
				}
			}
		default:
			i = 8
		}
	}
	if !gotCancel {
		t.Fatal("expected steer cancelled event")
	}
	if !gotError {
		t.Fatal("expected turn error event")
	}
}

func TestSteerHeadlessTurnAppliesUserMessage(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Bus:           NewBus(),
		runs:          map[string]*TurnRun{},
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background(), Cancel: func() {}, Headless: true}
	app.runs[run.ID] = run

	if err := app.SteerHeadlessTurn("c1", "  focus on tests  "); err != nil {
		t.Fatal(err)
	}
	applied, err := app.applyQueuedSteer(run)
	if err != nil || !applied {
		t.Fatalf("apply: applied=%v err=%v", applied, err)
	}
	saved, err := app.Conversations.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 1 {
		t.Fatalf("messages=%d, want 1", len(saved.Messages))
	}
	m := saved.Messages[0]
	if m.Role != domain.RoleUser || !m.Steer || m.Content != "focus on tests" {
		t.Fatalf("persisted message = %+v", m)
	}
	msgs := chatMessages(saved, "", ModelCapabilities{})
	if len(msgs) != 1 || msgs[0].Role != "user" || msgs[0].Content != "focus on tests" {
		t.Fatalf("provider messages = %+v", msgs)
	}
}

func TestApplyQueuedSteerRequeuesOnSaveFailure(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}, saveErr: errors.New("disk full")}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}
	msg := domain.Message{ID: "m-steer", Role: domain.RoleUser, Content: "wait", Status: domain.StatusDone, Steer: true}
	if !run.queueSteer(&SteerEntry{ID: "s1", Text: "wait", Status: "queued", Message: msg}) {
		t.Fatal("queue steer")
	}
	applied, err := app.applyQueuedSteer(run)
	if err == nil || applied {
		t.Fatalf("apply: applied=%v err=%v, want persist error", applied, err)
	}
	queued := run.queuedSteer()
	if queued == nil || queued.Text != "wait" || queued.Message.Content != "wait" {
		t.Fatalf("steer should be requeued after save failure, got %+v", queued)
	}
}

func TestCancelSteerEmitsTextAndUserReason(t *testing.T) {
	app := &App{Bus: NewBus(), runs: map[string]*TurnRun{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}
	app.runs[run.ID] = run
	if !run.queueSteer(&SteerEntry{ID: "s1", Text: "hold on", Status: "queued"}) {
		t.Fatal("queue steer")
	}
	_, events, unsub := app.Bus.Subscribe()
	defer unsub()

	got, rpcErr := app.handleTurnsCancelSteer(contracts.TurnCancelSteerRequest{ConversationID: "c1"})
	if rpcErr != nil {
		t.Fatalf("cancel: %v", rpcErr)
	}
	if got == nil {
		t.Fatal("expected cancel result")
	}

	select {
	case ev := <-events:
		if ev.Type != contracts.EventSteerCancelled {
			t.Fatalf("event type = %s", ev.Type)
		}
		var payload contracts.SteerEvent
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Text != "hold on" {
			t.Fatalf("text = %q", payload.Text)
		}
		if payload.Reason != contracts.SteerCancelReasonUser {
			t.Fatalf("reason = %q, want user", payload.Reason)
		}
	default:
		t.Fatal("expected steer cancelled event")
	}
}

func TestSteerAllowsAttachmentsWithoutText(t *testing.T) {
	app := &App{Bus: NewBus(), runs: map[string]*TurnRun{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}
	app.runs[run.ID] = run

	_, rpcErr := app.handleTurnsSteer(context.Background(), contracts.TurnSteerRequest{
		ConversationID: "c1",
		Text:           "   ",
		Attachments:    []contracts.AttachmentDTO{{Type: "text", Name: "note.txt", MediaType: "text/plain", Content: "see this"}},
	})
	if rpcErr != nil {
		t.Fatalf("steer: %v", rpcErr)
	}
	queued := run.queuedSteer()
	if queued == nil {
		t.Fatal("expected queued steer")
	}
	if queued.Message.Role != domain.RoleUser || !queued.Message.Steer {
		t.Fatalf("message = %+v", queued.Message)
	}
	if len(queued.Message.Attachments) != 1 || queued.Message.Attachments[0].Name != "note.txt" {
		t.Fatalf("attachments = %+v", queued.Message.Attachments)
	}
}

func TestTurnsActiveIncludesQueuedSteer(t *testing.T) {
	app := &App{runs: map[string]*TurnRun{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1", MessageID: "m1"}
	app.runs[run.ID] = run
	if !run.queueSteer(&SteerEntry{ID: "s1", Text: "nudge", Status: "queued"}) {
		t.Fatal("queue steer")
	}
	run.setMessageID("m2")
	got, rpcErr := app.handleTurnsActive(contracts.ConversationIDRequest{ID: "c1"})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	res, ok := got.(contracts.TurnActiveResult)
	if !ok {
		t.Fatalf("result type %T", got)
	}
	if !res.Active || res.MessageID != "m2" || res.QueuedSteer != "nudge" || res.QueuedSteerID != "s1" {
		t.Fatalf("active result = %+v", res)
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
	secondReq *core.Request
	streamErr error
	summary   string
}

func (a *overflowThenOKAdapter) Name() string { return "overflow-then-ok" }
func (a *overflowThenOKAdapter) Stream(_ context.Context, req *core.Request) (core.Stream, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streams++
	if a.streams == 1 {
		err := a.streamErr
		if err == nil {
			err = &UpstreamError{StatusCode: 400, Err: errors.New("maximum context length exceeded")}
		}
		return nil, err
	}
	a.secondReq = req
	resp := &core.Response{Blocks: []core.Block{core.TextBlock{Text: "ok after compact"}}, FinishReason: core.FinishReasonStop}
	return &stubStream{events: coreResponseEvents(resp)}, nil
}
func (a *overflowThenOKAdapter) Chat(context.Context, *core.Request) (*core.Response, error) {
	a.mu.Lock()
	a.completes++
	a.mu.Unlock()
	summary := a.summary
	if summary == "" {
		summary = validTestSummary
	}
	return &core.Response{Blocks: []core.Block{core.TextBlock{Text: summary}}, FinishReason: core.FinishReasonStop}, nil
}

// coreHasHydration checks core.Message slices for hydration tool calls,
// mirroring HasHydration but for the core.Request message format.
func coreHasHydration(messages []core.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != core.RoleAssistant {
			continue
		}
		allHydration := true
		expected := map[string]bool{}
		for _, b := range m.Blocks {
			if tc, ok := b.(core.ToolUseBlock); ok {
				if !domain.IsHydrationCallID(tc.ID) {
					allHydration = false
					break
				}
				expected[tc.ID] = false
			}
		}
		if !allHydration || len(expected) == 0 {
			continue
		}
		for j := i + 1; j < len(messages); j++ {
			t := messages[j]
			if t.Role != core.RoleTool {
				break
			}
			for _, b := range t.Blocks {
				if tr, ok := b.(core.ToolResultBlock); ok {
					if _, ok2 := expected[tr.ToolUseID]; ok2 {
						expected[tr.ToolUseID] = true
					}
				}
			}
		}
		allMatched := true
		for _, matched := range expected {
			if !matched {
				allMatched = false
				break
			}
		}
		if allMatched {
			return true
		}
	}
	return false
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
		Factory: func(context.Context, *domain.Provider, string) (core.Provider, error) {
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
	if !coreHasHydration(adapter.secondReq.Messages) {
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
		Factory: func(context.Context, *domain.Provider, string) (core.Provider, error) {
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

func (a *midTurnCompactionAdapter) Name() string { return "mid-turn-compaction" }
func (a *midTurnCompactionAdapter) Stream(_ context.Context, _ *core.Request) (core.Stream, error) {
	a.mu.Lock()
	a.streams++
	n := a.streams
	a.mu.Unlock()
	var resp *core.Response
	if n == 1 {
		resp = &core.Response{
			Blocks: []core.Block{
				core.TextBlock{Text: "let me read that file"},
				core.ToolUseBlock{ID: "call_1", Name: "read_file", Arguments: jsonRaw(`{"path":"/big"}`)},
			},
			FinishReason: core.FinishReasonToolCall,
		}
	} else {
		resp = &core.Response{Blocks: []core.Block{core.TextBlock{Text: "done after compaction"}}, FinishReason: core.FinishReasonStop}
	}
	return &stubStream{events: coreResponseEvents(resp)}, nil
}
func (a *midTurnCompactionAdapter) Chat(_ context.Context, _ *core.Request) (*core.Response, error) {
	a.mu.Lock()
	a.completes++
	a.mu.Unlock()
	return &core.Response{Blocks: []core.Block{core.TextBlock{Text: validTestSummary}}, FinishReason: core.FinishReasonStop}, nil
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
		Factory: func(context.Context, *domain.Provider, string) (core.Provider, error) {
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
	requests          []*core.Request
	summaries         []string
	toolCallSummaries []string // if set, return summary() tool call instead of Content
}

func (a *recordingCompleteAdapter) Name() string { return "recording-complete" }
func (a *recordingCompleteAdapter) Stream(context.Context, *core.Request) (core.Stream, error) {
	return nil, errors.New("stream not used")
}
func (a *recordingCompleteAdapter) Chat(_ context.Context, req *core.Request) (*core.Response, error) {
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
	if idx < len(a.toolCallSummaries) || (len(a.toolCallSummaries) > 0 && idx >= len(a.toolCallSummaries)) {
		tcSummary := a.toolCallSummaries[min(idx, len(a.toolCallSummaries)-1)]
		return &core.Response{
			Blocks: []core.Block{core.ToolUseBlock{
				ID:        fmt.Sprintf("call_%d", idx),
				Name:      compactionSummaryToolName,
				Arguments: jsonRaw(fmt.Sprintf(`{"text":%q}`, tcSummary)),
			}},
			FinishReason: core.FinishReasonToolCall,
		}, nil
	}
	return &core.Response{Blocks: []core.Block{core.TextBlock{Text: summary}}, FinishReason: core.FinishReasonStop}, nil
}

// coreMessageText extracts text content from a core.Message's TextBlocks.
func coreMessageText(m core.Message) string {
	var out string
	for _, b := range m.Blocks {
		if tb, ok := b.(core.TextBlock); ok {
			out += tb.Text
		}
	}
	return out
}

// coreRequestMaxTokens returns the MaxTokens value from a core.Request (0 if nil).
func coreRequestMaxTokens(req *core.Request) int {
	if req == nil || req.MaxTokens == nil {
		return 0
	}
	return *req.MaxTokens
}

// coreBlocksText extracts all text from a slice of core.Block.
func coreBlocksText(blocks []core.Block) string {
	var out string
	for _, b := range blocks {
		if tb, ok := b.(core.TextBlock); ok {
			out += tb.Text
		}
	}
	return out
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
	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings)
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
	if req.ToolChoice == nil {
		t.Fatal("compaction request did not force tool_choice onto summary()")
	}
	assertCompactionRequestEndsWithUserHandoff(t, req)
}

// TestCompactionRequestEndsWithUserHandoffAfterToolRound: a compaction
// chunk that ends on an open agent tool round (assistant + tool result)
// must still close the provider request with a user-role handoff command.
// Leaving tool as the last role makes reasoning models continue the task
// instead of calling summary() — the Nemotron 3 Ultra failure on
// conv_e27858651cd4e5ee.
func TestCompactionRequestEndsWithUserHandoffAfterToolRound(t *testing.T) {
	toolOut := strings.Repeat("tool-output-line\n", 80)
	keepFiller := strings.Repeat("keep-me-recent-user-message-", 40)
	msgs := []domain.Message{
		{ID: "u0", Role: domain.RoleUser, Content: "start the work", Status: domain.StatusDone},
		{ID: "a0", Role: domain.RoleAssistant, Content: "reading the review agent", Status: domain.StatusDone, ToolCalls: []domain.ToolCall{
			{ID: "call_read", Name: "file_read", Args: `{"path":"application/learning_review_agent.go"}`, Output: toolOut},
		}},
	}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("keep%d", i), Role: domain.RoleUser, Content: keepFiller, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c-tool", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c-tool": conv}}
	adapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) == 0 {
		t.Fatal("no Complete calls")
	}
	req := adapter.requests[0]
	assertCompactionRequestEndsWithUserHandoff(t, req)
	foundTool := false
	for _, m := range req.Messages {
		if m.Role == core.RoleTool {
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Fatal("expected the file_read tool result in the compaction transcript")
	}
}

func TestCompactionRequestClonesPrefixThenAppendsHandoff(t *testing.T) {
	// Clone the prefix in array order, then append the user closer.
	// Do not regroup users then assistants and do not sort by CreatedAt.
	keepFiller := strings.Repeat("keep-me-recent-user-message-", 40)
	msgs := []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "question-one-unique", Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: "answer-one-unique", Status: domain.StatusDone, ToolCalls: []domain.ToolCall{
			{ID: "call_one", Name: "file_read", Args: `{"path":"a.go"}`, Output: strings.Repeat("tool-one-output\n", 40)},
		}},
		{ID: "u2", Role: domain.RoleUser, Content: "question-two-unique", Status: domain.StatusDone},
		{ID: "a2", Role: domain.RoleAssistant, Content: "answer-two-unique", Status: domain.StatusDone},
	}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("keep%d", i), Role: domain.RoleUser, Content: keepFiller, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c-order", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c-order": conv}}
	adapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) == 0 {
		t.Fatal("no Complete calls")
	}
	req := adapter.requests[0]
	assertCompactionRequestEndsWithUserHandoff(t, req)
	var seen []string
	for _, m := range req.Messages {
		text := coreMessageText(m)
		switch {
		case strings.Contains(text, "question-one-unique"):
			seen = append(seen, "q1")
		case strings.Contains(text, "answer-one-unique"):
			seen = append(seen, "a1")
		case strings.Contains(text, "question-two-unique"):
			seen = append(seen, "q2")
		case strings.Contains(text, "answer-two-unique"):
			seen = append(seen, "a2")
		}
	}
	want := []string{"q1", "a1", "q2", "a2"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("handoff transcript order = %v, want cloned turn order %v", seen, want)
	}
}

func TestAppendCompactionHandoffUserClosesToolRound(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "assistant", Content: "reading", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "file_read", Args: `{}`}}},
		{Role: "tool", ToolResult: &ToolResult{ToolCallID: "c1", Name: "file_read", Content: "package application"}},
	}
	got := appendCompactionHandoffUser(msgs)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (transcript + closer)", len(got))
	}
	if got[1].Role != "tool" {
		t.Fatalf("transcript tool result dropped, last-but-one role = %s", got[1].Role)
	}
	if got[2].Role != "user" {
		t.Fatalf("last role = %s, want user", got[2].Role)
	}
	if !strings.Contains(got[2].Content, "Call the summary tool exactly once") {
		t.Fatalf("closer missing handoff command: %q", got[2].Content)
	}
}

func assertCompactionRequestEndsWithUserHandoff(t *testing.T, req *core.Request) {
	t.Helper()
	if req == nil || len(req.Messages) == 0 {
		t.Fatal("empty compaction request")
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != core.RoleUser {
		t.Fatalf("compaction request last role = %s, want user (handoff command must be last)", last.Role)
	}
	text := coreMessageText(last)
	if !strings.Contains(text, "Call the summary tool exactly once") {
		t.Fatalf("last user message is not the handoff command: %q", text)
	}
}

func TestCompactionToolChoiceForChatKind(t *testing.T) {
	got := compactionToolChoice(domain.ProviderChat)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("chat tool_choice = %#v", got)
	}
	fn, _ := m["function"].(map[string]any)
	if m["type"] != "function" || fn["name"] != compactionSummaryToolName {
		t.Fatalf("chat tool_choice = %#v", got)
	}
	got = compactionToolChoice(domain.ProviderMessages)
	m, ok = got.(map[string]any)
	if !ok || m["type"] != "tool" || m["name"] != compactionSummaryToolName {
		t.Fatalf("messages tool_choice = %#v", got)
	}
}

// TestCompactionToolChoiceForResponsesKind: the OpenAI Responses API uses the
// flat tool_choice shape {"type":"function","name":"summary"} (no nested
// "function" object). The nested Chat shape triggers
// "missing_required_parameter: 'tool_choice.name'" on gpt-5 responses models,
// which silently kills every client-side compaction pass.
func TestCompactionToolChoiceForResponsesKind(t *testing.T) {
	got := compactionToolChoice(domain.ProviderResponses)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("responses tool_choice = %#v", got)
	}
	if m["type"] != "function" {
		t.Fatalf("responses tool_choice type = %#v, want \"function\"", m["type"])
	}
	if name, _ := m["name"].(string); name != compactionSummaryToolName {
		t.Fatalf("responses tool_choice name = %#v, want %q", m["name"], compactionSummaryToolName)
	}
	if _, hasNested := m["function"]; hasNested {
		t.Fatalf("responses tool_choice must not nest \"function\" (Chat shape); got %#v", got)
	}
}

func TestCompactionSummaryEchoesAssistant(t *testing.T) {
	asst := strings.Repeat("Build hijau. Sekarang aku verifikasi dua konsumen kunci. ", 3)
	msgs := []ChatMessage{{Role: "user", Content: "go"}, {Role: "assistant", Content: asst}}
	if !compactionSummaryEchoesAssistant(asst+" extra", msgs) {
		t.Fatal("expected echo of latest assistant content")
	}
	if compactionSummaryEchoesAssistant("## Goal\nfix ordering\n## Done\nfound the Compact regroup bug", msgs) {
		t.Fatal("structured handoff must not be treated as an echo")
	}
}

func TestReasoningDeltaVisibleSkipsLeadingWhitespace(t *testing.T) {
	if reasoningDeltaVisible(" \n\t") {
		t.Fatal("leading whitespace-only reasoning must not be emitted")
	}
	if !reasoningDeltaVisible("ok") {
		t.Fatal("visible reasoning must be emitted")
	}
	if !reasoningDeltaVisible("ok\n") {
		t.Fatal("whitespace after visible text must still emit")
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
	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings)
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
	_, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings)
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
	_, _ = app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 200000, settings)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	expectedBudget := 800
	for i, req := range adapter.requests {
		if coreRequestMaxTokens(req) != expectedBudget {
			t.Fatalf("attempt %d: MaxTokens=%d, want %d", i, coreRequestMaxTokens(req), expectedBudget)
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
	_, _ = app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 2000, settings)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	maxBudget := 2000 - compactionSystemReserve
	for i, req := range adapter.requests {
		if coreRequestMaxTokens(req) > maxBudget {
			t.Fatalf("attempt %d: MaxTokens=%d exceeds maxBudget=%d", i, coreRequestMaxTokens(req), maxBudget)
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
	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 20000, settings)
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
			for _, b := range m.Blocks {
				switch b.(type) {
				case core.ImageBlock, core.AudioBlock, core.VideoBlock:
					t.Fatalf("request %d: message carries media block, want stripped", i)
				}
			}
		}
	}
	noteSeen := false
	for _, req := range adapter.requests {
		for _, m := range req.Messages {
			if strings.Contains(coreMessageText(m), "shot.png") {
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
	_, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 2000, settings)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	truncated := false
	for _, req := range adapter.requests {
		for _, m := range req.Messages {
			for _, b := range m.Blocks {
				tr, ok := b.(core.ToolResultBlock)
				if !ok {
					continue
				}
				content := coreBlocksText(tr.Content)
				if len(content) > 2200 {
					t.Fatalf("tool result not capped: %d chars", len(content))
				}
				if strings.Contains(content, "[truncated:") {
					truncated = true
				}
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
	_, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings)
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
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) < 2 {
		t.Fatalf("Complete calls = %d, want multi-pass", len(adapter.requests))
	}
	firstContent := 0
	for _, m := range adapter.requests[0].Messages {
		firstContent += domain.EstimateTokens(coreMessageText(m))
	}
	secondContent := 0
	for _, m := range adapter.requests[1].Messages {
		secondContent += domain.EstimateTokens(coreMessageText(m))
	}
	// Second pass carries the huge running summary, so remaining message
	// content must be smaller than the first pass's message payload.
	// The running-summary wrapper is detected by its stable header
	// ([COMPACTION CHECKPOINT]); the full template is not a prefix of the
	// filled version because the {{compacted_summaries}} placeholder sits
	// mid-template, so HasPrefix against the empty-filled template fails.
	const compactionWrapperHeader = "[COMPACTION CHECKPOINT]"
	firstMsgs := 0
	for _, m := range adapter.requests[0].Messages {
		text := coreMessageText(m)
		if !strings.HasPrefix(text, compactionWrapperHeader) {
			firstMsgs += domain.EstimateTokens(text)
		}
	}
	secondMsgs := 0
	for _, m := range adapter.requests[1].Messages {
		text := coreMessageText(m)
		if !strings.HasPrefix(text, compactionWrapperHeader) {
			secondMsgs += domain.EstimateTokens(text)
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
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, domain.DefaultSettings()); err != nil {
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
	// After compaction, the live transcript must have exactly ONE fresh
	// hydration checkpoint — persistCompactedConversation rebuilds it in the
	// same Save as Compact (the old one was stripped + archived). The
	// archived chunks above must not carry any (checked before this).
	checkpoints := 0
	for _, m := range got.Messages {
		if isHydrationMessage(m) {
			checkpoints++
		}
	}
	if checkpoints != 1 {
		t.Fatalf("live transcript after compaction has %d hydration checkpoints, want 1 fresh rebuild", checkpoints)
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
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) == 0 {
		t.Fatal("expected compaction Complete call")
	}
	for _, req := range adapter.requests {
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				tr, ok := b.(core.ToolResultBlock)
				if !ok {
					continue
				}
				for _, rb := range tr.Content {
					switch rb.(type) {
					case core.ImageBlock, core.AudioBlock, core.VideoBlock:
						t.Fatalf("compaction must not replay media attachments in tool results: %T", rb)
					}
				}
				if strings.Contains(coreBlocksText(tr.Content), huge) {
					t.Fatal("compaction payload still contains image data URL")
				}
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
