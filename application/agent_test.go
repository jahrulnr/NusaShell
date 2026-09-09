package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"nusashell/application/service/learnedparams"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// --- from agent_runner_test.go ---

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

func TestLastFailedAssistantIndex(t *testing.T) {
	hyd := domain.Message{
		ID: "h1", Role: domain.RoleAssistant, Status: domain.StatusDone,
		ToolCalls: []domain.ToolCall{{ID: domain.HydrateToolCallPrefix + "x_0", Name: "runtime_context"}},
	}
	failed := domain.Message{ID: "a1", Role: domain.RoleAssistant, Status: domain.StatusError, Content: "oops"}
	ok := domain.Message{ID: "a2", Role: domain.RoleAssistant, Status: domain.StatusDone, Content: "recovered"}
	user := domain.Message{ID: "u1", Role: domain.RoleUser, Status: domain.StatusDone, Content: "hi"}

	if got := lastFailedAssistantIndex([]domain.Message{user, hyd, failed}); got != 2 {
		t.Fatalf("last failed = %d, want 2", got)
	}
	if got := lastFailedAssistantIndex([]domain.Message{user, hyd, failed, ok}); got != -1 {
		t.Fatalf("after successful retry last failed = %d, want -1", got)
	}
	if got := lastFailedAssistantIndex([]domain.Message{user, ok}); got != -1 {
		t.Fatalf("successful turn last failed = %d, want -1", got)
	}
}

func TestShouldContinueFailedTurn(t *testing.T) {
	if !domain.ShouldContinueFailedTurn(domain.Message{Content: "partial"}) {
		t.Fatal("partial text without tools should continue")
	}
	if domain.ShouldContinueFailedTurn(domain.Message{Content: "partial", ToolCalls: []domain.ToolCall{{ID: "t1"}}}) {
		t.Fatal("partial text with tool calls must restart from scratch")
	}
	if domain.ShouldContinueFailedTurn(domain.Message{}) {
		t.Fatal("empty failed turn must restart from scratch")
	}
}

// TestHydrationCheckpointPersistsOnceUntilCompaction verifies the epoch-based
// hydration lifecycle: a fresh room gets one checkpoint via addTurnMessages,
// follow-up user messages do NOT add another, and persistCompactedConversation
// rebuilds exactly one fresh checkpoint after compaction. The turn loop no
// longer touches hydration, so the checkpoint stays at its epoch anchor
// (after the first user / handover) across rounds.
func TestHydrationCheckpointPersistsOnceUntilCompaction(t *testing.T) {
	conversation := &domain.Conversation{ID: "c1"}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conversation}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
	}
	countCheckpoints := func() int {
		saved, err := app.Conversations.Get("c1")
		if err != nil {
			t.Helper()
			t.Fatalf("Get: %v", err)
		}
		count := 0
		for _, message := range saved.Messages {
			if domain.IsHydrationMessage(message) {
				count++
			}
		}
		return count
	}

	app.addTurnMessages(conversation,
		domain.Message{ID: "u1", Role: domain.RoleUser, Content: "first", Status: domain.StatusDone},
		domain.Message{ID: "a1", Role: domain.RoleAssistant},
	)
	if err := bindConversation(app.Conversations, conversation).Save(); err != nil {
		t.Fatal(err)
	}
	if got := countCheckpoints(); got != 1 {
		t.Fatalf("initial checkpoints = %d, want 1", got)
	}

	// Follow-up user message: two users now, checkpoint already present.
	repo, err := app.loadRepo("c1")
	if err != nil {
		t.Fatal(err)
	}
	app.addTurnMessages(repo.Conversation(),
		domain.Message{ID: "u2", Role: domain.RoleUser, Content: "follow up", Status: domain.StatusDone},
		domain.Message{ID: "a2", Role: domain.RoleAssistant},
	)
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	if got := countCheckpoints(); got != 1 {
		t.Fatalf("checkpoints after later user message = %d, want 1", got)
	}

	conversation, err = app.Conversations.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
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
	if got := domain.CompactionTriggerTokens(1000, 0, settings); got != 800 {
		t.Fatalf("small window: got %d, want 800 (80%% of 1000)", got)
	}
	if got := domain.CompactionTriggerTokens(200000, 0, settings); got != 40000 {
		t.Fatalf("large window: got %d, want threshold 40000", got)
	}
	settings.CompactionThreshold = 5000
	if got := domain.CompactionTriggerTokens(200000, 0, settings); got != 5000 {
		t.Fatalf("custom threshold: got %d, want 5000", got)
	}
}

func TestCompactionTriggerAutoUsesWindowPercentage(t *testing.T) {
	// Threshold=0 means "auto": compaction triggers at 80% of the model's
	// available input budget (contextWindow - maxOutput), not at a flat
	// token count. This is the default for new installations and after
	// migration from the old 40k default.
	settings := domain.Settings{CompactionThreshold: 0}
	if got := domain.CompactionTriggerTokens(1048576, 0, settings); got != 838860 {
		t.Fatalf("1M window auto: got %d, want 838860 (80%% of 1M)", got)
	}
	if got := domain.CompactionTriggerTokens(200000, 0, settings); got != 160000 {
		t.Fatalf("200k window auto: got %d, want 160000 (80%% of 200k)", got)
	}
	if got := domain.CompactionTriggerTokens(1000, 0, settings); got != 800 {
		t.Fatalf("1k window auto: got %d, want 800", got)
	}
}

func TestCompactionTriggerSubtractsMaxOutput(t *testing.T) {
	// 256k window, 64k output → available = 196608 → trigger = 80% × 196608 = 157286
	settings := domain.Settings{CompactionThreshold: 0}
	if got := domain.CompactionTriggerTokens(262144, 65536, settings); got != 157286 {
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
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
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

func TestResolveCompactionAdapterCodexKeepsSameModel(t *testing.T) {
	defaultProvider := &fakeVisionAdapter{description: "codex-default"}
	defaultAdapter := ProviderContext{Provider: defaultProvider, Kind: domain.ProviderCodex}
	app := &App{}
	settings := domain.Settings{CompactionModel: "other-provider:cheap-model"}

	gotAdapter, gotModel, gotWindow := app.resolveCompactionAdapter(context.Background(), defaultAdapter, "gpt-5-codex", 400000, settings)
	if gotAdapter.Provider != defaultProvider {
		t.Fatal("Codex compaction must keep the current adapter")
	}
	if gotModel != "gpt-5-codex" || gotWindow != 400000 {
		t.Fatalf("Codex compaction = adapter=%v model=%q window=%d, want current model and window", gotAdapter.Provider, gotModel, gotWindow)
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
	if got := domain.EffectiveContextWindow(1_000_000, 0); got != 1_000_000 {
		t.Fatalf("uncapped model context = %d, want 1000000", got)
	}
	if got := domain.EffectiveContextWindow(1_000_000, 200_000); got != 1_000_000 {
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
	if res.Status != domain.ToolFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if !strings.Contains(res.Output, "line-1") {
		t.Fatalf("partial streamed output lost from persisted result: %q", res.Output)
	}
	if !strings.HasPrefix(res.Output, "error: ") {
		t.Fatalf("non-cancellation errors must still carry the error: prefix; got %q", res.Output)
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
// stages one presentation-bearing tool-start frame followed by one
// round-delta frame per output chunk into the round stream registry (SSE
// /stream), with ordered seq numbers, and that the final result is preserved.
func TestRunOneToolStreamsDeltas(t *testing.T) {
	reg := NewRoundStreamRegistry()
	app := &App{RoundStreams: reg, Bus: NewBus(), Toolbox: &streamedRecordingToolbox{recordingToolbox: &recordingToolbox{}}}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	res := app.runOneTool(run, "m1", domain.ToolCall{ID: "t1", Name: "exec", Args: `{"command":"echo hi"}`}, ModelCapabilities{}, domain.Settings{}, 1)
	if res.Output != "streamed-ok" {
		t.Fatalf("output = %q", res.Output)
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
			if len(frames) == 3 {
				goto collected
			}
		case <-time.After(2 * time.Second):
			t.Fatal("delta stream never delivered the tool start and both output frames")
		}
	}
collected:
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(frames))
	}
	for _, f := range frames {
		if f.Kind != contracts.RoundDeltaTool || f.ToolCallID != "t1" || f.Name != "exec" {
			t.Fatalf("bad frame metadata: %+v", f)
		}
	}
	if frames[0].Text != "" || frames[0].Presentation == nil || frames[0].Presentation.Variant != "terminal" {
		t.Fatalf("tool start frame should carry terminal presentation: %+v", frames[0])
	}
	if string(frames[0].Args) != `{"command":"echo hi"}` {
		t.Fatalf("tool start frame args = %q", frames[0].Args)
	}
	if len(frames[1].Args) != 0 || len(frames[2].Args) != 0 {
		t.Fatalf("streamed chunks must not repeat args: %+v", frames)
	}
	if frames[1].Text != "line-1\n" || frames[2].Text != "line-2\n" {
		t.Fatalf("frame text order wrong: %+v", frames)
	}
	if frames[1].Presentation != nil || frames[2].Presentation != nil {
		t.Fatalf("streamed chunks should not repeat the presentation: %+v", frames)
	}
	if frames[0].Seq != 1 || frames[1].Seq != 2 || frames[2].Seq != 3 {
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
	if res.Output != "ok" {
		t.Fatalf("output = %q", res.Output)
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
	if !run.QueueSteer(&SteerEntry{ID: "s1", Text: "wait", Status: "queued"}) {
		t.Fatal("queue steer")
	}
	app.failTurn(run, "m1", context.Canceled)
	if run.QueuedSteer() != nil {
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
	if !run.QueueSteer(&SteerEntry{ID: "s1", Text: "wait", Status: "queued", Message: msg}) {
		t.Fatal("queue steer")
	}
	applied, err := app.applyQueuedSteer(run)
	if err == nil || applied {
		t.Fatalf("apply: applied=%v err=%v, want persist error", applied, err)
	}
	queued := run.QueuedSteer()
	if queued == nil || queued.Text != "wait" || queued.Message.Content != "wait" {
		t.Fatalf("steer should be requeued after save failure, got %+v", queued)
	}
}

func TestCancelSteerEmitsTextAndUserReason(t *testing.T) {
	app := &App{Bus: NewBus(), runs: map[string]*TurnRun{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}
	app.runs[run.ID] = run
	if !run.QueueSteer(&SteerEntry{ID: "s1", Text: "hold on", Status: "queued"}) {
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
	queued := run.QueuedSteer()
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
	if !run.QueueSteer(&SteerEntry{ID: "s1", Text: "nudge", Status: "queued"}) {
		t.Fatal("queue steer")
	}
	run.SetMessageID("m2")
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
	g := &repeatedToolGuard{Limit: limit}
	tools := []domain.ToolCall{
		{Name: "mcp_enable", Args: `{"id":"nusashell.files"}`},
		{Name: "mcp_enable", Args: `{"id":"nusashell.terminal"}`},
		{Name: "mcp_enable", Args: `{"id":"nusashell.kanban"}`},
		{Name: "mcp_enable", Args: `{"id":"nusashell.notes"}`},
		{Name: "tool_schema", Args: `{"server":"Files","tool":"read"}`},
		{Name: "tool_schema", Args: `{"server":"Terminal","tool":"exec"}`},
	}

	for i := 1; i <= limit-1; i++ {
		if fired := g.Check(tools, ""); fired {
			t.Fatalf("round %d: guard fired too early (limit=%d)", i, limit)
		}
	}
	if !g.Check(tools, "") {
		t.Fatal("guard should fire on the limit-th identical parallel round")
	}
	// After firing, the guard resets — a new identical round should not fire immediately.
	if fired := g.Check(tools, ""); fired {
		t.Fatal("guard should reset after firing, not fire again immediately")
	}
}

func TestRepeatedToolGuardResetsOnContent(t *testing.T) {
	g := &repeatedToolGuard{Limit: 3}
	tools := []domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"x"}`}}

	for i := 1; i <= 2; i++ {
		g.Check(tools, "")
	}
	// A round with text content resets the streak.
	g.Check(tools, "here is my answer")
	if fired := g.Check(tools, ""); fired {
		t.Fatal("guard should reset after a round with content, not fire")
	}
}

func TestRepeatedToolGuardDifferentArgsResets(t *testing.T) {
	g := &repeatedToolGuard{Limit: 3}
	g.Check([]domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"a"}`}}, "")
	g.Check([]domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"a"}`}}, "")
	// Different args → new signature → streak resets.
	g.Check([]domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"b"}`}}, "")
	if fired := g.Check([]domain.ToolCall{{Name: "mcp_enable", Args: `{"id":"b"}`}}, ""); fired {
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
			err = &domain.ProviderError{StatusCode: 400, Err: errors.New("maximum context length exceeded")}
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
	// Locate the turn's assistant message by ID after Save cloned the
	// transcript; the original fixture pointer is stale.
	saved, err := app.Conversations.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
	var asst *domain.Message
	for i := range saved.Messages {
		if saved.Messages[i].ID == "m1" {
			asst = &saved.Messages[i]
			break
		}
	}
	if asst == nil {
		t.Fatalf("assistant message m1 missing from transcript (%d messages)", len(saved.Messages))
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
	mu            sync.Mutex
	streams       int
	completes     int
	overflowed    bool
	compactionErr error
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
	compactionErr := a.compactionErr
	a.mu.Unlock()
	if compactionErr != nil {
		return nil, compactionErr
	}
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

func TestMidTurnCompactionFailureStopsBeforeNextRound(t *testing.T) {
	conv := &domain.Conversation{ID: "c1", Messages: []domain.Message{
		{ID: "u0", Role: domain.RoleUser, Content: strings.Repeat("goal ", 100), Status: domain.StatusDone},
		{ID: "m1", Role: domain.RoleAssistant},
	}}
	adapter := &midTurnCompactionAdapter{compactionErr: errors.New("read udp 127.0.0.1:51574->127.0.0.53:53: i/o timeout")}
	toolbox := &largeOutputToolbox{output: strings.Repeat("tool result line of content. ", 500)}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	settings.CompactionThreshold = 0
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

	adapter.mu.Lock()
	streams := adapter.streams
	adapter.mu.Unlock()
	if streams != 1 {
		t.Fatalf("streams = %d, want 1: failed compaction must stop the run before the next provider round", streams)
	}
	toolbox.mu.Lock()
	toolCalls := len(toolbox.names)
	toolbox.mu.Unlock()
	if toolCalls != 1 {
		t.Fatalf("tool calls = %d, want 1 from the completed first round only", toolCalls)
	}
	saved, err := app.Conversations.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
	var userIDs []string
	for _, message := range saved.Messages {
		if message.Role == domain.RoleUser {
			userIDs = append(userIDs, message.ID)
		}
	}
	if len(userIDs) != 1 || userIDs[0] != "u0" {
		t.Fatalf("user history after failed compaction = %v, want the unchanged ordered user history", userIDs)
	}
}

type recordingCompleteAdapter struct {
	mu                sync.Mutex
	requests          []*core.Request
	summaries         []string
	toolCallSummaries []string // if set, return summary() tool call instead of Content
	err               error
}

func (a *recordingCompleteAdapter) Name() string { return "recording-complete" }
func (a *recordingCompleteAdapter) Stream(context.Context, *core.Request) (core.Stream, error) {
	return nil, errors.New("stream not used")
}
func (a *recordingCompleteAdapter) Chat(_ context.Context, req *core.Request) (*core.Response, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, req)
	if a.err != nil {
		return nil, a.err
	}
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

type codexCompactionProvider struct {
	streamRequests []*core.Request
	streamEvents   []core.Event
	streamErr      error
	chatCalls      int
}

func (p *codexCompactionProvider) Name() string { return "codex-test" }

func (p *codexCompactionProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	p.chatCalls++
	return nil, errors.New("codex compaction must use streaming")
}

func (p *codexCompactionProvider) Stream(_ context.Context, req *core.Request) (core.Stream, error) {
	p.streamRequests = append(p.streamRequests, req)
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	return &stubStream{events: p.streamEvents}, nil
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
	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings, domain.CompactionTriggerInitial)
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
			{ID: "call_read", Name: "file_read", Args: `{"path":"application/learning_job.go"}`, Output: toolOut},
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
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings, domain.CompactionTriggerInitial); err != nil {
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
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings, domain.CompactionTriggerInitial); err != nil {
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
	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings, domain.CompactionTriggerInitial)
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
	_, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings, domain.CompactionTriggerInitial)
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
	_, _ = app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 200000, settings, domain.CompactionTriggerInitial)
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
	_, _ = app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 2000, settings, domain.CompactionTriggerInitial)
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
	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 20000, settings, domain.CompactionTriggerInitial)
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
	_, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 2000, settings, domain.CompactionTriggerInitial)
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
	_, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings, domain.CompactionTriggerInitial)
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
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, settings, domain.CompactionTriggerInitial); err != nil {
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
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, domain.DefaultSettings(), domain.CompactionTriggerInitial); err != nil {
		t.Fatal(err)
	}
	for _, m := range store.archived {
		if domain.IsHydrationMessage(m) {
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
		if domain.IsHydrationMessage(m) {
			checkpoints++
		}
	}
	if checkpoints != 1 {
		t.Fatalf("live transcript after compaction has %d hydration checkpoints, want 1 fresh rebuild", checkpoints)
	}
}

func TestPersistCompactedConversationPreservesTranscriptWhenArchiveFails(t *testing.T) {
	messages := []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: strings.Repeat("old question ", 100), Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: strings.Repeat("old answer ", 100), Status: domain.StatusDone},
		{ID: "u2", Role: domain.RoleUser, Content: "latest question", Status: domain.StatusDone},
	}
	conv := &domain.Conversation{ID: "c-archive-fail", Messages: append([]domain.Message(nil), messages...)}
	store := &fakeConvStore{
		convs:      map[string]*domain.Conversation{conv.ID: conv},
		archiveErr: errors.New("disk full"),
	}
	app := &App{Conversations: store, Bus: NewBus()}

	err := app.persistCompactedConversation(conv, "summary", 1)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("persistCompactedConversation error = %v, want archive failure", err)
	}
	if len(conv.Messages) != len(messages) {
		t.Fatalf("messages = %d, want original %d", len(conv.Messages), len(messages))
	}
	for i := range messages {
		if conv.Messages[i].ID != messages[i].ID {
			t.Fatalf("message[%d] = %q, want %q", i, conv.Messages[i].ID, messages[i].ID)
		}
	}
	if conv.CompactionBlob != "" || conv.Summary != "" {
		t.Fatalf("compaction state changed after archive failure: blob=%q summary=%q", conv.CompactionBlob, conv.Summary)
	}
}

func TestPersistCodexCompactedConversationPreservesTranscriptWhenArchiveFails(t *testing.T) {
	messages := []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "old question", Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: strings.Repeat("old answer ", 100), Status: domain.StatusDone},
		{ID: "u2", Role: domain.RoleUser, Content: "latest question", Status: domain.StatusDone},
	}
	conv := &domain.Conversation{ID: "c-codex-archive-fail", Messages: append([]domain.Message(nil), messages...)}
	store := &fakeConvStore{
		convs:      map[string]*domain.Conversation{conv.ID: conv},
		archiveErr: errors.New("disk full"),
	}
	app := &App{Conversations: store, Bus: NewBus()}

	err := app.agentService().PersistCodexCompactedConversation(conv, `[{"type":"compaction"}]`, 1)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("PersistCodexCompactedConversation error = %v, want archive failure", err)
	}
	if len(conv.Messages) != len(messages) {
		t.Fatalf("messages = %d, want original %d", len(conv.Messages), len(messages))
	}
	for i := range messages {
		if conv.Messages[i].ID != messages[i].ID {
			t.Fatalf("message[%d] = %q, want %q", i, conv.Messages[i].ID, messages[i].ID)
		}
	}
	if conv.CompactionBlob != "" || conv.CompactionPrefixMessages != 0 {
		t.Fatalf("Codex compaction state changed after archive failure: blob=%q prefix=%d", conv.CompactionBlob, conv.CompactionPrefixMessages)
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
	if _, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, domain.DefaultSettings(), domain.CompactionTriggerInitial); err != nil {
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

// --- from agentengine_test.go ---

// engineStreamStub records the requests it streams and returns scripted
// responses.
type engineStreamStub struct {
	responses []ChatResponse
	errs      []error
	reqs      []ChatRequest
}

func (s *engineStreamStub) stream(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	s.reqs = append(s.reqs, req)
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		if err != nil {
			return ChatResponse{}, err
		}
	}
	resp := ChatResponse{}
	if len(s.responses) > 0 {
		resp = s.responses[0]
		s.responses = s.responses[1:]
	}
	return resp, nil
}

func TestAgentEngineRunsUntilTerminal(t *testing.T) {
	stub := &engineStreamStub{
		responses: []ChatResponse{
			{ToolCalls: []domain.ToolCall{{ID: "c1", Name: "exec"}}}, // round 0: tools
			{Content: "done"}, // round 1: terminal
		},
	}
	var rounds []int
	var outcomes [][]ToolOutcome
	var terminalRounds []int

	_, err := (&AgentEngine{}).Run(context.Background(), AgentRules{
		Stream: stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest {
			return ChatRequest{Messages: st.Messages}
		},
		Terminal: func(st *RoundState, resp ChatResponse) bool {
			terminalRounds = append(terminalRounds, st.Round)
			return len(resp.ToolCalls) == 0
		},
		Execute: func(st *RoundState, resp ChatResponse, calls []domain.ToolCall) ([]ToolOutcome, error) {
			rounds = append(rounds, st.Round)
			out := make([]ToolOutcome, len(calls))
			for i := range calls {
				out[i] = ToolOutcome{Status: domain.ToolOK, Output: "out"}
			}
			return out, nil
		},
		OnRound: func(st *RoundState, resp ChatResponse, out []ToolOutcome) error {
			outcomes = append(outcomes, out)
			st.Messages = append(st.Messages, ChatMessage{Role: "tool"})
			return nil
		},
	}, 5)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(stub.reqs) != 2 {
		t.Fatalf("streamed %d rounds, want 2", len(stub.reqs))
	}
	if len(rounds) != 1 || rounds[0] != 0 {
		t.Fatalf("Execute ran on rounds %v, want [0]", rounds)
	}
	if len(outcomes) != 2 {
		t.Fatalf("OnRound ran %d times, want 2 (tool round + terminal round)", len(outcomes))
	}
	if len(terminalRounds) != 2 {
		t.Fatalf("Terminal evaluated %d times, want 2", len(terminalRounds))
	}
}

func TestAgentEngineStopsAtMaxRounds(t *testing.T) {
	stub := &engineStreamStub{
		responses: []ChatResponse{{ToolCalls: []domain.ToolCall{{ID: "c1", Name: "exec"}}}},
	}
	streams := 0
	_, err := (&AgentEngine{}).Run(context.Background(), AgentRules{
		Stream: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			streams++
			return stub.stream(ctx, req)
		},
		BuildRequest: func(st *RoundState) ChatRequest { return ChatRequest{} },
		Terminal:     func(st *RoundState, resp ChatResponse) bool { return false },
	}, 3)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if streams != 3 {
		t.Fatalf("streamed %d rounds, want 3 (maxRounds)", streams)
	}
}

func TestAgentEngineStreamErrorRecoversViaHook(t *testing.T) {
	stub := &engineStreamStub{
		errs: []error{errors.New("boom")},
		responses: []ChatResponse{
			{Content: "recovered"},
		},
	}
	recovered := 0
	_, err := (&AgentEngine{}).Run(context.Background(), AgentRules{
		Stream: stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest {
			return ChatRequest{}
		},
		Terminal: func(st *RoundState, resp ChatResponse) bool { return true },
		OnStreamErr: func(st *RoundState, err error) bool {
			recovered++
			return true
		},
	}, 3)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("OnStreamErr ran %d times, want 1", recovered)
	}
	if len(stub.reqs) != 2 {
		t.Fatalf("streamed %d rounds, want 2 (failed + recovered)", len(stub.reqs))
	}
}

func TestAgentEngineStreamErrorUnrecoveredFails(t *testing.T) {
	stub := &engineStreamStub{errs: []error{errors.New("boom")}}
	_, err := (&AgentEngine{}).Run(context.Background(), AgentRules{
		Stream:       stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest { return ChatRequest{} },
		Terminal:     func(st *RoundState, resp ChatResponse) bool { return false },
	}, 3)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Run err = %v, want boom", err)
	}
}

func TestAgentEngineBeforeRoundAndCancellation(t *testing.T) {
	stub := &engineStreamStub{
		responses: []ChatResponse{{Content: "done"}},
	}
	beforeRounds := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := (&AgentEngine{}).Run(ctx, AgentRules{
		Stream: stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest {
			return ChatRequest{}
		},
		Terminal: func(st *RoundState, resp ChatResponse) bool { return true },
		BeforeRound: func(st *RoundState) error {
			beforeRounds++
			return nil
		},
	}, 3)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if beforeRounds != 1 {
		t.Fatalf("BeforeRound ran %d times, want 1", beforeRounds)
	}

	// Cancelled context fails before streaming.
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := (&AgentEngine{}).Run(ctx2, AgentRules{
		Stream:       stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest { return ChatRequest{} },
		Terminal:     func(st *RoundState, resp ChatResponse) bool { return true },
	}, 3); err == nil {
		t.Fatal("Run with cancelled context must fail")
	}
}

// --- from headless_turn_test.go ---

func TestFinalHeadlessAssistantMessageUsesTheLastTurnMessage(t *testing.T) {
	messages := []domain.Message{
		{ID: "user-1", Role: domain.RoleUser, Content: "inspect the file"},
		{
			ID:      "assistant-1",
			Role:    domain.RoleAssistant,
			Content: "I will inspect the file first.",
			ToolCalls: []domain.ToolCall{{
				ID: "tool-1", Name: "file_read", Status: domain.ToolOK, Output: "contents",
			}},
		},
		{ID: "assistant-2", Role: domain.RoleAssistant, Content: "The file contains the final answer."},
	}

	message, ok := finalHeadlessAssistantMessage(messages, "assistant-2")
	if !ok {
		t.Fatal("final assistant message was not found")
	}
	if message.ID != "assistant-2" || message.Content != "The file contains the final answer." {
		t.Fatalf("final assistant message = %+v, want the last assistant round", message)
	}
}

func TestFinalHeadlessAssistantMessageKeepsAnEmptyFinalRoundEmpty(t *testing.T) {
	messages := []domain.Message{
		{ID: "assistant-1", Role: domain.RoleAssistant, Content: "preliminary text"},
		{ID: "assistant-2", Role: domain.RoleAssistant, Content: ""},
	}

	message, ok := finalHeadlessAssistantMessage(messages, "assistant-2")
	if !ok {
		t.Fatal("final assistant message was not found")
	}
	if message.ID != "assistant-2" || message.Content != "" {
		t.Fatalf("final assistant message = %+v, want the empty final round", message)
	}
}

func TestHeadlessWorkspaceLearnerUsesDataDir(t *testing.T) {
	tests := []struct {
		name    string
		kind    AgentKind
		ctxWS   string
		dataDir string
		want    string
	}{
		{"learner with dataDir", AgentLearner, "/media/disk/project", "/home/u/.config/nusashell", "/home/u/.config/nusashell"},
		{"legacy learner alias", AgentMemoryConsolidator, "/proj", "/data/nusashell", "/data/nusashell"},
		{"learner without dataDir keeps ctx", AgentLearner, "/proj", "", "/proj"},
		{"automation keeps ctx workspace", AgentAutomation, "/proj", "/data/nusashell", "/proj"},
		{"delegate keeps ctx workspace", AgentDelegate, "/proj", "/data/nusashell", "/proj"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headlessWorkspace(tt.ctxWS, tt.kind, tt.dataDir); got != tt.want {
				t.Fatalf("headlessWorkspace(%q, %s, %q) = %q, want %q", tt.ctxWS, tt.kind, tt.dataDir, got, tt.want)
			}
		})
	}
}

func TestHeadlessTurnsDoNotBroadcastRoomCompletion(t *testing.T) {
	bus := NewBus()
	app := &App{Bus: bus}
	_, events, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	app.emitInteractiveTurnEvent(&TurnRun{Headless: true, ID: "run_learn"}, contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: "run_learn", ConversationID: "conv_learn",
	})
	select {
	case event := <-events:
		t.Fatalf("headless learning turn leaked %s to the UI bus", event.Type)
	case <-time.After(50 * time.Millisecond):
	}

	app.emitInteractiveTurnEvent(&TurnRun{Headless: false, ID: "run_chat"}, contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: "run_chat", ConversationID: "conv_chat",
	})
	select {
	case event := <-events:
		if event.Type != contracts.EventTurnDone {
			t.Fatalf("interactive turn event = %s, want %s", event.Type, contracts.EventTurnDone)
		}
	case <-time.After(time.Second):
		t.Fatal("interactive Agent rooms must still broadcast turn.done")
	}
}

func TestHeadlessTurnsDoNotBroadcastAgentActivity(t *testing.T) {
	bus := NewBus()
	app := &App{Bus: bus}
	_, events, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	app.agentService().EmitCompactionStarted(&TurnRun{
		Headless: true,
		ID:       "run_learn",
	}, "conv_learn")
	select {
	case event := <-events:
		t.Fatalf("headless learning activity leaked %s to the UI bus", event.Type)
	case <-time.After(50 * time.Millisecond):
	}
}

// --- from tpm_learning_test.go ---

// tpmRejectionBody is the field-observed OpenAI Responses API rejection
// (in-stream SSE error): the request fits the raw limit but consumes more
// than half the per-minute budget, so it keeps colliding with whatever is
// already used in the window.
const tpmRejectionBody = "openai: stream error: Rate limit reached for gpt-5.6-luna in organization org-sFd4kjWg5yerL8fwlgAerbgo on tokens per min (TPM): Limit 500000, Used 241004, Requested 355391. Please try again in 11.567s. Visit https://platform.openai.com/account/rate-limits to learn more. kind=sse_transport"

func TestTPMContextCap(t *testing.T) {
	cases := []struct {
		limit, maxOutput, want int
	}{
		{500000, 65536, 184464},  // 500k/2 − 65k output; floor 125k does not bind
		{200000, 65536, 50000},   // 100k − 65k < floor 50k → floor binds
		{500000, 300000, 125000}, // output alone exceeds half → floor 125k
		{100000, 65536, 25000},
		{30000, 65536, 7500},
		{0, 65536, 0},
	}
	for _, tc := range cases {
		if got := tpmContextCap(tc.limit, tc.maxOutput); got != tc.want {
			t.Errorf("tpmContextCap(%d, %d) = %d, want %d", tc.limit, tc.maxOutput, got, tc.want)
		}
	}
}

// TestLearnTPMContextCapWiresIntoContextWindow: learning from a dominant TPM
// rejection must shrink the effective context window used for compaction
// decisions, which makes every future request on that provider+model smaller.
func TestLearnTPMContextCapWiresIntoContextWindow(t *testing.T) {
	app := &App{
		learnedParams: learnedparams.New(&fakeLearnedParamStore{}),
		Logs:          &fakeLogStore{},
	}
	run := &TurnRun{ID: "r1", ProviderID: "openai", Ctx: context.Background()}
	err := &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: errors.New(tpmRejectionBody)}

	if !app.learnTPMContextCap(run, "gpt-5.6-luna", err, 65536) {
		t.Fatal("dominant TPM rejection must learn a context cap")
	}
	// Idempotent: the same rejection must not re-learn (cap unchanged).
	if app.learnTPMContextCap(run, "gpt-5.6-luna", err, 65536) {
		t.Fatal("repeat observation must not re-learn")
	}

	provider := &domain.Provider{ID: "openai", Models: []domain.Model{{ID: "gpt-5.6-luna", Context: 1000000}}}
	settings := domain.DefaultSettings()
	settings.MaxInputTokens = 1000000
	if got := app.resolveContextWindow(provider, "gpt-5.6-luna", settings); got != 184464 {
		t.Fatalf("resolveContextWindow = %d, want 184464 (TPM cap overrides catalog)", got)
	}
}

// TestLearnTPMContextCapSkipsModestRequests: congestion (Used high, request
// small) must not shrink the window — waiting is the right fix there.
func TestLearnTPMContextCapSkipsModestRequests(t *testing.T) {
	app := &App{
		learnedParams: learnedparams.New(&fakeLearnedParamStore{}),
		Logs:          &fakeLogStore{},
	}
	run := &TurnRun{ID: "r1", ProviderID: "openai", Ctx: context.Background()}
	modest := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Rate limit reached for gpt-5.6-luna on tokens per min (TPM): Limit 500000, Used 450000, Requested 40000.")}
	if app.learnTPMContextCap(run, "gpt-5.6-luna", modest, 65536) {
		t.Fatal("modest request must not learn a cap (congestion, not size)")
	}
	if got := app.learnedParams.ContextCap("openai", "gpt-5.6-luna"); got != 0 {
		t.Fatalf("ContextCap = %d, want 0 (nothing learned)", got)
	}
}

// tpmDominatedProvider always rejects with the field-observed TPM body.
type tpmDominatedProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *tpmDominatedProvider) Name() string { return "tpm-dominated" }

func (p *tpmDominatedProvider) Stream(_ context.Context, _ *core.Request) (core.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return nil, &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: errors.New(tpmRejectionBody)}
}

func (p *tpmDominatedProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}

// TestStreamTurnRoundBailsToCompactionOnDominatedTPM proves the retry loop
// does not burn provider attempts on a dominant TPM rejection: it returns
// the error on the first attempt (no backoff sleep) so the emergency
// compaction hook can shrink the context and retry the round, and it learns
// the context cap for subsequent rounds/conversations.
func TestStreamTurnRoundBailsToCompactionOnDominatedTPM(t *testing.T) {
	provider := &tpmDominatedProvider{}
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "do the thing"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	sleeps := 0
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		learnedParams: learnedparams.New(&fakeLearnedParamStore{}),
		Logs:          &fakeLogStore{},
		retrySleeper: func(context.Context, time.Duration) error {
			sleeps++
			return nil
		},
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", ProviderID: "openai", Ctx: context.Background()}

	_, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a1", "gpt-5.6-luna", "", nil, domain.Settings{}, false, 65536, nil, ModelCapabilities{}, 1)
	if err == nil {
		t.Fatal("dominated TPM must surface the provider error (compaction hook handles it)")
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (no futile retries)", provider.calls)
	}
	if sleeps != 0 {
		t.Fatalf("backoff sleeps = %d, want 0 (bail straight to compaction)", sleeps)
	}
	if got := app.learnedParams.ContextCap("openai", "gpt-5.6-luna"); got != 184464 {
		t.Fatalf("learned ContextCap = %d, want 184464", got)
	}
}

// TestFriendlyRateLimitMessageShowsTPMAccounting: the 429 surface for a
// dominant TPM rejection must quote the provider's own numbers (limit, used,
// requested) so the user understands the request must shrink, not that they
// should wait.
func TestFriendlyRateLimitMessageShowsTPMAccounting(t *testing.T) {
	app := &App{Logs: &fakeLogStore{}}
	upstream := &domain.ProviderError{
		Kind:       domain.KindSSETransport,
		StatusCode: 429,
		Err:        errors.New(tpmRejectionBody),
	}
	err := app.friendlyRateLimitError("openai", upstream, 30*time.Second)
	msg := err.Error()
	for _, want := range []string{"500000", "241004", "355391", "compacted"} {
		if !strings.Contains(msg, want) {
			t.Errorf("friendly message missing %q: %s", want, msg)
		}
	}
	// A request-count (RPM) limit without TPM numbers keeps the generic
	// message with the window hint.
	rpm := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, Err: errors.New("rate limited")}
	if msg := app.friendlyRateLimitError("openai", rpm, 0).Error(); strings.Contains(msg, "500000") {
		t.Errorf("RPM message must not quote TPM numbers: %s", msg)
	}
}

// --- from mid_tool_compaction_test.go ---

func TestMidToolCompactionFailureSkipsToolExecution(t *testing.T) {
	body := strings.Repeat("abcdefghij", 40)
	msgs := make([]domain.Message, 0, 42)
	for i := 0; i < 40; i++ {
		msgs = append(msgs, domain.Message{ID: fmt.Sprintf("u%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone})
	}
	const inFlightID = "msg-inflight-failure"
	msgs = append(msgs, domain.Message{ID: inFlightID, Role: domain.RoleAssistant, Status: domain.StatusDone})
	conv := &domain.Conversation{ID: "c-mid-tool-failure", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{conv.ID: conv}}
	toolbox := &recordingToolbox{}
	compactionErr := errors.New("read udp 127.0.0.1:51574->127.0.0.53:53: i/o timeout")
	adapter := &recordingCompleteAdapter{err: compactionErr}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       toolbox,
	}
	provider := &domain.Provider{Models: []domain.Model{{ID: "model", Context: 4000}}}
	run := &TurnRun{ID: "run-mid-tool-failure", ConversationID: conv.ID, Ctx: context.Background()}
	p := app.conversationRulesForTest(run, stubProviderContext(adapter), conv, settings, provider, "model", inFlightID, 2)
	calls := []domain.ToolCall{{ID: "call-1", Name: "read_file", Args: `{ "path": "/tmp/x" }`}}
	_, err := p.Rules().Execute(&RoundState{}, ChatResponse{ToolCalls: calls}, calls)
	if err == nil || !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("Execute error = %v, want compaction failure", err)
	}
	if len(toolbox.names) != 0 {
		t.Fatalf("tools executed after compaction failure: %v", toolbox.names)
	}
}

// TestMidToolCompactionRunsAtToolRequestBoundary covers the tool-spam edge
// case: the model requests a tool round, the round is persisted, and the
// estimated context (requested calls included, tool outputs not yet) already
// crosses the trigger. Compaction must run at the tool-request boundary and
// preserve the in-flight assistant message verbatim so the pending outputs
// are patched into the live tail instead of being lost by strip retention.
func TestMidToolCompactionRunsAtToolRequestBoundary(t *testing.T) {
	body := strings.Repeat("abcdefghij", 40) // ~400 chars ≈ 100 tokens
	msgs := make([]domain.Message, 0, 42)
	for i := 0; i < 40; i++ {
		msgs = append(msgs, domain.Message{ID: fmt.Sprintf("u%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone})
	}
	const inFlightID = "msg-inflight"
	msgs = append(msgs, domain.Message{
		ID:        inFlightID,
		Role:      domain.RoleAssistant,
		Content:   "reading now",
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "read", Args: `{"path":"/x"}`}},
	})
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	bus := NewBus()
	_, events, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: bus}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	provider := &domain.Provider{Models: []domain.Model{{ID: "model", Context: 4000}}}
	run := &TurnRun{ID: "run1", ConversationID: "c1", Ctx: context.Background()}
	p := app.conversationRulesForTest(run, stubProviderContext(adapter), conv, settings, provider, "model", inFlightID, 2)

	if !p.TryMidToolCompaction() {
		t.Fatal("mid-tool compaction did not run at the tool-request boundary")
	}

	// The compaction lifecycle events fired (started then compacted). The
	// bus carries unrelated events (logs.append per log line), so scan the
	// channel until the relevant event arrives.
	gotCompacted := false
	deadline := time.After(3 * time.Second)
	for !gotCompacted {
		select {
		case ev := <-events:
			if ev.Type == contracts.EventCompacted {
				gotCompacted = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for agent.compacted event")
		}
	}

	// The store now carries the compaction handover as the first message.
	saved, err := app.Conversations.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) == 0 || !domain.IsCompactionSummary(saved.Messages[0].Content) {
		t.Fatalf("compaction handover not persisted first: %+v", saved.Messages[:1])
	}

	// The in-flight round survived verbatim: its tool calls are still present
	// so executeTurnTools can patch the outputs in afterwards.
	var kept *domain.Message
	for i := range saved.Messages {
		if saved.Messages[i].ID == inFlightID {
			kept = &saved.Messages[i]
		}
	}
	if kept == nil {
		t.Fatal("in-flight assistant message was dropped by compaction")
	}
	if len(kept.ToolCalls) != 1 || kept.ToolCalls[0].ID != "call-1" {
		t.Fatalf("in-flight tool calls not preserved verbatim: %+v", kept.ToolCalls)
	}

	// The in-memory conversation was refreshed (the hook's estimate now sees
	// the compacted state, not the stale one).
	if p.Conv() == conv || !domain.IsCompactionSummary(p.Conv().Messages[0].Content) {
		t.Fatal("p.conv was not refreshed to the compacted conversation")
	}
	if got, want := p.CompactionAttempts(), 1; got != want {
		t.Fatalf("compactionAttempts = %d, want %d", got, want)
	}
}

// TestMidToolCompactionSkipsBelowTrigger: when the estimate is still below
// the trigger at the tool-request boundary, compaction must not run — the
// proactive/emergency hooks stay the only safety nets.
func TestMidToolCompactionSkipsBelowTrigger(t *testing.T) {
	conv := &domain.Conversation{ID: "c2", Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "small", Status: domain.StatusDone},
	}}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c2": conv}}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	p := app.conversationRulesForTest(&TurnRun{ID: "run2", ConversationID: "c2", Ctx: context.Background()}, ProviderContext{}, conv, settings, &domain.Provider{Models: []domain.Model{{ID: "model", Context: 4000}}}, "model", "", 2)
	if p.TryMidToolCompaction() {
		t.Fatal("mid-tool compaction ran below the trigger")
	}
	if got := len(conv.Messages); got != 1 {
		t.Fatalf("messages = %d, want 1 (conversation untouched)", got)
	}
}

// TestMidToolCompactionSkipsFirstRound keeps the tool-request hook inert on
// the first round (round 1 = turn start, already covered by initializeTurn).
func TestMidToolCompactionSkipsFirstRound(t *testing.T) {
	body := strings.Repeat("abcdefghij", 40)
	msgs := make([]domain.Message, 0, 42)
	for i := 0; i < 40; i++ {
		msgs = append(msgs, domain.Message{ID: fmt.Sprintf("u%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone})
	}
	conv := &domain.Conversation{ID: "c3", Messages: msgs}
	app := &App{Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c3": conv}}, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	p := app.conversationRulesForTest(&TurnRun{ID: "run3", ConversationID: "c3", Ctx: context.Background()}, ProviderContext{}, conv, settings, &domain.Provider{Models: []domain.Model{{ID: "model", Context: 4000}}}, "model", "", 1)
	if p.TryMidToolCompaction() {
		t.Fatal("mid-tool compaction ran on the first round")
	}
}

// --- from server_compaction_test.go ---

func TestServerCompactionContextManagementEligibleModel(t *testing.T) {
	cases := []struct {
		model         string
		wantThreshold int
	}{
		{"gpt-5.2", 360000},      // 400k * 0.9
		{"gpt-5.6-luna", 942818}, // 1047576 * 0.9
		{"gpt-4.1", 942818},      // 1047576 * 0.9
		{"o3", 180000},           // 200k * 0.9
		{"o1", 180000},           // 200k * 0.9
	}
	for _, tc := range cases {
		cm := serverCompactionContextManagement(tc.model)
		if cm == nil {
			t.Fatalf("serverCompactionContextManagement(%q) = nil, want non-nil", tc.model)
		}
		if len(cm) != 1 {
			t.Fatalf("cm len = %d, want 1", len(cm))
		}
		if cm[0]["type"] != "compaction" {
			t.Fatalf("cm[0].type = %v, want compaction", cm[0]["type"])
		}
		threshold, ok := cm[0]["compact_threshold"].(int)
		if !ok {
			t.Fatalf("cm[0].compact_threshold = %T, want int", cm[0]["compact_threshold"])
		}
		if threshold != tc.wantThreshold {
			t.Errorf("serverCompactionContextManagement(%q) threshold = %d, want %d", tc.model, threshold, tc.wantThreshold)
		}
	}
}

func TestServerCompactionContextManagementIneligibleModel(t *testing.T) {
	cases := []string{
		"gpt-4o",      // 128k context, below 200k floor
		"gpt-4-turbo", // not in table
		"claude-sonnet-4",
		"",
	}
	for _, model := range cases {
		cm := serverCompactionContextManagement(model)
		if cm != nil {
			t.Errorf("serverCompactionContextManagement(%q) = %v, want nil", model, cm)
		}
	}
}

func TestServerCompactionContextManagementCodexKindIsDisabled(t *testing.T) {
	if got := serverCompactionContextManagementForKind("gpt-5-codex", domain.ProviderCodex); got != nil {
		t.Fatalf("Codex context management = %v, want nil; Codex uses remote v2 trigger", got)
	}
	if got := serverCompactionContextManagementForKind("gpt-5.2", domain.ProviderResponses); got == nil {
		t.Fatal("Responses context management = nil, want eligible OpenAI directive")
	}
}

func TestServerCompactionContextManagementFloorEnforced(t *testing.T) {
	// Temporarily raise the floor to verify it clamps. The threshold is
	// computed in the domain, so the floor must be mutated there.
	original := domain.ServerCompactionThresholdFloor
	domain.ServerCompactionThresholdFloor = 200000
	defer func() { domain.ServerCompactionThresholdFloor = original }()

	// o3 has 200k context, 0.9 * 200k = 180k < 200k floor → should use floor
	cm := serverCompactionContextManagement("o3")
	if cm == nil {
		t.Fatalf("serverCompactionContextManagement(o3) = nil, want non-nil")
	}
	threshold, _ := cm[0]["compact_threshold"].(int)
	if threshold != 200000 {
		t.Errorf("threshold = %d, want 200000 (floor)", threshold)
	}
}

func TestFromCoreResponseCarriesCompactionItems(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"type":"compaction","encrypted_content":"ENC-1"}`),
		json.RawMessage(`{"type":"compaction","encrypted_content":"ENC-2"}`),
	}
	resp := &core.Response{
		Blocks:          []core.Block{core.Text("answer")},
		CompactionItems: items,
	}
	out := FromCoreResponse(resp)
	if len(out.CompactionItems) != 2 {
		t.Fatalf("CompactionItems len = %d, want 2", len(out.CompactionItems))
	}
	if string(out.CompactionItems[0]) != `{"type":"compaction","encrypted_content":"ENC-1"}` {
		t.Fatalf("CompactionItems[0] = %s", out.CompactionItems[0])
	}
}

func TestCompactConversationSkipsForServerSideEligibleModel(t *testing.T) {
	// For server-side eligible models, compactConversation should return
	// immediately without doing any client-side summarization.
	keepFiller := strings.Repeat("keep-me-recent-user-message-", 40)
	msgs := []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "question-one", Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: "answer-one", Status: domain.StatusDone},
	}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("keep%d", i), Role: domain.RoleUser, Content: keepFiller, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c-server-skip", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c-server-skip": conv}}
	adapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()

	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "gpt-5.2", 4000, settings, domain.CompactionTriggerInitial)
	if err != nil {
		t.Fatalf("compactConversation: %v", err)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty for server-side eligible model", summary)
	}
	// The recording adapter should NOT have been called.
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) != 0 {
		t.Fatalf("adapter requests = %d, want 0 (server-side eligible skips client-side)", len(adapter.requests))
	}
}

func TestCompactConversationUsesCodexRemoteCompaction(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c-codex-remote",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: strings.Repeat("old question ", 30000), Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant, Content: strings.Repeat("old answer ", 30000), Status: domain.StatusDone},
			{ID: "u2", Role: domain.RoleUser, Content: "keep this latest question", Status: domain.StatusDone},
			{ID: "a2", Role: domain.RoleAssistant, Content: "keep this latest answer", Status: domain.StatusDone},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c-codex-remote": conv}}
	provider := &codexCompactionProvider{
		streamEvents: []core.Event{
			core.ProviderEvent{
				Name: "compaction",
				Raw:  json.RawMessage(`{"type":"compaction","encrypted_content":"ENC-1"}`),
			},
			core.DoneEvent{FinishReason: core.FinishReasonStop, Provider: "codex", Model: "gpt-5-codex"},
		},
	}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	adapter := ProviderContext{Provider: provider, Kind: domain.ProviderCodex}

	summary, err := app.compactConversation(context.Background(), adapter, conv, "gpt-5-codex", 4000, domain.DefaultSettings(), domain.CompactionTriggerInitial)
	if err != nil {
		t.Fatalf("compactConversation: %v", err)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty for opaque Codex checkpoint", summary)
	}
	if provider.chatCalls != 0 {
		t.Fatalf("Chat calls = %d, want 0; Codex compaction must stream", provider.chatCalls)
	}
	if len(provider.streamRequests) != 1 {
		t.Fatalf("Stream calls = %d, want 1", len(provider.streamRequests))
	}
	req := provider.streamRequests[0]
	if got := req.ProviderOptions["compaction_trigger"]; got != true {
		t.Fatalf("compaction_trigger = %#v, want true", got)
	}
	saved := store.convs[conv.ID]
	if saved.CompactionBlob != `[{"type":"compaction","encrypted_content":"ENC-1"}]` {
		t.Fatalf("CompactionBlob = %q, want opaque checkpoint", saved.CompactionBlob)
	}
	if saved.Summary != "" {
		t.Fatalf("Summary = %q, want empty", saved.Summary)
	}
	if saved.CompactionPrefixMessages != 2 {
		t.Fatalf("CompactionPrefixMessages = %d, want 2 retained transcript messages", saved.CompactionPrefixMessages)
	}
	if len(saved.Messages) <= saved.CompactionPrefixMessages || !domain.IsHydrationMessage(saved.Messages[saved.CompactionPrefixMessages]) {
		t.Fatalf("messages = %+v, want hydration after the persisted checkpoint boundary", saved.Messages)
	}
	retainedIDs := make([]string, 0, saved.CompactionPrefixMessages)
	for _, message := range saved.Messages {
		if domain.IsCompactionSummary(message.Content) {
			t.Fatalf("Codex compaction inserted a text handover: %+v", message)
		}
		if domain.IsHydrationMessage(message) {
			continue
		}
		retainedIDs = append(retainedIDs, message.ID)
	}
	if len(retainedIDs) != 2 || retainedIDs[0] != "u2" || retainedIDs[1] != "a2" {
		t.Fatalf("Codex active epoch ids = %v, want chronological suffix [u2 a2]", retainedIDs)
	}
	if len(store.archived) != 2 || store.archived[0].ID != "u1" || store.archived[1].ID != "a1" {
		t.Fatalf("Codex archived messages = %+v, want chronological prefix moved to the archive", store.archived)
	}
}

func TestCodexRemoteCompactionRebuildsAdapterAfterCircuitOpen(t *testing.T) {
	conv := &domain.Conversation{ID: "c-codex-failover", Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "old question", Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: "old answer", Status: domain.StatusDone},
	}}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{conv.ID: conv}}
	old := &codexCompactionProvider{streamErr: errors.New("stale account must not be used")}
	fresh := &codexCompactionProvider{streamEvents: []core.Event{
		core.ProviderEvent{Name: "compaction", Raw: json.RawMessage(`{"type":"compaction","encrypted_content":"ENC-NEW"}`)},
		core.DoneEvent{FinishReason: core.FinishReasonStop, Provider: "codex", Model: "gpt-5-codex"},
	}}
	codexProvider := &domain.Provider{ID: "codex", Kind: domain.ProviderCodex}
	router := NewCodexAccountRouter()
	router.MarkCircuitOpen("exhausted", time.Now().Add(time.Hour))
	var factoryKey string
	app := &App{
		Conversations: store,
		Providers:     &fakeProviderStore{items: map[string]*domain.Provider{"codex": codexProvider}},
		Credentials:   &fakeVisionCredStore{creds: map[string]string{accountKey("codex", "exhausted"): "old", accountKey("codex", "fresh"): "new"}},
		CodexRouter:   router,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Factory: func(_ context.Context, _ *domain.Provider, key string) (AIProvider, error) {
			factoryKey = key
			return fresh, nil
		},
	}
	adapter := ProviderContext{Provider: old, ProviderID: "codex", Kind: domain.ProviderCodex}
	if _, err := app.compactConversation(context.Background(), adapter, conv, "gpt-5-codex", 4000, domain.DefaultSettings(), domain.CompactionTriggerInitial); err != nil {
		t.Fatalf("compactConversation: %v", err)
	}
	if factoryKey != "new" {
		t.Fatalf("factory key = %q, want fresh account token", factoryKey)
	}
	if len(old.streamRequests) != 0 {
		t.Fatalf("stale adapter stream calls = %d, want 0", len(old.streamRequests))
	}
	if len(fresh.streamRequests) != 1 {
		t.Fatalf("fresh adapter stream calls = %d, want 1", len(fresh.streamRequests))
	}
}

func TestCompactConversationCodexFailureDoesNotFallbackToSummary(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c-codex-error",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "question", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant, Content: "answer", Status: domain.StatusDone},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c-codex-error": conv}}
	provider := &codexCompactionProvider{streamErr: errors.New("remote compaction unavailable")}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	adapter := ProviderContext{Provider: provider, Kind: domain.ProviderCodex}

	if _, err := app.compactConversation(context.Background(), adapter, conv, "gpt-5-codex", 4000, domain.DefaultSettings(), domain.CompactionTriggerInitial); err == nil {
		t.Fatal("expected Codex remote compaction error")
	}
	if provider.chatCalls != 0 {
		t.Fatalf("Chat calls = %d, want 0; failed Codex compaction must not fall back to summary", provider.chatCalls)
	}
	if conv.CompactionBlob != "" || conv.Summary != "" {
		t.Fatalf("conversation changed after failed compaction: blob=%q summary=%q", conv.CompactionBlob, conv.Summary)
	}
}

// --- from capabilities_test.go ---

func TestModelCapabilitiesUnknownModelDefaultsVisionOnly(t *testing.T) {
	provider := &domain.Provider{ID: "p1", Models: nil}
	caps := modelCapabilitiesWithLearned(provider, "unknown-model", nil, nil)
	if !caps.Vision {
		t.Errorf("unknown model should default Vision=true, got %+v", caps)
	}
	if caps.Audio {
		t.Errorf("unknown model should default Audio=false (rare capability, causes provider errors), got %+v", caps)
	}
	if caps.Video {
		t.Errorf("unknown model should default Video=false (rare capability, causes provider errors), got %+v", caps)
	}
	if caps.Document {
		t.Errorf("unknown model should default Document=false (rare capability, may silently drop PDF), got %+v", caps)
	}
}

func TestModelCapabilitiesNilProviderDefaultsVisionOnly(t *testing.T) {
	caps := modelCapabilitiesWithLearned(nil, "any", nil, nil)
	if !caps.Vision {
		t.Errorf("nil provider should default Vision=true, got %+v", caps)
	}
	if caps.Audio {
		t.Errorf("nil provider should default Audio=false, got %+v", caps)
	}
	if caps.Video {
		t.Errorf("nil provider should default Video=false, got %+v", caps)
	}
	if caps.Document {
		t.Errorf("nil provider should default Document=false, got %+v", caps)
	}
}

func TestModelCapabilitiesFromMetadata(t *testing.T) {
	provider := &domain.Provider{
		ID: "p1",
		Models: []domain.Model{
			{ID: "gemini-2.5-flash", Vision: true, Audio: true, Video: true, Document: true},
			{ID: "gpt-4o", Vision: true, Audio: false, Video: false, Document: true},
			{ID: "llama-3", Vision: false, Audio: false, Video: false, Document: false},
		},
	}

	tests := []struct {
		model        string
		wantVision   bool
		wantAudio    bool
		wantVideo    bool
		wantDocument bool
	}{
		{"gemini-2.5-flash", true, true, true, true},
		{"gpt-4o", true, false, false, true},
		{"llama-3", false, false, false, false},
	}
	for _, tt := range tests {
		caps := modelCapabilitiesWithLearned(provider, tt.model, nil, nil)
		if caps.Vision != tt.wantVision || caps.Audio != tt.wantAudio || caps.Video != tt.wantVideo || caps.Document != tt.wantDocument {
			t.Errorf("model %s: got %+v, want vision=%v audio=%v video=%v document=%v",
				tt.model, caps, tt.wantVision, tt.wantAudio, tt.wantVideo, tt.wantDocument)
		}
	}
}

// TestModelCapabilitiesLearnedDisablesVision proves that a learned
// "text-only" 400 for a provider+model proactively disables Vision in
// caps, so the first request to that model strips images instead of
// waiting for a 400 and retrying. This is the proactive path; the retry
// loop in streamTurnRound handles the reactive path.
func TestModelCapabilitiesLearnedDisablesVision(t *testing.T) {
	store := &fakeLearnedParamStore{}
	cache := learnedparams.New(store)
	cache.LearnFrom400("openrouter", "qwen3.8-max-free",
		`Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part`)

	// Unknown model defaults Vision=true, but the learned rule overrides it.
	provider := &domain.Provider{ID: "openrouter", Models: nil}
	caps := modelCapabilitiesWithLearned(provider, "qwen3.8-max-free", cache, nil)
	if caps.Vision {
		t.Errorf("learned text-only should disable Vision, got %+v", caps)
	}
}

// TestModelCapabilitiesLearnedDoesNotAffectOtherModel proves that a
// learned disabled modality for one model does not leak to another.
func TestModelCapabilitiesLearnedDoesNotAffectOtherModel(t *testing.T) {
	store := &fakeLearnedParamStore{}
	cache := learnedparams.New(store)
	cache.LearnFrom400("openrouter", "qwen3.8-max-free", `text-only`)

	provider := &domain.Provider{ID: "openrouter", Models: nil}
	caps := modelCapabilitiesWithLearned(provider, "other-model", cache, nil)
	if !caps.Vision {
		t.Errorf("learned rule for qwen3.8 leaked to other-model, got %+v", caps)
	}
}

func TestChatMessagesAudioPlaceholderForNonAudioModel(t *testing.T) {
	dir := t.TempDir()
	audioPath := testAbsPath(dir, "recording.mp3")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "listen to this", Attachments: []domain.Attachment{
			{Type: "audio", Name: "recording.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,abc", FilePath: audioPath},
		}},
	}}

	msgs := chatMessages(conv, "", ModelCapabilities{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	userMsg := msgs[0]
	if !strings.Contains(userMsg.Content, "audio content omitted") {
		t.Errorf("content should contain audio omission placeholder, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, audioPath) {
		t.Errorf("placeholder should include absolute file path, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "read_media") {
		t.Errorf("placeholder should mention read_media tool, got: %q", userMsg.Content)
	}
	// Audio attachment should be stripped (non-audio model)
	if len(userMsg.Attachments) != 0 {
		t.Errorf("expected 0 attachments (audio stripped), got %d", len(userMsg.Attachments))
	}
}

func TestChatMessagesVideoPlaceholderForNonVideoModel(t *testing.T) {
	dir := t.TempDir()
	videoPath := testAbsPath(dir, "clip.mp4")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "watch this", Attachments: []domain.Attachment{
			{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,abc", FilePath: videoPath},
		}},
	}}

	msgs := chatMessages(conv, "", ModelCapabilities{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	userMsg := msgs[0]
	if !strings.Contains(userMsg.Content, "video content omitted") {
		t.Errorf("content should contain video omission placeholder, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, videoPath) {
		t.Errorf("placeholder should include absolute file path, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "read_media") {
		t.Errorf("placeholder should mention read_media tool, got: %q", userMsg.Content)
	}
	// Video attachment should be stripped (non-video model)
	if len(userMsg.Attachments) != 0 {
		t.Errorf("expected 0 attachments (video stripped), got %d", len(userMsg.Attachments))
	}
}

func TestChatMessagesAudioKeptForAudioModel(t *testing.T) {
	dir := t.TempDir()
	audioPath := testAbsPath(dir, "recording.mp3")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "listen", Attachments: []domain.Attachment{
			{Type: "audio", Name: "recording.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,abc", FilePath: audioPath},
		}},
	}}

	msgs := chatMessages(conv, "", ModelCapabilities{Audio: true})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	userMsg := msgs[0]
	if strings.Contains(userMsg.Content, "audio content omitted") {
		t.Errorf("audio-capable model should not get placeholder, got: %q", userMsg.Content)
	}
	if len(userMsg.Attachments) != 1 || userMsg.Attachments[0].Type != "audio" {
		t.Errorf("audio attachment should be kept for audio-capable model, got %d attachments", len(userMsg.Attachments))
	}
}

// TestFilterToolAttachmentsByCapsStripsAudio: a non-audio model should not
// receive audio attachments from tool results (e.g. read_media). The audio
// should be stripped and replaced with a text note.
func TestFilterToolAttachmentsByCapsStripsAudio(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "audio", Name: "recording.mp3", FilePath: "/tmp/rec.mp3"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Audio loaded.", ModelCapabilities{})
	if len(filtered) != 0 {
		t.Errorf("audio should be stripped for non-audio model, got %d attachments", len(filtered))
	}
	if !strings.Contains(content, "cannot be played") {
		t.Errorf("content should note audio can't be played, got: %q", content)
	}
	if !strings.Contains(content, "/tmp/rec.mp3") {
		t.Errorf("content should include file path, got: %q", content)
	}
}

// TestFilterToolAttachmentsByCapsKeepsAudioForAudioModel: an audio-capable
// model should receive audio attachments from tool results unchanged.
func TestFilterToolAttachmentsByCapsKeepsAudioForAudioModel(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "audio", Name: "recording.mp3", FilePath: "/tmp/rec.mp3"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Audio loaded.", ModelCapabilities{Audio: true})
	if len(filtered) != 1 || filtered[0].Type != "audio" {
		t.Errorf("audio should be kept for audio-capable model, got %d attachments", len(filtered))
	}
	if strings.Contains(content, "cannot be played") {
		t.Errorf("content should not note audio can't be played, got: %q", content)
	}
}

// TestFilterToolAttachmentsByCapsStripsVideo: a non-video model should not
// receive video attachments from tool results.
func TestFilterToolAttachmentsByCapsStripsVideo(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "video", Name: "clip.mp4", FilePath: "/tmp/clip.mp4"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Video loaded.", ModelCapabilities{})
	if len(filtered) != 0 {
		t.Errorf("video should be stripped for non-video model, got %d attachments", len(filtered))
	}
	if !strings.Contains(content, "cannot be shown") {
		t.Errorf("content should note video can't be shown, got: %q", content)
	}
}

// TestFilterToolAttachmentsByCapsKeepsImageForVisionModel: a vision-capable
// model should receive image attachments from tool results unchanged.
func TestFilterToolAttachmentsByCapsKeepsImageForVisionModel(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "image", Name: "photo.png", FilePath: "/tmp/photo.png"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Image loaded.", ModelCapabilities{Vision: true})
	if len(filtered) != 1 || filtered[0].Type != "image" {
		t.Errorf("image should be kept for vision-capable model, got %d attachments", len(filtered))
	}
	if strings.Contains(content, "cannot be shown") {
		t.Errorf("content should not note image can't be shown, got: %q", content)
	}
}

// TestModelCapabilitiesReasoningReplayFromCatalog proves that the
// ReasoningReplay flag is resolved from the model's InterleavedField
// catalog signal. A model with InterleavedField="reasoning_content"
// (e.g. GLM, DeepSeek V4, Kimi) gets ReasoningReplay=true; a model
// without it gets false.
func TestModelCapabilitiesReasoningReplayFromCatalog(t *testing.T) {
	provider := &domain.Provider{
		ID: "openrouter",
		Models: []domain.Model{
			{ID: "glm-5.2", InterleavedField: "reasoning_content"},
			{ID: "gpt-5.5", InterleavedField: ""},
		},
	}

	caps := modelCapabilitiesWithLearned(provider, "glm-5.2", nil, nil)
	if !caps.ReasoningReplay {
		t.Errorf("glm-5.2 with interleaved_field=reasoning_content: ReasoningReplay = false, want true")
	}

	caps = modelCapabilitiesWithLearned(provider, "gpt-5.5", nil, nil)
	if caps.ReasoningReplay {
		t.Errorf("gpt-5.5 with no interleaved_field: ReasoningReplay = true, want false")
	}
}

// TestModelCapabilitiesReasoningReplayPatternFallback proves that the
// pattern fallback catches models not in the catalog but matching known
// reasoning-replay patterns (e.g. stealth/ox-alpha on OpenRouter, which
// doesn't expose the interleaved signal).
func TestModelCapabilitiesReasoningReplayPatternFallback(t *testing.T) {
	provider := &domain.Provider{
		ID: "openrouter",
		Models: []domain.Model{
			{ID: "stealth/ox-alpha"}, // no InterleavedField (OpenRouter hides it)
		},
	}
	caps := modelCapabilitiesWithLearned(provider, "stealth/ox-alpha", nil, nil)
	if !caps.ReasoningReplay {
		t.Errorf("stealth/ox-alpha should match pattern fallback: ReasoningReplay = false, want true")
	}
}

// TestModelCapabilitiesReasoningReplayOpenCodeHost proves that OpenCode
// Zen/Go (generated provider IDs, empty InterleavedField on glm-5.3 /
// deepseek-v4-flash) still requires reasoning_content replay. The static
// "opencode-go" whitelist never matches NusaShell's prov_* IDs; the host
// plus the model's Reasoning flag is the durable signal. Non-reasoning
// models on the same host must not get a forced reasoning_content placeholder.
func TestModelCapabilitiesReasoningReplayOpenCodeHost(t *testing.T) {
	provider := &domain.Provider{
		ID:      "prov_b9587aa5f937c4f2",
		BaseURL: "https://opencode.ai/zen/go/v1",
		Models: []domain.Model{
			{ID: "glm-5.3-flash", Reasoning: true},
			{ID: "deepseek-v4-flash", Reasoning: true},
			{ID: "omen-alpha", Reasoning: false},
		},
	}
	for _, model := range []string{"glm-5.3-flash", "deepseek-v4-flash"} {
		caps := modelCapabilitiesWithLearned(provider, model, nil, nil)
		if !caps.ReasoningReplay {
			t.Errorf("%s on opencode.ai: ReasoningReplay = false, want true", model)
		}
	}
	nonReasoning := modelCapabilitiesWithLearned(provider, "omen-alpha", nil, nil)
	if nonReasoning.ReasoningReplay {
		t.Errorf("omen-alpha (non-reasoning) on opencode.ai: ReasoningReplay = true, want false")
	}

	other := &domain.Provider{
		ID:      "prov_other",
		BaseURL: "https://api.openai.com/v1",
		Models:  []domain.Model{{ID: "gpt-5.5"}},
	}
	caps := modelCapabilitiesWithLearned(other, "gpt-5.5", nil, nil)
	if caps.ReasoningReplay {
		t.Errorf("gpt-5.5 on openai.com: ReasoningReplay = true, want false")
	}
}

// TestModelCapabilitiesReasoningFlagFromCatalog proves the Reasoning
// capability is resolved from the model's catalog metadata. A model with
// Reasoning=true gets caps.Reasoning=true; a model without it gets false.
// This flag guards the effort strip in streamTurnRoundOnce so non-reasoning
// models do not receive a thinking field.
func TestModelCapabilitiesReasoningFlagFromCatalog(t *testing.T) {
	provider := &domain.Provider{
		ID: "openrouter",
		Models: []domain.Model{
			{ID: "deepseek/deepseek-r1", Reasoning: true},
			{ID: "openai/gpt-4.1", Reasoning: false},
		},
	}
	caps := modelCapabilitiesWithLearned(provider, "deepseek/deepseek-r1", nil, nil)
	if !caps.Reasoning {
		t.Errorf("deepseek-r1 with Reasoning=true: caps.Reasoning = false, want true")
	}
	caps = modelCapabilitiesWithLearned(provider, "openai/gpt-4.1", nil, nil)
	if caps.Reasoning {
		t.Errorf("gpt-4.1 with Reasoning=false: caps.Reasoning = true, want false")
	}
}

func TestChatMessagesDocumentPlaceholderForNonDocumentModel(t *testing.T) {
	dir := t.TempDir()
	pdfPath := testAbsPath(dir, "report.pdf")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "read this", Attachments: []domain.Attachment{
			{Type: "file", Name: "report.pdf", MediaType: "application/pdf", DataURL: "data:application/pdf;base64,abc", FilePath: pdfPath},
		}},
	}}

	msgs := chatMessages(conv, "", ModelCapabilities{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	userMsg := msgs[0]
	if !strings.Contains(userMsg.Content, "document content omitted") {
		t.Errorf("content should contain document omission placeholder, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, pdfPath) {
		t.Errorf("placeholder should include absolute file path, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "read_media") {
		t.Errorf("placeholder should mention read_media tool, got: %q", userMsg.Content)
	}
	if len(userMsg.Attachments) != 0 {
		t.Errorf("expected 0 attachments (file stripped), got %d", len(userMsg.Attachments))
	}
}

func TestChatMessagesDocumentKeptForDocumentModel(t *testing.T) {
	dir := t.TempDir()
	pdfPath := testAbsPath(dir, "report.pdf")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "read", Attachments: []domain.Attachment{
			{Type: "file", Name: "report.pdf", MediaType: "application/pdf", DataURL: "data:application/pdf;base64,abc", FilePath: pdfPath},
		}},
	}}

	msgs := chatMessages(conv, "", ModelCapabilities{Document: true})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	userMsg := msgs[0]
	if strings.Contains(userMsg.Content, "document content omitted") {
		t.Errorf("document-capable model should not get placeholder, got: %q", userMsg.Content)
	}
	if len(userMsg.Attachments) != 1 || userMsg.Attachments[0].Type != "file" {
		t.Errorf("file attachment should be kept for document-capable model, got %d attachments", len(userMsg.Attachments))
	}
}

func TestFilterToolAttachmentsByCapsStripsDocument(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "file", Name: "report.pdf", FilePath: "/tmp/report.pdf"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Document loaded.", ModelCapabilities{})
	if len(filtered) != 0 {
		t.Errorf("file should be stripped for non-document model, got %d attachments", len(filtered))
	}
	if !strings.Contains(content, "cannot be read") {
		t.Errorf("content should note document can't be read, got: %q", content)
	}
	if !strings.Contains(content, "/tmp/report.pdf") {
		t.Errorf("content should include file path, got: %q", content)
	}
}

func TestFilterToolAttachmentsByCapsKeepsDocumentForDocumentModel(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "file", Name: "report.pdf", FilePath: "/tmp/report.pdf"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Document loaded.", ModelCapabilities{Document: true})
	if len(filtered) != 1 || filtered[0].Type != "file" {
		t.Errorf("file should be kept for document-capable model, got %d attachments", len(filtered))
	}
	if strings.Contains(content, "cannot be read") {
		t.Errorf("content should not note document can't be read, got: %q", content)
	}
}

// TestChatMessagesToolResultNoteInsideUntrustedEnvelope verifies that
// capability-filter notes appended to tool results (e.g. "[Audio ... was
// loaded but cannot be played]") land INSIDE the <untrusted_tool_result>
// envelope, not after the closing tag. If notes land outside, a malicious
// tool could craft an attachment file path containing injection instructions
// that the model would treat as trusted/system-level content.
func TestChatMessagesToolResultNoteInsideUntrustedEnvelope(t *testing.T) {
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "a1", Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{
			ID:     "tc",
			Name:   "generate_media",
			Output: "---\nfile_path: /tmp/speech.wav\nmedia_type: audio/wav\nstatus: completed\n---\n\nSpeech generated.",
			OutputAttachments: []domain.Attachment{{
				Type:     "audio",
				Name:     "speech.wav",
				FilePath: "/tmp/speech.wav",
			}},
		}}},
	}}
	msgs := chatMessages(conv, "", ModelCapabilities{}) // no audio cap
	var tool *ChatMessage
	for i := range msgs {
		if msgs[i].Role == "tool" && msgs[i].ToolResult != nil {
			tool = &msgs[i]
			break
		}
	}
	if tool == nil {
		t.Fatal("expected a tool message, got none")
	}
	content := tool.ToolResult.Content
	closeIdx := strings.Index(content, "</untrusted_tool_result>")
	noteIdx := strings.Index(content, "[Audio")
	if closeIdx < 0 {
		t.Fatalf("missing untrusted envelope close tag in tool content:\n%s", content)
	}
	if noteIdx < 0 {
		t.Fatalf("missing audio capability note in tool content:\n%s", content)
	}
	if noteIdx > closeIdx {
		t.Fatalf("audio note is OUTSIDE the untrusted envelope (note at %d, close at %d):\n%s", noteIdx, closeIdx, content)
	}
}

// --- from announcement_test.go ---

func TestDrainAnnouncementsInjectsPendingAtRoundBoundary(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-1", Type: "config_changed", Args: `{"type":"config_changed"}`, Message: "config changed", CreatedAt: time.Now(),
	})
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}

	applied, err := app.drainAnnouncements(run)
	if err != nil {
		t.Fatalf("drainAnnouncements: %v", err)
	}
	if !applied {
		t.Fatal("expected an announcement to be injected")
	}
	last := conv.Messages[len(conv.Messages)-1]
	if last.Role != domain.RoleAssistant || len(last.ToolCalls) != 1 {
		t.Fatalf("injected message = %+v, want assistant announcement tool call", last)
	}
	tc := last.ToolCalls[0]
	if tc.Name != domain.AnnouncementToolName || tc.Output != "config changed" {
		t.Fatalf("tool call = %+v, want announcement with pre-filled result", tc)
	}
	if len(conv.PendingAnnouncements) != 0 {
		t.Fatalf("pending queue not cleared: %+v", conv.PendingAnnouncements)
	}
}

func TestDrainAnnouncementsMergesAllPendingTypesIntoOne(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-1", Type: "config_changed", Args: `{"type":"config_changed"}`, Message: "config changed", CreatedAt: time.Now(),
	})
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-2", Type: "memory_changed", Args: `{"type":"memory_changed"}`, Message: "memory changed", CreatedAt: time.Now(),
	})
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-3", Type: "skills_changed", Args: `{"type":"skills_changed"}`, Message: "skills changed", CreatedAt: time.Now(),
	})
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}

	applied, err := app.drainAnnouncements(run)
	if err != nil {
		t.Fatalf("drainAnnouncements: %v", err)
	}
	if !applied {
		t.Fatal("expected announcements to be injected")
	}
	// All pending notices merge into ONE synthetic announcement tool call.
	if len(conv.Messages) != 1 {
		t.Fatalf("messages = %d, want 1 merged announcement message", len(conv.Messages))
	}
	tc := conv.Messages[0].ToolCalls
	if len(tc) != 1 || tc[0].Name != domain.AnnouncementToolName {
		t.Fatalf("injected message = %+v, want a single announcement tool call", conv.Messages[0])
	}
	wantOutput := "config changed\n---\nmemory changed\n---\nskills changed"
	if tc[0].Output != wantOutput {
		t.Fatalf("output = %q, want merged notices:\n%s", tc[0].Output, wantOutput)
	}
	for _, want := range []string{`"items"`, `"config_changed"`, `"memory_changed"`, `"skills_changed"`} {
		if !strings.Contains(tc[0].Args, want) {
			t.Fatalf("args must carry %s so every notice stays self-describing, got %s", want, tc[0].Args)
		}
	}
	if len(conv.PendingAnnouncements) != 0 {
		t.Fatalf("pending queue not cleared: %+v", conv.PendingAnnouncements)
	}
}

func TestPublishAnnouncementCapDropsOldest(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	for i := 0; i < maxPendingAnnouncements+1; i++ {
		app.publishAnnouncement("c1", newAnnouncement(
			"config_changed",
			`{"type":"config_changed"}`,
			"config changed "+strings.Repeat("x", i+1),
		))
	}

	got, err := store.Get("c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PendingAnnouncements) != maxPendingAnnouncements {
		t.Fatalf("pending queue = %d entries, want capped at %d", len(got.PendingAnnouncements), maxPendingAnnouncements)
	}
	// The very first published notice was dropped, the newest survives.
	if got.PendingAnnouncements[0].Message == "config changed x" {
		t.Fatalf("oldest entry must be dropped, got %q first", got.PendingAnnouncements[0].Message)
	}
	if last := got.PendingAnnouncements[len(got.PendingAnnouncements)-1].Message; last != "config changed "+strings.Repeat("x", maxPendingAnnouncements+1) {
		t.Fatalf("last entry = %q, want the newest notice kept", last)
	}
}

func TestDrainAnnouncementsEmptyQueueIsNoop(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}

	applied, err := app.drainAnnouncements(run)
	if err != nil {
		t.Fatalf("drainAnnouncements: %v", err)
	}
	if applied {
		t.Fatal("empty queue must not inject anything")
	}
	if len(conv.Messages) != 0 {
		t.Fatalf("no message must be injected, got %d", len(conv.Messages))
	}
}

func TestPublishAnnouncementPersistsPending(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	app.publishAnnouncement("c1", newAnnouncement("config_changed", `{"type":"config_changed"}`, "config changed"))

	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("pending queue = %+v, want 1 entry", conv.PendingAnnouncements)
	}
	pa := conv.PendingAnnouncements[0]
	if pa.Type != "config_changed" || pa.Message != "config changed" {
		t.Fatalf("pending entry = %+v, want config_changed with message", pa)
	}
}

func TestPublishAnnouncementDedupsIdenticalAppendsDistinct(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	// Distinct content: both arrive (append, never last-write-win).
	app.publishAnnouncement("c1", newAnnouncement("config_changed", `{"type":"config_changed","changed":["subagent"]}`, "config changed 1"))
	app.publishAnnouncement("c1", newAnnouncement("config_changed", `{"type":"config_changed","changed":["provider"]}`, "config changed 2"))
	// Exact duplicate of the first: skipped so bursts never double-book.
	app.publishAnnouncement("c1", newAnnouncement("config_changed", `{"type":"config_changed","changed":["subagent"]}`, "config changed 1"))

	// Save persists a clone, so re-fetch from the store.
	got, err := store.Get("c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PendingAnnouncements) != 2 {
		t.Fatalf("pending queue = %+v, want 2 entries (deduped identical, appended distinct)", got.PendingAnnouncements)
	}
	if got.PendingAnnouncements[0].Message != "config changed 1" {
		t.Fatalf("entries[0] = %q, want first published order preserved", got.PendingAnnouncements[0].Message)
	}
	if got.PendingAnnouncements[1].Message != "config changed 2" {
		t.Fatalf("entries[1] = %q, want second distinct entry appended", got.PendingAnnouncements[1].Message)
	}
}

func TestPublishAnnouncementToAllSkipsHiddenAndSelf(t *testing.T) {
	visible := &domain.Conversation{ID: "c1"}
	hidden := &domain.Conversation{ID: "c2", Origin: domain.ConversationOriginPipeline}
	self := &domain.Conversation{ID: "c3"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": visible, "c2": hidden, "c3": self}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	app.publishAnnouncementToAll(newAnnouncement("memory_changed", `{"type":"memory_changed"}`, "memory changed"), "c3")

	if len(visible.PendingAnnouncements) != 1 {
		t.Fatalf("visible conversation pending = %+v, want 1", visible.PendingAnnouncements)
	}
	if len(hidden.PendingAnnouncements) != 0 {
		t.Fatalf("hidden conversation must be skipped, got %+v", hidden.PendingAnnouncements)
	}
	if len(self.PendingAnnouncements) != 0 {
		t.Fatalf("self conversation must be skipped, got %+v", self.PendingAnnouncements)
	}
}

func TestPublishDrainConcurrentNoLostOrDoubleInjection(t *testing.T) {
	// The per-conversation announcement lock serializes load-modify-save
	// between publishers and the worker drain: every published announcement
	// is injected exactly once, and the queue is never left behind or
	// double-injected. All publishes here are byte-identical, so the
	// exact-duplicate dedup collapses them into a single pending entry.
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}

	const publishers = 8
	const perPublisher = 10
	var wg sync.WaitGroup
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				app.publishAnnouncement("c1", newAnnouncement(
					"config_changed",
					`{"type":"config_changed"}`,
					"config changed",
				))
			}
		}(p)
	}
	wg.Wait()

	// All publishes coalesce to a single pending entry.
	got, err := store.Get("c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PendingAnnouncements) != 1 {
		t.Fatalf("pending queue = %+v, want 1 coalesced entry", got.PendingAnnouncements)
	}

	// Drain injects it exactly once.
	applied, err := app.drainAnnouncements(run)
	if err != nil {
		t.Fatalf("drainAnnouncements: %v", err)
	}
	if !applied {
		t.Fatal("expected the coalesced announcement to be injected")
	}
	// Save persists a clone, so re-fetch from the store.
	got, _ = store.Get("c1")
	if len(got.Messages) != 1 {
		t.Fatalf("messages = %d, want exactly 1 injected announcement", len(got.Messages))
	}
	if len(got.PendingAnnouncements) != 0 {
		t.Fatalf("pending queue not cleared after drain: %+v", got.PendingAnnouncements)
	}
}

func TestAddTurnMessagesDrainsPendingAnnouncements(t *testing.T) {
	conv := &domain.Conversation{ID: "c1", Messages: []domain.Message{{ID: "u0", Role: domain.RoleUser, Content: "earlier"}}}
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-1", Type: "config_changed", Args: `{"type":"config_changed"}`, Message: "config changed", CreatedAt: time.Now(),
	})
	app := &App{Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}, Bus: NewBus(), Logs: &fakeLogStore{}}

	userMsg := domain.Message{ID: "u1", Role: domain.RoleUser, Content: "hi", Status: domain.StatusDone}
	asstMsg := domain.Message{ID: "a1", Role: domain.RoleAssistant}
	app.addTurnMessages(conv, userMsg, asstMsg)

	// Order: user → announcement → assistant placeholder.
	if len(conv.Messages) != 4 {
		t.Fatalf("messages = %d, want 4 (earlier, user, announcement, assistant)", len(conv.Messages))
	}
	if conv.Messages[1].ID != "u1" {
		t.Fatalf("messages[1] = %s, want the new user message", conv.Messages[1].ID)
	}
	ann := conv.Messages[2]
	if ann.Role != domain.RoleAssistant || len(ann.ToolCalls) != 1 || ann.ToolCalls[0].Name != domain.AnnouncementToolName {
		t.Fatalf("messages[2] = %+v, want announcement tool call", ann)
	}
	if conv.Messages[3].ID != "a1" {
		t.Fatalf("messages[3] = %s, want the assistant placeholder", conv.Messages[3].ID)
	}
	if len(conv.PendingAnnouncements) != 0 {
		t.Fatalf("pending queue not drained: %+v", conv.PendingAnnouncements)
	}
}

func TestQueueAnnouncementDedupsIdenticalAppendsDistinct(t *testing.T) {
	c := &domain.Conversation{}
	// a1 and a2 carry identical content (only IDs differ): deduped.
	c.QueueAnnouncement(domain.PendingAnnouncement{ID: "a1", Type: "config_changed"})
	c.QueueAnnouncement(domain.PendingAnnouncement{ID: "a2", Type: "config_changed"})
	// Different type: appended.
	c.QueueAnnouncement(domain.PendingAnnouncement{ID: "a3", Type: "memory_changed"})
	if len(c.PendingAnnouncements) != 2 {
		t.Fatalf("pending = %+v, want 2 entries (deduped identical, appended distinct)", c.PendingAnnouncements)
	}
	if c.PendingAnnouncements[0].ID != "a1" {
		t.Fatalf("config entry = %q, want a1 (first arrival kept)", c.PendingAnnouncements[0].ID)
	}
	if c.PendingAnnouncements[1].ID != "a3" {
		t.Fatalf("memory entry = %q, want a3 appended", c.PendingAnnouncements[1].ID)
	}
}

// memSettingsStore is a minimal in-memory SettingsStore for tests.
type memSettingsStore struct {
	s domain.Settings
}

func (m *memSettingsStore) Get() domain.Settings { return m.s }
func (m *memSettingsStore) Set(s domain.Settings) error {
	m.s = s
	return nil
}

func TestHandleSettingsSetPublishesOnlyOnUserPromptChange(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{
		Conversations: store,
		Bus:           NewBus(),
		Logs:          &fakeLogStore{},
		Settings:      &memSettingsStore{s: domain.DefaultSettings()},
	}

	// Unrelated settings change: no announcement.
	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{
		MaxToolRounds: intPtr(5),
	}); rpcErr != nil {
		t.Fatalf("handleSettingsSet: %v", rpcErr.Message)
	}
	if len(conv.PendingAnnouncements) != 0 {
		t.Fatalf("unrelated settings change must not announce, got %+v", conv.PendingAnnouncements)
	}

	// Real UserPrompt change: announcement with the user_prompt surface.
	prompt := "Always answer in Indonesian."
	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{
		UserPrompt: &prompt,
	}); rpcErr != nil {
		t.Fatalf("handleSettingsSet: %v", rpcErr.Message)
	}
	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("pending = %+v, want 1 config_changed entry", conv.PendingAnnouncements)
	}
	pa := conv.PendingAnnouncements[0]
	if pa.Type != "config_changed" || !strings.Contains(pa.Args, "user_prompt") {
		t.Fatalf("pending = %+v, want config_changed with user_prompt surface", pa)
	}

	// Same value saved again: no new announcement (coalesced by type anyway,
	// but the publish must not fire at all).
	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{
		UserPrompt: &prompt,
	}); rpcErr != nil {
		t.Fatalf("handleSettingsSet: %v", rpcErr.Message)
	}
	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("unchanged UserPrompt must not publish, got %+v", conv.PendingAnnouncements)
	}
}

func intPtr(v int) *int { return &v }

// --- from restart_announcement_test.go ---

func TestShouldAnnounceRestart(t *testing.T) {
	startedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	before := startedAt.Add(-time.Hour)
	after := startedAt.Add(time.Hour)

	persisted := &domain.Conversation{
		ID: "conv_old", UpdatedAt: before,
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hi"}},
	}
	if !shouldAnnounceRestart(persisted, startedAt) {
		t.Error("conversation used before restart must announce")
	}

	fresh := &domain.Conversation{
		ID: "conv_new", UpdatedAt: after,
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hi"}},
	}
	if shouldAnnounceRestart(fresh, startedAt) {
		t.Error("conversation created after restart must not announce")
	}

	empty := &domain.Conversation{ID: "conv_empty", UpdatedAt: before}
	if shouldAnnounceRestart(empty, startedAt) {
		t.Error("empty conversation (no history) must not announce")
	}

	touched := &domain.Conversation{
		ID: "conv_announced", UpdatedAt: after,
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hi"}},
	}
	if shouldAnnounceRestart(touched, startedAt) {
		t.Error("conversation already active in this process must not re-announce")
	}

	if shouldAnnounceRestart(persisted, time.Time{}) {
		t.Error("zero startedAt must disable announcements")
	}

	hydrationOnly := &domain.Conversation{
		ID: "conv_hyd", UpdatedAt: before,
		Messages: []domain.Message{hydrationCheckpointMessage()},
	}
	if shouldAnnounceRestart(hydrationOnly, startedAt) {
		t.Error("hydration-only transcript is not durable history; must not announce")
	}
}

func TestRestartAnnouncementShape(t *testing.T) {
	app := &App{Bus: NewBus()}
	msg := app.restartAnnouncement()
	if msg.Role != domain.RoleAssistant || msg.Status != domain.StatusDone {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != domain.AnnouncementToolName {
		t.Fatalf("tool name = %q", tc.Name)
	}
	if !domain.IsAnnouncementCallID(tc.ID) {
		t.Fatalf("call id %q must use the announce- prefix", tc.ID)
	}
	if tc.Status != domain.ToolOK || tc.Output != domain.AnnouncementMessage {
		t.Fatalf("tool call must be pre-completed: %+v", tc)
	}
}

// TestAutoContinueAnnouncementShape verifies the auto-continue notice rides
// the same announcement channel as restarts: persisted assistant message,
// pre-completed tool call, self-describing args, continuation guidance as
// the pre-filled output — never a synthetic user message.
func TestAutoContinueAnnouncementShape(t *testing.T) {
	app := &App{Bus: NewBus()}
	msg := app.autoContinueAnnouncement(domain.AutoContinueDecision{ShouldContinue: true, ContinuesUsed: 2, OpenTodoCount: 3})
	if msg.Role != domain.RoleAssistant || msg.Status != domain.StatusDone {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != domain.AnnouncementToolName || !domain.IsAnnouncementCallID(tc.ID) {
		t.Fatalf("call must use the announcement channel: %+v", tc)
	}
	if tc.Status != domain.ToolOK {
		t.Fatalf("tool call must be pre-completed: %+v", tc)
	}
	if strings.TrimSpace(tc.Output) == "" {
		t.Fatalf("output must carry the continuation guidance: %+v", tc)
	}
	for _, want := range []string{`"type":"auto_continue"`, `"continues_used":2`, `"open_todos":3`} {
		if !strings.Contains(tc.Args, want) {
			t.Fatalf("args must contain %s, got %s", want, tc.Args)
		}
	}
}

// TestAddTurnMessagesInjectsRestartAnnouncement verifies the injection
// point (addTurnMessages): a conversation whose history predates the
// process gets the announcement appended between the user message and
// the assistant turn, and only once per restart.
func TestAddTurnMessagesInjectsRestartAnnouncement(t *testing.T) {
	startedAt := time.Now().UTC()
	oldUpdated := startedAt.Add(-2 * time.Hour)
	conv := &domain.Conversation{
		ID:        "conv_old",
		CreatedAt: oldUpdated,
		UpdatedAt: oldUpdated,
		Messages: []domain.Message{
			{ID: "m_old", Role: domain.RoleUser, Content: "earlier message", Status: domain.StatusDone},
			{ID: "m_old2", Role: domain.RoleAssistant, Content: "earlier reply", Status: domain.StatusDone},
		},
	}
	app := &App{startedAt: startedAt}

	app.addTurnMessages(conv,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "hello again", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)

	if len(conv.Messages) != 5 {
		t.Fatalf("messages = %d, want 5 (old 2 + user + announcement + assistant)", len(conv.Messages))
	}
	if conv.Messages[2].ID != "m_user" {
		t.Fatalf("message[2] must be the user message: %+v", conv.Messages[2])
	}
	announcement := conv.Messages[3]
	if announcement.Role != domain.RoleAssistant || len(announcement.ToolCalls) != 1 {
		t.Fatalf("message[3] must be the announcement: %+v", announcement)
	}
	tc := announcement.ToolCalls[0]
	if tc.Name != domain.AnnouncementToolName || tc.Output != domain.AnnouncementMessage {
		t.Fatalf("unexpected announcement tool call: %+v", tc)
	}
	if conv.Messages[4].ID != "m_asst" {
		t.Fatalf("message[4] must be the assistant turn message: %+v", conv.Messages[4])
	}

	// One-shot per restart: addTurnMessages bumped UpdatedAt past
	// startedAt, so the next turn in the same process gets nothing.
	app.addTurnMessages(conv,
		domain.Message{ID: "m_user2", Role: domain.RoleUser, Content: "again", Status: domain.StatusDone},
		domain.Message{ID: "m_asst2", Role: domain.RoleAssistant},
	)
	if len(conv.Messages) != 7 {
		t.Fatalf("messages after second turn = %d, want 7 (no repeat announcement)", len(conv.Messages))
	}
	for _, m := range conv.Messages[5:] {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				t.Fatalf("second turn must not repeat the announcement: %+v", tc)
			}
		}
	}
}

// TestAddTurnMessagesSkipsFreshOrEmpty verifies that conversations
// created after startup or without history never get the announcement.
func TestAddTurnMessagesSkipsFreshOrEmpty(t *testing.T) {
	startedAt := time.Now().UTC()
	app := &App{startedAt: startedAt}

	fresh := &domain.Conversation{
		ID: "conv_fresh", CreatedAt: startedAt.Add(time.Minute), UpdatedAt: startedAt.Add(time.Minute),
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hi", Status: domain.StatusDone}},
	}
	app.addTurnMessages(fresh,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "first message", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	for _, m := range fresh.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				t.Fatalf("fresh conversation must not get an announcement: %+v", tc)
			}
		}
	}

	empty := &domain.Conversation{ID: "conv_empty", UpdatedAt: startedAt.Add(-time.Hour)}
	app.addTurnMessages(empty,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "hi", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	if empty.Messages[0].ID != "m_user" || empty.Messages[len(empty.Messages)-1].ID != "m_asst" {
		t.Fatalf("empty conversation messages = %+v, want user first and assistant last", empty.Messages)
	}
	for _, m := range empty.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				t.Fatalf("empty conversation must not get an announcement: %+v", tc)
			}
		}
	}
}

func TestAddTurnMessagesDerivesTitleFromFirstUserMessage(t *testing.T) {
	conv := domain.NewConversation("conv_title", "Untitled")
	app := &App{}
	app.addTurnMessages(conv,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "Fix the room history window", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	if conv.Title != "Fix the room history window" {
		t.Fatalf("title = %q, want first user message", conv.Title)
	}

	conv.Title = "My custom room"
	app.addTurnMessages(conv,
		domain.Message{ID: "m_user2", Role: domain.RoleUser, Content: "Do not replace this title", Status: domain.StatusDone},
		domain.Message{ID: "m_asst2", Role: domain.RoleAssistant},
	)
	if conv.Title != "My custom room" {
		t.Fatalf("custom title was overwritten: %q", conv.Title)
	}
}

// --- from nudge_user_test.go ---

func TestHasUserMessage(t *testing.T) {
	cases := []struct {
		name     string
		msgs     []ChatMessage
		expected bool
	}{
		{"empty", nil, false},
		{"only assistant", []ChatMessage{{Role: "assistant", Content: "hi"}}, false},
		{"only system", []ChatMessage{{Role: "system", Content: "sys"}}, false},
		{"only tool", []ChatMessage{{Role: "tool", ToolResult: &ToolResult{}}}, false},
		{"has user", []ChatMessage{{Role: "user", Content: "hello"}}, true},
		{"user after assistant", []ChatMessage{
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "hello"},
		}, true},
		{"user empty content", []ChatMessage{{Role: "user", Content: ""}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasUserMessage(c.msgs); got != c.expected {
				t.Errorf("hasUserMessage(%s) = %v, want %v", c.name, got, c.expected)
			}
		})
	}
}

func TestNeedsUserMessageAtEnd(t *testing.T) {
	cases := []struct {
		name string
		msgs []ChatMessage
		want bool
	}{
		{name: "empty", want: true},
		{name: "system only", msgs: []ChatMessage{{Role: "system"}}, want: true},
		{name: "user last", msgs: []ChatMessage{{Role: "user"}}, want: false},
		{name: "assistant last", msgs: []ChatMessage{{Role: "user"}, {Role: "assistant", Content: "previous answer"}}, want: true},
		{name: "tool result last", msgs: []ChatMessage{{Role: "user"}, {Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_1"}}}, {Role: "tool", ToolResult: &ToolResult{ToolCallID: "call_1"}}}, want: false},
		{name: "tool without user", msgs: []ChatMessage{{Role: "tool", ToolResult: &ToolResult{}}}, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := needsUserMessageAtEnd(c.msgs); got != c.want {
				t.Fatalf("needsUserMessageAtEnd(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// --- from system_prompt_stability_test.go ---

type callbackAcpRuntime struct {
	AcpRuntime
	onDone func(*domain.AcpRun)
}

func (runtime *callbackAcpRuntime) SetCallbacks(
	_ func(*domain.AcpRun),
	onDone func(*domain.AcpRun),
	_ func(*domain.AcpRun, domain.AcpPermissionRequest),
	_ func(*domain.AcpRun, string),
) {
	runtime.onDone = onDone
}

// TestAppendContinuationTool verifies the interrupted-response notice is
// delivered on the shared announcement channel as an ephemeral synthetic
// tool call + result, never as a system prompt mutation.
func TestAppendContinuationTool(t *testing.T) {
	base := []ChatMessage{{Role: "user", Content: "hi"}}
	msgs := appendContinuationTool(base)

	if len(msgs) != len(base)+2 {
		t.Fatalf("want 2 synthetic messages appended, got %d total", len(msgs))
	}
	asst := msgs[len(base)]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 1 {
		t.Fatalf("synthetic assistant message wrong: %+v", asst)
	}
	call := asst.ToolCalls[0]
	if call.Name != domain.AnnouncementToolName {
		t.Fatalf("tool name = %q, want %q", call.Name, domain.AnnouncementToolName)
	}
	if !domain.IsAnnouncementCallID(call.ID) {
		t.Fatalf("call id %q must use the announce- prefix", call.ID)
	}
	if call.Status != domain.ToolOK || call.Output != domain.AnnouncementInterruptedMessage {
		t.Fatalf("tool call must be pre-completed: %+v", call)
	}
	res := msgs[len(msgs)-1]
	if res.Role != "tool" || res.ToolResult == nil {
		t.Fatalf("last message must be the tool result: %+v", res)
	}
	if res.ToolResult.ToolCallID != call.ID || res.ToolResult.Content != domain.AnnouncementInterruptedMessage {
		t.Fatalf("tool result must reference the same call with the same content: %+v", res.ToolResult)
	}
}

// TestAcpDelegationDescription verifies the delegation guidance renders
// only when agents are enabled and stays out of the system prompt.
func TestAcpDelegationDescription(t *testing.T) {
	if got := AcpDelegationDescription(nil); got != "" {
		t.Fatalf("nil agents must produce empty description, got %q", got)
	}
	if got := AcpDelegationDescription([]*domain.AcpAgent{}); got != "" {
		t.Fatalf("no agents must produce empty description, got %q", got)
	}

	agents := []*domain.AcpAgent{
		{ID: "acp_1", Name: "Cursor", Enabled: true},
		{ID: "acp_2", Name: "Claude Code", Enabled: true},
	}
	desc := AcpDelegationDescription(agents)
	if !strings.Contains(desc, "Cursor (acp_1)") || !strings.Contains(desc, "Claude Code (acp_2)") {
		t.Fatalf("description must list enabled agents: %q", desc)
	}
	if !strings.Contains(desc, "Default ACP agent: Cursor") {
		t.Fatalf("description must name the default agent: %q", desc)
	}
}

func TestHeadlessAgentKindsUseDistinctSystemPrompts(t *testing.T) {
	conv := &domain.Conversation{}
	delegate := buildSystemPromptForRun(&TurnRun{Headless: true, ToolKind: AgentDelegate}, conv, "")
	automation := buildSystemPromptForRun(&TurnRun{Headless: true, ToolKind: AgentAutomation}, conv, "")
	interactive := buildSystemPrompt(conv, "")

	if delegate == "" || !strings.Contains(delegate, "internal delegate agent") {
		t.Fatalf("delegate prompt must identify its internal delegate role: %q", delegate)
	}
	if delegate == interactive || delegate == automation {
		t.Fatal("delegate prompt must be distinct from interactive and automation prompts")
	}
	if automation == interactive {
		t.Fatal("automation prompt must remain distinct from interactive prompt")
	}
}

// TestSubagentResultMessageShape verifies the synthetic subagent_result
// message mirrors the announcement pattern: pre-completed tool call with
// the full result, persisted as an assistant message.
func TestSubagentResultMessageShape(t *testing.T) {
	run := &domain.AcpRun{
		TaskState:        domain.TaskState[domain.AcpRunStatus]{ID: "run_abc", Status: domain.AcpRunCompleted},
		ParentToolCallID: "call_parent",
		Transcript: []domain.AcpTranscriptChunk{
			{Kind: "text", Text: "work done"},
		},
	}
	app := &App{}
	msg := app.subagentResultMessage(run, "/data/run_abc.json", domain.ToolOK)

	if msg.Role != domain.RoleAssistant || msg.Status != domain.StatusDone {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != domain.SubagentResultToolName {
		t.Fatalf("tool name = %q, want %q", tc.Name, domain.SubagentResultToolName)
	}
	if !domain.IsSubagentResultCallID(tc.ID) {
		t.Fatalf("call id %q must use the subagent-result- prefix", tc.ID)
	}
	if tc.Args != domain.SubagentResultArgs(run.ID) {
		t.Fatalf("args must carry the run id: %q", tc.Args)
	}
	if tc.Status != domain.ToolOK {
		t.Fatalf("completed run must be ToolOK, got %v", tc.Status)
	}
	if !strings.Contains(tc.Output, "work done") || !strings.Contains(tc.Output, "output_path: /data/run_abc.json") {
		t.Fatalf("output must carry the full subagent result: %q", tc.Output)
	}
}

// TestCompleteSubagentRun verifies the atomic completion path: the
// original `subagent` tool call transitions to a brief terminal status
// while the full result is delivered via the synthetic subagent_result
// message, both in one conversation save.
func TestCompleteSubagentRun(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{
				ID: "m1", Role: domain.RoleUser, Content: "delegate", Status: domain.StatusDone,
			},
			{
				ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{
					ID: "call_parent", Name: "subagent", Status: domain.ToolRunning,
					Args:   `{"prompt":"inspect the workspace"}`,
					Output: "---\nstatus: starting",
				}},
			},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus()}
	_, events, unsubscribe := app.Bus.Subscribe()
	defer unsubscribe()

	run := &domain.AcpRun{
		TaskState:        domain.TaskState[domain.AcpRunStatus]{ID: "run_xyz", Status: domain.AcpRunCompleted},
		ParentToolCallID: "call_parent",
		Transcript:       []domain.AcpTranscriptChunk{{Kind: "text", Text: "done"}},
	}
	turnLock := app.conversationTurnLock("c1")
	turnLock.Lock()
	err := app.completeSubagentRunLocked("c1", run.ParentToolCallID, domain.ToolOK, run, "/data/run_xyz.json")
	turnLock.Unlock()
	if err != nil {
		t.Fatalf("completeSubagentRunLocked: %v", err)
	}

	saved := store.convs["c1"]
	if len(saved.Messages) != 3 {
		t.Fatalf("want 3 messages after completion, got %d", len(saved.Messages))
	}
	original := saved.Messages[1].ToolCalls[0]
	if original.Status != domain.ToolOK {
		t.Fatalf("original tool call must be ToolOK, got %v", original.Status)
	}
	if strings.Contains(original.Output, "done") || !strings.Contains(original.Output, "subagent_result") {
		t.Fatalf("original tool call must carry only the brief pointer: %q", original.Output)
	}
	synthetic := saved.Messages[2]
	if synthetic.Role != domain.RoleAssistant || len(synthetic.ToolCalls) != 1 {
		t.Fatalf("synthetic message missing: %+v", synthetic)
	}
	stc := synthetic.ToolCalls[0]
	if stc.Name != domain.SubagentResultToolName || !domain.IsSubagentResultCallID(stc.ID) {
		t.Fatalf("synthetic tool call wrong: %+v", stc)
	}
	if !strings.Contains(stc.Output, "done") {
		t.Fatalf("synthetic tool call must carry the full result: %q", stc.Output)
	}
	select {
	case event := <-events:
		if event.Type != contracts.EventToolCompleted {
			t.Fatalf("event type = %q, want %q", event.Type, contracts.EventToolCompleted)
		}
		var completed contracts.ToolCompletedEvent
		if err := json.Unmarshal(event.Payload, &completed); err != nil {
			t.Fatalf("decode tool completion: %v", err)
		}
		if string(completed.Args) != `{"prompt":"inspect the workspace"}` {
			t.Fatalf("completion args = %s, want original subagent args", completed.Args)
		}
		if completed.Presentation == nil || !strings.Contains(completed.Presentation.Request, "inspect the workspace") {
			t.Fatalf("completion presentation must retain the request: %+v", completed.Presentation)
		}
	case <-time.After(time.Second):
		t.Fatal("subagent completion event was not published")
	}
}

// TestCompleteSubagentRunFailedStatus verifies failed/cancelled runs flip
// the synthetic tool call to ToolFailed while still delivering the full
// result.
func TestCompleteSubagentRunFailedStatus(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "delegate", Status: domain.StatusDone},
			{
				ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{ID: "call_parent", Name: "subagent", Status: domain.ToolRunning}},
			},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus()}

	run := &domain.AcpRun{
		TaskState:        domain.TaskState[domain.AcpRunStatus]{ID: "run_fail", Status: domain.AcpRunFailed, Error: "boom"},
		ParentToolCallID: "call_parent",
	}
	turnLock := app.conversationTurnLock("c1")
	turnLock.Lock()
	err := app.completeSubagentRunLocked("c1", run.ParentToolCallID, domain.ToolFailed, run, "")
	turnLock.Unlock()
	if err != nil {
		t.Fatalf("completeSubagentRunLocked: %v", err)
	}

	saved := store.convs["c1"]
	if saved.Messages[1].ToolCalls[0].Status != domain.ToolFailed {
		t.Fatalf("original tool call must be ToolFailed, got %v", saved.Messages[1].ToolCalls[0].Status)
	}
	stc := saved.Messages[2].ToolCalls[0]
	if stc.Status != domain.ToolFailed {
		t.Fatalf("synthetic tool call must be ToolFailed, got %v", stc.Status)
	}
	if !strings.Contains(stc.Output, "boom") {
		t.Fatalf("synthetic output must include the error: %q", stc.Output)
	}
}

func TestCompleteSubagentRunWaitsForActiveTurnMutation(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "delegate", Status: domain.StatusDone},
			{
				ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{ID: "call_parent", Name: "subagent", Status: domain.ToolRunning}},
			},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus()}
	run := &domain.AcpRun{
		TaskState:        domain.TaskState[domain.AcpRunStatus]{ID: "run_done", Status: domain.AcpRunCompleted},
		ParentToolCallID: "call_parent",
		Transcript:       []domain.AcpTranscriptChunk{{Kind: "text", Text: "done"}},
	}

	turnLock := app.conversationTurnLock("c1")
	turnLock.Lock()
	completed := make(chan struct{})
	go func() {
		turnLock.Lock()
		defer turnLock.Unlock()
		if err := app.completeSubagentRunLocked("c1", run.ParentToolCallID, domain.ToolOK, run, "/data/run_done.json"); err != nil {
			t.Errorf("completeSubagentRunLocked: %v", err)
		}
		close(completed)
	}()

	select {
	case <-completed:
		t.Fatal("subagent completion mutated the conversation during an active turn")
	case <-time.After(20 * time.Millisecond):
	}

	store.convs["c1"].AddMessage(domain.Message{
		ID: "m3", Role: domain.RoleAssistant, Content: "latest parent round", Status: domain.StatusDone,
	})
	turnLock.Unlock()

	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("subagent completion did not resume after the active turn released the conversation")
	}

	saved := store.convs["c1"]
	if len(saved.Messages) != 4 {
		t.Fatalf("messages = %d, want parent round and synthetic subagent result preserved", len(saved.Messages))
	}
	if saved.Messages[2].Content != "latest parent round" {
		t.Fatalf("parent round was overwritten: %+v", saved.Messages)
	}
}

func TestAcpDoneCallbackDoesNotBlockActiveParentTool(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "delegate", Status: domain.StatusDone},
			{
				ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{ID: "call_parent", Name: "subagent", Status: domain.ToolRunning}},
			},
		},
	}
	runtime := &callbackAcpRuntime{}
	app := NewApp(Deps{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Acp:           runtime,
	})
	_, events, unsubscribe := app.Bus.Subscribe()
	defer unsubscribe()
	run := &domain.AcpRun{
		TaskState:        domain.TaskState[domain.AcpRunStatus]{ID: "run_done", Status: domain.AcpRunCompleted},
		ConversationID:   "c1",
		ParentToolCallID: "call_parent",
		Transcript:       []domain.AcpTranscriptChunk{{Kind: "text", Text: "done"}},
	}

	turnLock := app.conversationTurnLock("c1")
	turnLock.Lock()
	returned := make(chan struct{})
	go func() {
		runtime.onDone(run)
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		turnLock.Unlock()
		t.Fatal("ACP completion callback blocked on the active parent turn")
	}
	turnLock.Unlock()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == contracts.EventToolCompleted {
				return
			}
		case <-deadline.C:
			t.Fatal("asynchronous ACP completion did not resume after the parent turn")
		}
	}
}

// --- from capability_registry_test.go ---

func TestCapabilityBuiltinAvailable(t *testing.T) {
	reg := NewCapabilityRegistry()
	b, err := reg.Resolve(context.Background(), "filesystem.read", domain.DefaultAutoStart)
	if err != nil {
		t.Fatal(err)
	}
	if b.Kind != domain.CapabilityBuiltin || b.Status != domain.CapAvailable {
		t.Fatalf("%+v", b)
	}
	out, err := reg.Execute(context.Background(), b, json.RawMessage(`{"path":"/no/such"}`))
	if err == nil {
		t.Fatalf("expected read error, got %s", out)
	}
}

func TestDisabledProviderBlocksNotFails(t *testing.T) {
	mem := NewAutomationStore()
	reg := NewCapabilityRegistry()
	reg.State = ProviderStateMem{AutomationStore: mem}
	reg.Plugins = &capPluginStore{items: []*domain.Plugin{{
		Manifest: domain.PluginManifest{ID: "mail-mcp", Name: "mail"},
	}}}
	reg.MCP = &capMCP{tools: map[string][]contracts.MCPToolDTO{
		"plugin:mail-mcp": {{Name: "email_read"}},
	}}
	_ = reg.SetDisabled(context.Background(), "mail-mcp", true)
	b, err := reg.Resolve(context.Background(), "email.read", domain.DefaultAutoStart)
	if err != nil && b.Status != domain.CapDisabled {
		t.Fatal(err)
	}
	if b.Status != domain.CapDisabled {
		t.Fatalf("status = %s", b.Status)
	}
	if domain.MapAvailability(b.Status, true) != domain.AvailBlocked {
		t.Fatal("disabled maps to blocked, not failed")
	}
}

type capPluginStore struct{ items []*domain.Plugin }

func (s *capPluginStore) List() ([]*domain.Plugin, error) { return s.items, nil }
func (s *capPluginStore) Get(id string) (*domain.Plugin, error) {
	for _, p := range s.items {
		if p.Manifest.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (s *capPluginStore) Install(string) (*domain.Plugin, error) { return nil, fmt.Errorf("no") }
func (s *capPluginStore) Uninstall(string) error                 { return nil }
func (s *capPluginStore) Save(*domain.Plugin) error              { return nil }
func (s *capPluginStore) Delete(string) error                    { return nil }

type capMCP struct {
	tools map[string][]contracts.MCPToolDTO
}

func (m *capMCP) ToolsFor(id string) ([]contracts.MCPToolDTO, bool) {
	t, ok := m.tools[id]
	return t, ok
}
func (m *capMCP) Connect(_ context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error) {
	tools, _ := m.ToolsFor(p.Manifest.MCPServerID())
	return tools, nil
}
func (m *capMCP) Drop(string) {}

// --- from roundstream_test.go ---

// TestRoundStreamReplayCursor verifies the idempotent resume contract: a
// subscriber attaching with after=<seq> receives only newer frames, in
// order, with monotonic seq numbers.
func TestRoundStreamReplayCursor(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "a")
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "b")
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "c")

	sub, err := reg.Subscribe(context.Background(), "r", "m", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var got []string
	for i := 0; i < 2; i++ {
		select {
		case f := <-sub.Frames():
			got = append(got, f.Text)
			if i == 0 && f.Seq != 2 {
				t.Fatalf("first replayed seq = %d, want 2", f.Seq)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for replay frames")
		}
	}
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("replay after=1 = %v, want [b c]", got)
	}
}

func TestRoundStreamPublishActivityCarriesNoToolArguments(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.PublishActivity("r", "m", 1, "call_1", "file_read", contracts.RoundActivityToolCall)

	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case frame := <-sub.Frames():
		if frame.Kind != contracts.RoundDeltaActivity || frame.Activity != contracts.RoundActivityToolCall {
			t.Fatalf("activity frame = %+v", frame)
		}
		if frame.ToolCallID != "call_1" || frame.Name != "file_read" {
			t.Fatalf("activity metadata = %+v", frame)
		}
		if len(frame.Args) != 0 || frame.Text != "" || frame.Presentation != nil {
			t.Fatalf("activity frame leaked tool payload: %+v", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for activity frame")
	}
}

// TestRoundStreamSealDeliversDone verifies the terminal frame reaches
// subscribers and the seal is idempotent.
func TestRoundStreamSealDeliversDone(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "a")

	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{
		State: contracts.RoundStateDone, RunID: "r", MessageID: "m", Round: 1,
		Next: &contracts.RoundRef{RunID: "r", MessageID: "m2", Round: 2},
	})
	// Second seal is a no-op.
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{State: contracts.RoundStateError, RunID: "r", MessageID: "m"})

	select {
	case <-sub.Done():
	case <-time.After(time.Second):
		t.Fatal("done never closed")
	}
	done := sub.DoneFrame()
	if done.State != contracts.RoundStateDone || done.Next == nil || done.Next.MessageID != "m2" {
		t.Fatalf("done frame = %+v", done)
	}
	if done.LastSeq != 1 {
		t.Fatalf("done last_seq = %d, want 1", done.LastSeq)
	}
	// Drain the replayed delta then verify no more frames arrive.
	select {
	case f := <-sub.Frames():
		if f.Text != "a" {
			t.Fatalf("delta = %+v", f)
		}
	default:
		t.Fatal("expected the replayed delta")
	}
}

// TestRoundStreamLateJoinReplay verifies a consumer attaching after the
// round has fully sealed still receives the complete delta tail plus the
// terminal frame (within the sealed TTL).
func TestRoundStreamLateJoinReplay(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "x")
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "y")
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{State: contracts.RoundStateDone, RunID: "r", MessageID: "m", Round: 1})

	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var got string
	for i := 0; i < 2; i++ {
		select {
		case f := <-sub.Frames():
			got += f.Text
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for replay")
		}
	}
	if got != "xy" {
		t.Fatalf("late join replay = %q, want xy", got)
	}
	select {
	case <-sub.Done():
	default:
		t.Fatal("sealed stream should close done immediately")
	}
}

// TestRoundStreamSealedOnlyRound verifies rounds that produce no deltas at
// all (e.g. a tool-only round) still get a stream via Seal, so consumers
// receive the terminal frame instead of a 404.
func TestRoundStreamSealedOnlyRound(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{State: contracts.RoundStateDone, RunID: "r", MessageID: "m", Round: 1})

	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case <-sub.Done():
	default:
		t.Fatal("done not closed for sealed-only round")
	}
}

// TestRoundStreamWaitForLateCreate verifies a consumer that attaches before
// the round publishes (between agent.turn.started and the first delta) waits
// and then receives the live stream.
func TestRoundStreamWaitForLateCreate(t *testing.T) {
	reg := NewRoundStreamRegistry()
	result := make(chan *RoundStreamSub, 1)
	errs := make(chan error, 1)
	go func() {
		sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
		if err != nil {
			errs <- err
			return
		}
		result <- sub
	}()
	time.Sleep(50 * time.Millisecond)
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "z")

	select {
	case sub := <-result:
		defer sub.Close()
		select {
		case f := <-sub.Frames():
			if f.Text != "z" {
				t.Fatalf("delta = %+v", f)
			}
		case <-time.After(time.Second):
			t.Fatal("no live frame after wait")
		}
	case err := <-errs:
		t.Fatalf("subscribe failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe never returned")
	}
}

// TestRoundStreamNotFound verifies a subscribe for a stream that never
// appears fails (ctx cancel path here; the registry TTL covers the timeout
// path).
func TestRoundStreamNotFound(t *testing.T) {
	reg := NewRoundStreamRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := reg.Subscribe(ctx, "r", "nope", 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

// TestRoundStreamPublishAfterSealDropped verifies deltas published after a
// seal are dropped (the terminal frame is authoritative).
func TestRoundStreamPublishAfterSealDropped(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{State: contracts.RoundStateDone, RunID: "r", MessageID: "m", Round: 1})
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "late")
	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case f := <-sub.Frames():
		t.Fatalf("unexpected frame after seal: %+v", f)
	case <-time.After(100 * time.Millisecond):
		// pass: no frames
	}
}

// TestRoundStreamSlowSubscriberBufferHeadroom verifies a subscriber that
// pauses reading still receives every frame up to roundStreamSubBuf, and
// only drops after the buffer is full. The drop is non-blocking on the
// publisher (turn goroutine never stalls) and is recoverable by
// reconnecting with after=<lastSeq> — the replay buffer still holds the
// missed frames as long as its head has not been trimmed.
//
// This pins the design: replay-sized subscriber headroom buys more tolerance
// for brief browser-side stalls (other tab in focus, GC pause, devtools open)
// without deadlocking a replay or falling back to a snapshot refresh.
func TestRoundStreamSlowSubscriberBufferHeadroom(t *testing.T) {
	reg := NewRoundStreamRegistry()
	// Bootstrap the stream so the subscriber attaches to a known stream
	// (Subscribe otherwise waits for a stream to appear and would time out
	// after roundStreamWaitTTL).
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "warmup")
	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	// Drain the warmup so the live channel is empty.
	select {
	case <-sub.Frames():
	case <-time.After(time.Second):
		t.Fatal("warmup frame never delivered")
	}

	// Publish far more than the buffer can hold, without the subscriber
	// reading. The publisher must not block: every Publish must return
	// promptly even though the channel is overflowing.
	const total = roundStreamSubBuf + 1024
	done := make(chan struct{})
	go func() {
		for i := 0; i < total; i++ {
			reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "x")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher blocked while subscriber was not reading")
	}

	// Now drain — we should receive at most roundStreamSubBuf frames
	// (overflow was dropped) in seq order, with the highest seq = total
	// (the buffer captured the last roundStreamSubBuf publishes).
	received := 0
	var highestSeq int64
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case f := <-sub.Frames():
			received++
			highestSeq = f.Seq
		case <-deadline:
			break loop
		}
	}
	if received == 0 {
		t.Fatal("subscriber received no frames despite publishes")
	}
	if received > roundStreamSubBuf {
		t.Fatalf("subscriber received %d frames, exceeds buffer %d", received, roundStreamSubBuf)
	}
	// The buffer captured the tail — the last roundStreamSubBuf frames
	// (seq 1+1024 .. seq 1+1024+roundStreamSubBuf, modulo 1+5120 total).
	// The replay buffer at the registry holds all 5120 published frames
	// (well under roundStreamFrameCap=8192), so the head of the live
	// subscriber's channel starts at the first frame that fit.
	// (We do not assert the exact lowest received seq because the live
	// channel's policy is "drop the oldest, keep the newest" — verifying
	// the count cap is what pins the design.)
	if highestSeq < int64(total) {
		t.Logf("subscriber got up to seq %d of %d (headroom %d, drops after)",
			highestSeq, total, roundStreamSubBuf)
	}
}

// TestRoundStreamSlowSubscriberGapDetectable verifies that a subscriber
// which missed frames can still detect the gap and fall back: the
// persisted replay buffer holds the head, so a new subscriber attaching
// with after=<receivedSeq> gets the tail cleanly. The dropped frames
// (between receivedSeq and the head of the replay buffer) are the
// "gap" the client detects by seeing the next replayed Seq > after+1.
func TestRoundStreamSlowSubscriberGapDetectable(t *testing.T) {
	reg := NewRoundStreamRegistry()
	// Publish a small head — within the subscriber buffer — before any
	// subscriber attaches. The head goes into the replay buffer; the
	// subscriber will drain it on attach without blocking.
	const head = 128
	for i := 0; i < head; i++ {
		reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "h")
	}
	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	// Drain the head from the subscriber's channel.
	for i := 0; i < head; i++ {
		select {
		case <-sub.Frames():
		case <-time.After(time.Second):
			t.Fatalf("drained only %d of %d head frames", i, head)
		}
	}
	// The subscriber has acknowledged seq == head. Now publish a few more
	// frames without reading. They sit in the subscriber's buffered channel.
	const tail = 32
	for i := 0; i < tail; i++ {
		reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "t")
	}
	// A new subscriber attaching with after=<head> gets the live tail
	// cleanly — no gap visible to a fresh attacher, because the publisher
	// is the source of truth for in-flight seqs.
	sub2, err := reg.Subscribe(context.Background(), "r", "m", int64(head))
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()
	first, ok := <-sub2.Frames()
	if !ok {
		t.Fatal("new subscriber received no live frames")
	}
	// first.Seq must be > head (the head replay drained already) and
	// contiguous — no gap visible to a fresh attacher.
	if first.Seq < int64(head+1) {
		t.Fatalf("first live frame seq = %d, want > %d", first.Seq, head)
	}
}

func TestRoundStreamLargeReplayDoesNotBlockSubscription(t *testing.T) {
	reg := NewRoundStreamRegistry()
	for i := 0; i < roundStreamFrameCap; i++ {
		reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "x")
	}

	result := make(chan *RoundStreamSub, 1)
	go func() {
		sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
		if err == nil {
			result <- sub
		}
	}()

	select {
	case sub := <-result:
		defer sub.Close()
		for i := 0; i < roundStreamFrameCap; i++ {
			select {
			case <-sub.Frames():
			case <-time.After(time.Second):
				t.Fatalf("large replay stalled after %d frames", i)
			}
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("subscription blocked when replay exceeded the live subscriber queue")
	}
}

// --- from ask_question_service_test.go ---

func TestAskQuestionService_PendingForConversation(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{Question: "Q?", Options: []domain.AskQuestionOption{{ID: "a", Label: "A"}}}
	_, _ = s.Ask("run-1", "call-1", "conv-1", req)
	_, _ = s.Ask("run-1", "call-2", "conv-1", req)
	_, _ = s.Ask("run-2", "call-3", "conv-2", req)

	got := s.PendingForConversation("conv-1")
	if len(got) != 2 {
		t.Fatalf("PendingForConversation(conv-1) = %d asks, want 2", len(got))
	}
	for _, p := range got {
		if p.ConversationID != "conv-1" || p.Req.Question != "Q?" {
			t.Fatalf("unexpected pending ask: %+v", p)
		}
	}
	// Answering removes it from the pending list.
	if _, err := s.Answer("run-1", "call-1", domain.AskQuestionAnswer{Via: domain.AskAnswerViaOption, OptionIDs: []string{"a"}}); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if len(s.PendingForConversation("conv-1")) != 1 {
		t.Fatalf("after answer, conv-1 should have 1 pending ask")
	}
	if len(s.PendingForConversation("conv-2")) != 1 {
		t.Fatalf("conv-2 should still have 1 pending ask")
	}
	if len(s.PendingForConversation("missing")) != 0 {
		t.Fatalf("unknown conversation should have 0 pending asks")
	}
}

func TestAskQuestionService_Answer(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{
		Question:      "Which option?",
		Options:       []domain.AskQuestionOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
		AllowFreeText: true,
	}
	ch, err := s.Ask("run-1", "call-1", "conv-1", req)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if !s.HasPending("run-1", "call-1") {
		t.Fatal("expected pending ask")
	}
	result, err := s.Answer("run-1", "call-1", domain.AskQuestionAnswer{
		Via:       domain.AskAnswerViaOption,
		OptionIDs: []string{"a"},
	})
	if err != nil {
		t.Fatalf("Answer failed: %v", err)
	}
	if result.Answer != "A" {
		t.Fatalf("answer = %q, want %q", result.Answer, "A")
	}
	got := <-ch
	if !got.OK || got.Answer != "A" {
		t.Fatalf("channel result = %+v, want OK=true Answer=A", got)
	}
	if s.HasPending("run-1", "call-1") {
		t.Fatal("expected no pending ask after answer")
	}
}

func TestAskQuestionService_Cancel(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{
		Question: "Q?",
		Options:  []domain.AskQuestionOption{{ID: "a", Label: "A"}},
	}
	ch, _ := s.Ask("run-1", "call-1", "conv-1", req)
	s.Cancel("run-1", "call-1", "user cancelled")
	got := <-ch
	if got.OK {
		t.Fatalf("expected OK=false after cancel, got %+v", got)
	}
}

func TestAskQuestionService_RejectRun(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{
		Question: "Q?",
		Options:  []domain.AskQuestionOption{{ID: "a", Label: "A"}},
	}
	ch1, _ := s.Ask("run-1", "call-1", "conv-1", req)
	ch2, _ := s.Ask("run-1", "call-2", "conv-1", req)
	_, _ = s.Ask("run-2", "call-3", "conv-2", req)
	s.RejectRun("run-1", "turn ended")
	got1 := <-ch1
	got2 := <-ch2
	if got1.OK || got2.OK {
		t.Fatalf("expected both rejected, got %+v and %+v", got1, got2)
	}
	if !s.HasPending("run-2", "call-3") {
		t.Fatal("run-2 ask should still be pending")
	}
}

func TestAskQuestionService_DuplicateAsk(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{
		Question: "Q?",
		Options:  []domain.AskQuestionOption{{ID: "a", Label: "A"}},
	}
	_, err := s.Ask("run-1", "call-1", "conv-1", req)
	if err != nil {
		t.Fatalf("first Ask failed: %v", err)
	}
	_, err = s.Ask("run-1", "call-1", "conv-1", req)
	if err == nil {
		t.Fatal("expected error on duplicate Ask")
	}
}

func TestAskQuestionService_AnswerNotFound(t *testing.T) {
	s := NewAskQuestionService()
	_, err := s.Answer("no-run", "no-call", domain.AskQuestionAnswer{})
	if err == nil {
		t.Fatal("expected error for unknown answer")
	}
}

func TestAskQuestionService_OnAsk(t *testing.T) {
	s := NewAskQuestionService()
	var gotRun, gotCall, gotConv string
	var gotReq domain.AskQuestionRequest
	s.SetOnAsk(func(runID, callID, convID string, req domain.AskQuestionRequest) {
		gotRun = runID
		gotCall = callID
		gotConv = convID
		gotReq = req
	})
	req := domain.AskQuestionRequest{
		Question: "Q?",
		Options:  []domain.AskQuestionOption{{ID: "a", Label: "A"}},
	}
	_, _ = s.Ask("run-1", "call-1", "conv-1", req)
	if gotRun != "run-1" || gotCall != "call-1" || gotConv != "conv-1" {
		t.Fatalf("callback got run=%s call=%s conv=%s", gotRun, gotCall, gotConv)
	}
	if gotReq.Question != "Q?" {
		t.Fatalf("callback got question=%q", gotReq.Question)
	}
}

func TestIsLearnable400(t *testing.T) {
	if !isLearnable400(&domain.ProviderError{StatusCode: 400, Err: errors.New("bad request")}) {
		t.Error("400 should be learnable")
	}
	if isLearnable400(&domain.ProviderError{StatusCode: 429, Err: errors.New("rate limit")}) {
		t.Error("429 should not be learnable")
	}
	if isLearnable400(&domain.ProviderError{StatusCode: 500, Err: errors.New("server error")}) {
		t.Error("500 should not be learnable")
	}
	if isLearnable400(errors.New("plain error")) {
		t.Error("non-ProviderError should not be learnable")
	}
}

func TestExtractErrBody(t *testing.T) {
	if got := extractErrBody(nil); got != "" {
		t.Errorf("extractErrBody(nil) = %q, want empty", got)
	}
	if got := extractErrBody(errors.New("plain")); got != "plain" {
		t.Errorf("extractErrBody(plain) = %q, want plain", got)
	}
	if got := extractErrBody(&domain.ProviderError{StatusCode: 400, Err: errors.New("unsupported")}); got != "unsupported" {
		t.Errorf("extractErrBody(upstream) = %q, want unsupported", got)
	}
}

// --- from agent_round_test.go ---

func TestEstimateRequestTokensIncludesSystemAndTools(t *testing.T) {
	system := "system prompt content"
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	tools := []ToolDef{{Name: "exec", Description: "run a command"}}
	got := estimateRequestTokens(system, messages, tools)
	if got <= int64(0) {
		t.Fatalf("expected positive estimate, got %d", got)
	}
	if got < int64(10) {
		t.Fatalf("estimate too small for payload: %d", got)
	}
}

func TestBuildPromptCachePolicyKeyLength(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	policy := buildPromptCachePolicy(settings, &domain.Provider{ID: "prov1", Kind: domain.ProviderChat}, "gpt-5", "conv_abc", promptCacheConversationPrefix)
	if policy == nil {
		t.Fatal("expected non-nil policy when PromptCaching is true")
	}
	if len(policy.Key) != 32 {
		t.Errorf("cache key length = %d, want 32 (key=%q)", len(policy.Key), policy.Key)
	}
	if !strings.HasPrefix(policy.Key, "nusashell_cv_") {
		t.Errorf("conversation cache key should start with nusashell_cv_, got %q", policy.Key)
	}
}

func TestBuildPromptCachePolicyUsesDistinctAgentPrefixes(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	provider := &domain.Provider{ID: "prov1", Kind: domain.ProviderChat}
	conversation := buildPromptCachePolicy(settings, provider, "gpt-5", "conv_abc", promptCacheConversationPrefix)
	background := buildPromptCachePolicy(settings, provider, "gpt-5", "conv_abc", promptCacheBackgroundPrefix)
	if conversation == nil || background == nil {
		t.Fatal("expected cache policies for both agent namespaces")
	}
	if len(conversation.Key) != len(background.Key) || len(conversation.Key) != 32 {
		t.Fatalf("cache key lengths = %d and %d, want both 32", len(conversation.Key), len(background.Key))
	}
	if !strings.HasPrefix(conversation.Key, "nusashell_cv_") {
		t.Errorf("conversation key = %q, want nusashell_cv_ prefix", conversation.Key)
	}
	if !strings.HasPrefix(background.Key, "nusashell_bg_") {
		t.Errorf("background key = %q, want nusashell_bg_ prefix", background.Key)
	}
	if conversation.Key == background.Key {
		t.Fatalf("conversation and background keys must be isolated: %q", conversation.Key)
	}
}

func TestPromptCachePrefixForRunSeparatesHeadlessAgents(t *testing.T) {
	if got := promptCachePrefixForRun(&TurnRun{}); got != promptCacheConversationPrefix {
		t.Fatalf("interactive run prefix = %q, want %q", got, promptCacheConversationPrefix)
	}
	if got := promptCachePrefixForRun(&TurnRun{Headless: true}); got != promptCacheBackgroundPrefix {
		t.Fatalf("headless run prefix = %q, want %q", got, promptCacheBackgroundPrefix)
	}
}

func TestBuildPromptCachePolicyNilWhenDisabled(t *testing.T) {
	settings := domain.Settings{PromptCaching: false}
	if p := buildPromptCachePolicy(settings, &domain.Provider{ID: "prov1"}, "gpt-5", "conv_abc", promptCacheConversationPrefix); p != nil {
		t.Errorf("expected nil policy when PromptCaching is false, got %+v", p)
	}
}

func TestBuildPromptCachePolicyStableForSameInputs(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	p := &domain.Provider{ID: "prov1", Kind: domain.ProviderChat}
	a := buildPromptCachePolicy(settings, p, "gpt-5", "conv_abc", promptCacheConversationPrefix)
	b := buildPromptCachePolicy(settings, p, "gpt-5", "conv_abc", promptCacheConversationPrefix)
	if a.Key != b.Key {
		t.Errorf("cache key should be stable for same inputs: %q vs %q", a.Key, b.Key)
	}
	c := buildPromptCachePolicy(settings, p, "gpt-5", "conv_xyz", promptCacheConversationPrefix)
	if a.Key == c.Key {
		t.Errorf("cache key should differ for different conversation: %q vs %q", a.Key, c.Key)
	}
}

func TestBuildPromptCachePolicyTTLFromProvider(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	anthropic := &domain.Provider{ID: "anthropic", Driver: domain.ProviderDriverAnthropic, Kind: domain.ProviderMessages, CacheTTL: "1h"}
	policy := buildPromptCachePolicy(settings, anthropic, "claude-sonnet-4-6", "conv_abc", promptCacheConversationPrefix)
	if policy == nil || policy.TTL != "1h" {
		t.Fatalf("anthropic TTL = %+v, want 1h", policy)
	}

	openai := &domain.Provider{ID: "openai", Driver: domain.ProviderDriverOpenAI, Kind: domain.ProviderResponses}
	policy = buildPromptCachePolicy(settings, openai, "gpt-5", "conv_abc", promptCacheConversationPrefix)
	if policy == nil || policy.TTL != "30m" {
		t.Fatalf("openai default TTL = %+v, want 30m", policy)
	}

	openrouter := &domain.Provider{ID: "openrouter", Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat, CacheTTL: "1h", BaseURL: "https://openrouter.ai/api/v1"}
	policy = buildPromptCachePolicy(settings, openrouter, "anthropic/claude-sonnet-4", "conv_abc", promptCacheConversationPrefix)
	if policy == nil || policy.TTL != "1h" {
		t.Fatalf("openrouter TTL = %+v, want 1h", policy)
	}

	off := &domain.Provider{ID: "anthropic", Driver: domain.ProviderDriverAnthropic, Kind: domain.ProviderMessages, CacheTTL: domain.CacheTTLOff}
	if policy = buildPromptCachePolicy(settings, off, "claude-sonnet-4-6", "conv_abc", promptCacheConversationPrefix); policy != nil {
		t.Fatalf("off TTL must skip prompt cache, got %+v", policy)
	}

	// OpenCode Console Go validates cache TTL as 5m|1h. Its Chat wire stays
	// vanilla for reasoning_content replay, so the cache enum intentionally
	// remains OpenRouter-compatible instead of matching the wire serializer.
	opencode := &domain.Provider{
		ID: "prov_oc", Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
		BaseURL: "https://opencode.ai/zen/go/v1", CacheTTL: "1h",
	}
	policy = buildPromptCachePolicy(settings, opencode, "deepseek-v4-flash", "conv_abc", promptCacheConversationPrefix)
	if policy == nil || policy.TTL != "1h" {
		t.Fatalf("opencode TTL = %+v, want 1h", policy)
	}
}

func TestTruncateToolErrorShortMessage(t *testing.T) {
	msg := "error: something went wrong"
	if got := truncateToolError(msg); got != msg {
		t.Errorf("short error should be unchanged, got %q", got)
	}
}

func TestTruncateToolErrorLongMessage(t *testing.T) {
	// Simulate a provider error that embeds base64 audio data (1.5MB+)
	msg := "error: Failed to load image from data:audio/mpeg;base64,//Nkx" + strings.Repeat("A", 2000000)
	got := truncateToolError(msg)
	if len(got) > maxToolErrorLen+100 {
		t.Errorf("truncated error should be ~%d chars, got %d", maxToolErrorLen+100, len(got))
	}
	if !strings.HasPrefix(got, "error: Failed to load image") {
		t.Errorf("truncated error should preserve diagnostic prefix, got %q", got[:100])
	}
	if !strings.Contains(got, "[truncated:") {
		t.Errorf("truncated error should note truncation, got %q", got[len(got)-100:])
	}
}

func TestTruncateToolErrorExactLimit(t *testing.T) {
	msg := strings.Repeat("x", maxToolErrorLen)
	if got := truncateToolError(msg); got != msg {
		t.Errorf("error at exact limit should be unchanged, got len %d", len(got))
	}
}

// --- from agent_round_continuation_retry_test.go ---

// prematureEOFStream emits the given content deltas, then returns a network
// error wrapping io.ErrUnexpectedEOF — the exact error shape produced by
// openai/stream.go and compat/stream.go when the stream ends before [DONE]
// without finish_reason. This simulates a 2xx response that starts
// streaming content but closes the connection without completing the turn.
type prematureEOFStream struct {
	events []core.Event
	idx    int
}

func (s *prematureEOFStream) Next() (core.Event, error) {
	if s.idx < len(s.events) {
		ev := s.events[s.idx]
		s.idx++
		return ev, nil
	}
	return nil, core.NewNetworkError("openai", "openai: stream ended before [DONE] without finish_reason", io.ErrUnexpectedEOF)
}

func (s *prematureEOFStream) Close() error { return nil }

// prematureEOFThenOKProvider streams partial content then fails with a
// premature EOF on the first call, then succeeds on the second call. It
// records every request so the test can assert the retry happened and that
// the continuation nudge was injected.
type prematureEOFThenOKProvider struct {
	mu       sync.Mutex
	calls    int
	requests []*core.Request
}

func (p *prematureEOFThenOKProvider) Name() string { return "premature-eof-then-ok" }

func (p *prematureEOFThenOKProvider) Stream(_ context.Context, req *core.Request) (core.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, req)
	if p.calls == 1 {
		// Stream partial content, then cut without finish_reason or [DONE].
		return &prematureEOFStream{events: []core.Event{
			core.ContentDelta{Text: "Hello, I will help you with"},
		}}, nil
	}
	// Second call: the model should continue from where it stopped.
	// Verify the continuation tool was injected by checking the last messages.
	resp := &core.Response{
		Blocks:       []core.Block{core.TextBlock{Text: " that task."}},
		FinishReason: core.FinishReasonStop,
	}
	return &stubStream{events: coreResponseEvents(resp)}, nil
}

func (p *prematureEOFThenOKProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}

// TestStreamTurnRoundContinuesAfterPrematureEOF proves that when a provider
// stream delivers partial content then ends without [DONE] or finish_reason
// (a 2xx response that cuts mid-stream), the retry loop does NOT fail the
// turn. Instead it auto-retries with a continuation nudge: the partial
// content is injected as an ephemeral assistant message followed by the
// announcement tool, and the model continues from where it stopped.
//
// Without this behavior, a transient mid-stream EOF on a 2xx response
// requires the user to manually click retry — even though the partial
// content is valid and the model can continue seamlessly.
func TestStreamTurnRoundContinuesAfterPrematureEOF(t *testing.T) {
	provider := &prematureEOFThenOKProvider{}
	conv := &domain.Conversation{
		ID: "c-premature",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "help me"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c-premature": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		retrySleeper:  func(context.Context, time.Duration) error { return nil },
	}
	run := &TurnRun{ID: "r-premature", ConversationID: "c-premature", ProviderID: "openai", Ctx: context.Background()}

	res, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a1", "gpt-4.1", "", nil, domain.Settings{}, false, 100, nil, ModelCapabilities{}, 1)
	if err != nil {
		t.Fatalf("streamTurnRound returned error after premature EOF: %v (expected continuation retry to succeed)", err)
	}
	// The final content should be the accumulated text: the partial content
	// from the first attempt prepended to the continuation from the retry.
	if res.Content != "Hello, I will help you with that task." {
		t.Fatalf("content = %q, want %q", res.Content, "Hello, I will help you with that task.")
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (initial EOF + continuation retry)", provider.calls)
	}
	// The retry request must contain the continuation announcement tool:
	// the partial content as an assistant message, followed by the
	// announcement tool call and its result.
	retryReq := provider.requests[1]
	foundPartial := false
	foundAnnouncement := false
	for _, msg := range retryReq.Messages {
		if msg.Role == core.RoleAssistant && len(msg.Blocks) == 1 {
			if tb, ok := msg.Blocks[0].(core.TextBlock); ok && tb.Text == "Hello, I will help you with" {
				foundPartial = true
			}
		}
		for _, b := range msg.Blocks {
			if tb, ok := b.(core.ToolUseBlock); ok && tb.Name == domain.AnnouncementToolName {
				foundAnnouncement = true
			}
		}
	}
	if !foundPartial {
		t.Fatal("retry request does not contain the partial content as an assistant message")
	}
	if !foundAnnouncement {
		t.Fatal("retry request does not contain the continuation announcement tool")
	}
}

// prematureEOFWithToolsProvider streams a tool-call start then fails with a
// premature EOF. Tool-call failures must NOT use continuation — the tool
// call needs a clean restart, not a "continue from where you stopped".
func TestStreamTurnRoundDoesNotContinueWhenToolCallsInProgress(t *testing.T) {
	provider := &prematureEOFWithToolsProvider{}
	conv := &domain.Conversation{
		ID: "c-premature-tools",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "run a tool"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c-premature-tools": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		retrySleeper:  func(context.Context, time.Duration) error { return nil },
	}
	run := &TurnRun{ID: "r-premature-tools", ConversationID: "c-premature-tools", ProviderID: "openai", Ctx: context.Background()}

	_, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a1", "gpt-4.1", "", nil, domain.Settings{}, false, 100, nil, ModelCapabilities{}, 1)
	if err == nil {
		t.Fatal("expected error when tool calls in progress and stream cuts, got nil (should not continue with tool calls)")
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (should not retry with continuation when tool calls are in progress)", provider.calls)
	}
}

type prematureEOFWithToolsProvider struct {
	calls int
}

func (p *prematureEOFWithToolsProvider) Name() string { return "premature-eof-tools" }

func (p *prematureEOFWithToolsProvider) Stream(context.Context, *core.Request) (core.Stream, error) {
	p.calls++
	idx := 0
	return &prematureEOFStream{events: []core.Event{
		core.ContentDelta{Text: "Let me check"},
		core.ToolUseStart{ID: "call_1", Name: "file_read", Index: &idx},
	}}, nil
}

func (p *prematureEOFWithToolsProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}

// --- from agent_round_effort_test.go ---

// effortCapturingProvider records the Thinking field of the last
// core.Request it received so the test can assert the effort strip guard.
type effortCapturingProvider struct {
	lastThinking *core.Thinking
}

func (p *effortCapturingProvider) Name() string { return "effort-capture" }

func (p *effortCapturingProvider) Chat(ctx context.Context, req *core.Request) (*core.Response, error) {
	p.lastThinking = req.Thinking
	return &core.Response{FinishReason: core.FinishReasonStop, Blocks: []core.Block{core.TextBlock{Text: "ok"}}}, nil
}

func (p *effortCapturingProvider) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	p.lastThinking = req.Thinking
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &singleShotStream{resp: resp, provider: p.Name(), model: req.Model}, nil
}

type singleShotStream struct {
	resp     *core.Response
	provider string
	model    string
	done     bool
}

func (s *singleShotStream) Next() (core.Event, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return core.DoneEvent{FinishReason: s.resp.FinishReason, Provider: s.provider, Model: s.model}, nil
}

func (s *singleShotStream) Close() error { return nil }

type toolConstructionActivityProvider struct{}

func (p *toolConstructionActivityProvider) Name() string { return "tool-activity" }

func (p *toolConstructionActivityProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, nil
}

func (p *toolConstructionActivityProvider) Stream(context.Context, *core.Request) (core.Stream, error) {
	index := 0
	return &stubStream{events: []core.Event{
		core.ToolUseStart{ID: "call_1", Name: "file_read", Index: &index},
		core.ToolUseDelta{ID: "call_1", Index: &index, ArgumentsDelta: []byte(`{"path":`)},
		core.ToolUseDelta{ID: "call_1", Index: &index, ArgumentsDelta: []byte(`"/tmp/note"}`)},
		core.ToolUseDone{ID: "call_1", Index: &index},
		core.DoneEvent{FinishReason: core.FinishReasonToolCall, Provider: "tool-activity", Model: "test-model"},
	}}, nil
}

func TestStreamTurnRoundPublishesToolConstructionActivity(t *testing.T) {
	reg := NewRoundStreamRegistry()
	conv := &domain.Conversation{
		ID: "c-activity",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "read the note"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c-activity": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		RoundStreams:  reg,
	}
	run := &TurnRun{ID: "r-activity", ConversationID: conv.ID, Ctx: context.Background()}
	if _, err := app.streamTurnRoundOnce(run, stubProviderContext(&toolConstructionActivityProvider{}), conv, "a1", "test-model", "", nil, domain.Settings{}, false, nil, 100, nil, ModelCapabilities{}, 1); err != nil {
		t.Fatalf("streamTurnRoundOnce: %v", err)
	}

	sub, err := reg.Subscribe(context.Background(), run.ID, "a1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case frame := <-sub.Frames():
		if frame.Kind != contracts.RoundDeltaActivity || frame.Activity != contracts.RoundActivityToolCall {
			t.Fatalf("activity frame = %+v", frame)
		}
		if frame.ToolCallID != "call_1" || frame.Name != "file_read" {
			t.Fatalf("activity metadata = %+v", frame)
		}
		if len(frame.Args) != 0 {
			t.Fatalf("activity frame leaked partial args: %s", frame.Args)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool construction activity")
	}
}

// TestStreamTurnRoundStripsEffortForNonReasoningModel proves Bug 2's guard:
// when caps.Reasoning=false and effort is a real level (e.g. "high"), the
// effort is stripped to "auto" before reaching the provider so non-reasoning
// models do not receive a thinking field.
func TestStreamTurnRoundStripsEffortForNonReasoningModel(t *testing.T) {
	provider := &effortCapturingProvider{}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {
			ID: "c1",
			Messages: []domain.Message{
				{ID: "u1", Role: domain.RoleUser, Content: "hi"},
				{ID: "a1", Role: domain.RoleAssistant},
			},
		}}},
		Toolbox: &recordingToolbox{},
		Bus:     NewBus(),
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	caps := ModelCapabilities{Vision: true, Reasoning: false}
	_, err := app.streamTurnRoundOnce(run, stubProviderContext(provider), &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "hi"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}, "a1", "openai/gpt-4.1", "high", nil, domain.Settings{}, false, nil, 100, nil, caps, 1)
	if err != nil {
		t.Fatalf("streamTurnRoundOnce: %v", err)
	}
	if provider.lastThinking != nil {
		t.Fatalf("non-reasoning model received Thinking=%+v, want nil (effort stripped)", provider.lastThinking)
	}
}

// TestStreamTurnRoundKeepsEffortForReasoningModel ensures the guard does
// not strip effort when the model supports reasoning.
func TestStreamTurnRoundKeepsEffortForReasoningModel(t *testing.T) {
	provider := &effortCapturingProvider{}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {
			ID: "c1",
			Messages: []domain.Message{
				{ID: "u1", Role: domain.RoleUser, Content: "hi"},
				{ID: "a1", Role: domain.RoleAssistant},
			},
		}}},
		Toolbox: &recordingToolbox{},
		Bus:     NewBus(),
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	caps := ModelCapabilities{Vision: true, Reasoning: true}
	_, err := app.streamTurnRoundOnce(run, stubProviderContext(provider), &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "hi"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}, "a1", "deepseek/deepseek-r1", "high", nil, domain.Settings{}, false, nil, 100, nil, caps, 1)
	if err != nil {
		t.Fatalf("streamTurnRoundOnce: %v", err)
	}
	if provider.lastThinking == nil || provider.lastThinking.Mode != core.ThinkingEnabled || provider.lastThinking.Effort != "high" {
		t.Fatalf("reasoning model received Thinking=%+v, want enabled/high", provider.lastThinking)
	}
}

// --- from agent_round_400learn_test.go ---

// textOnlyThenOKProvider returns a 400 "text-only" domain.ProviderError on the
// first Stream call, then succeeds on the second. It records every request
// so the test can assert the retry actually happened and that the image
// was stripped after the 400-learning disabled Vision.
type textOnlyThenOKProvider struct {
	mu       sync.Mutex
	calls    int
	requests []*core.Request
}

func (p *textOnlyThenOKProvider) Name() string { return "text-only-then-ok" }

func (p *textOnlyThenOKProvider) Stream(_ context.Context, req *core.Request) (core.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, req)
	if p.calls == 1 {
		return nil, &domain.ProviderError{
			StatusCode: 400,
			Err:        errors.New(`Qwen3.8 open checkpoint is text-only; messages[0].content[1] must be a text part`),
		}
	}
	resp := &core.Response{
		Blocks:       []core.Block{core.TextBlock{Text: "ok after learning"}},
		FinishReason: core.FinishReasonStop,
	}
	return &stubStream{events: coreResponseEvents(resp)}, nil
}

func (p *textOnlyThenOKProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}

// TestStreamTurnRoundRetriesAfterLearnable400 proves that a learnable 400
// (text-only model rejecting an image) triggers an in-turn retry after the
// 400-learning classifier disables Vision and the request is rebuilt with
// the image stripped. Without the retry, the turn fails immediately on the
// first 400 even though the learning already recorded the fix.
func TestStreamTurnRoundRetriesAfterLearnable400(t *testing.T) {
	provider := &textOnlyThenOKProvider{}
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "describe this", Attachments: []domain.Attachment{
				{Type: "image", Name: "pic.png", FilePath: "/tmp/pic.png", MediaType: "image/png", DataURL: "data:image/png;base64,ZmFrZQ=="},
			}},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		learnedParams: learnedparams.New(&fakeLearnedParamStore{}),
		retrySleeper:  func(context.Context, time.Duration) error { return nil },
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", ProviderID: "9router", Ctx: context.Background()}
	caps := ModelCapabilities{Vision: true}

	res, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a1", "qwen/qwen3.8-max-free", "", nil, domain.Settings{}, false, 100, nil, caps, 1)
	if err != nil {
		t.Fatalf("streamTurnRound returned error after learnable 400: %v (expected retry to succeed)", err)
	}
	if res.Content != "ok after learning" {
		t.Fatalf("content = %q, want %q", res.Content, "ok after learning")
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (initial 400 + retry after learning)", provider.calls)
	}
	// The retry request must not carry the image block: Vision was
	// disabled by the 400-learning, so chatMessages should have stripped
	// the image attachment and replaced it with a read_media placeholder.
	if requestHasImageBlock(provider.requests[1]) {
		t.Fatal("retry request still contains an image block; 400-learning should have stripped it after disabling Vision")
	}
}

type assistantPrefillThenOKProvider struct {
	calls    int
	requests []*core.Request
}

func (p *assistantPrefillThenOKProvider) Name() string { return "assistant-prefill-then-ok" }

func (p *assistantPrefillThenOKProvider) Stream(_ context.Context, req *core.Request) (core.Stream, error) {
	p.calls++
	p.requests = append(p.requests, req)
	if p.calls == 1 {
		return nil, &domain.ProviderError{
			Kind:       domain.KindHTTPStatus,
			StatusCode: 400,
			Err:        errors.New(`provider returned HTTP 400: {"error":{"message":"This model does not support assistant message prefill. The conversation must end with a user message."}}`),
		}
	}
	resp := &core.Response{
		Blocks:       []core.Block{core.TextBlock{Text: "ok after user repair"}},
		FinishReason: core.FinishReasonStop,
	}
	return &stubStream{events: coreResponseEvents(resp)}, nil
}

func (p *assistantPrefillThenOKProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}

func TestStreamTurnRoundRepairsAssistantPrefill400(t *testing.T) {
	provider := &assistantPrefillThenOKProvider{}
	conv := &domain.Conversation{
		ID: "c-prefill",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "research this"},
			{ID: "a1", Role: domain.RoleAssistant, Content: "previous answer", Status: domain.StatusDone},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c-prefill": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		learnedParams: learnedparams.New(&fakeLearnedParamStore{}),
		retrySleeper:  func(context.Context, time.Duration) error { return nil },
	}
	run := &TurnRun{ID: "r-prefill", ConversationID: "c-prefill", ProviderID: "9router", Ctx: context.Background()}

	res, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a2", "ag/claude-sonnet-4-6", "", nil, domain.Settings{}, false, 100, nil, ModelCapabilities{}, 1)
	if err != nil {
		t.Fatalf("streamTurnRound returned error after prefill repair: %v", err)
	}
	if res.Content != "ok after user repair" {
		t.Fatalf("content = %q, want ok after user repair", res.Content)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if got := provider.requests[0].Messages[len(provider.requests[0].Messages)-1].Role; got != core.RoleAssistant {
		t.Fatalf("first request last role = %q, want assistant", got)
	}
	last := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if last.Role != core.RoleUser {
		t.Fatalf("retry request last role = %q, want user", last.Role)
	}
	if len(last.Blocks) != 1 || last.Blocks[0].(core.TextBlock).Text != userNudgeText {
		t.Fatalf("retry user message = %#v, want synthetic %q", last.Blocks, userNudgeText)
	}
}

type connectionResetThenOKProvider struct {
	calls int
}

func (p *connectionResetThenOKProvider) Name() string { return "connection-reset-then-ok" }

func (p *connectionResetThenOKProvider) Stream(context.Context, *core.Request) (core.Stream, error) {
	p.calls++
	if p.calls == 1 {
		// Return the standard library transport error directly. This is the
		// boundary case where an adapter has not wrapped the error in a core
		// LiteLLMError yet; the domain policy must still recognize it.
		return &errorStream{err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}}, nil
	}
	return &stubStream{events: []core.Event{
		core.ContentDelta{Text: "recovered after reset"},
		core.DoneEvent{FinishReason: core.FinishReasonStop, Provider: "connection-reset-then-ok", Model: "test-model"},
	}}, nil
}

func (p *connectionResetThenOKProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}

type errorStream struct {
	err      error
	returned bool
}

func (s *errorStream) Next() (core.Event, error) {
	if !s.returned {
		s.returned = true
		return nil, s.err
	}
	return nil, io.EOF
}

func (s *errorStream) Close() error { return nil }

func TestStreamTurnRoundRetriesConnectionResetTransportError(t *testing.T) {
	provider := &connectionResetThenOKProvider{}
	conv := &domain.Conversation{
		ID:       "c-reset",
		Messages: []domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "continue the task"}},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c-reset": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		retrySleeper:  func(context.Context, time.Duration) error { return nil },
	}
	run := &TurnRun{ID: "r-reset", ConversationID: "c-reset", ProviderID: "openai", Ctx: context.Background()}

	res, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a1", "gpt-4.1", "", nil, domain.Settings{}, false, 100, nil, ModelCapabilities{}, 1)
	if err != nil {
		t.Fatalf("streamTurnRound returned error after connection reset: %v", err)
	}
	if res.Content != "recovered after reset" {
		t.Fatalf("content = %q, want recovered response", res.Content)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (initial reset + internal retry)", provider.calls)
	}
}

// requestHasImageBlock reports whether any message in the request carries
// an image block.
func requestHasImageBlock(req *core.Request) bool {
	if req == nil {
		return false
	}
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if _, ok := b.(core.ImageBlock); ok {
				return true
			}
		}
	}
	return false
}

// --- from agent_round_content_test.go ---

func TestApplyStreamRoundPersistsReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-PERSIST"}`)
	msg := &domain.Message{ID: "a1", Role: domain.RoleAssistant}
	applyStreamRound(msg, "gpt-5.6-sol", streamedTurnRound{
		Content:   "answer",
		Reasoning: "think",
		Response:  ChatResponse{ReasoningExtra: extra},
	})
	if msg.Reasoning != "think" {
		t.Fatalf("Reasoning = %q, want think (UI text unchanged)", msg.Reasoning)
	}
	if string(msg.ReasoningExtra) != string(extra) {
		t.Fatalf("ReasoningExtra = %s, want %s", msg.ReasoningExtra, extra)
	}
}

func TestChatMessagesForProviderReplaysReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-REPLAY"}`)
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "hi", Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: "ok", Reasoning: "think", ReasoningExtra: extra, Status: domain.StatusDone},
	}}
	msgs := chatMessages(conv, "", ModelCapabilities{})
	var assistant *ChatMessage
	for i := range msgs {
		if msgs[i].Role == "assistant" {
			assistant = &msgs[i]
			break
		}
	}
	if assistant == nil {
		t.Fatal("expected assistant ChatMessage")
	}
	if string(assistant.ReasoningExtra) != string(extra) {
		t.Fatalf("ReasoningExtra = %s, want %s", assistant.ReasoningExtra, extra)
	}
}

func TestApplyStreamRoundDropsWhitespaceOnlyContent(t *testing.T) {
	for _, raw := range []string{"\n\n", "\n\n\n\n", "  \n"} {
		msg := &domain.Message{ID: "a1", Role: domain.RoleAssistant}
		applyStreamRound(msg, "qwen", streamedTurnRound{Content: raw})
		if msg.Content != "" {
			t.Fatalf("Content = %q from %q, want empty", msg.Content, raw)
		}
		if len(msg.Steps) != 0 {
			t.Fatalf("Steps = %+v, want none for whitespace-only content", msg.Steps)
		}
	}
}

func TestApplyStreamRoundTrimsLeadingTrailingNewlines(t *testing.T) {
	msg := &domain.Message{ID: "a1", Role: domain.RoleAssistant}
	applyStreamRound(msg, "qwen", streamedTurnRound{Content: "\n\nNow update main.go:\n\n"})
	if msg.Content != "Now update main.go:" {
		t.Fatalf("Content = %q, want trimmed text", msg.Content)
	}
}

// TestApplyStreamRoundKeepsTrailingSpace pins partial-stream fidelity: a
// stream cut mid-sentence persists its trailing space. The continuation
// round starts a new message, and the space is the word boundary between
// the two — stripping it runs words together ("here.And").
func TestApplyStreamRoundKeepsTrailingSpace(t *testing.T) {
	msg := &domain.Message{ID: "a1", Role: domain.RoleAssistant}
	applyStreamRound(msg, "model", streamedTurnRound{Content: "The answer starts here. "})
	if msg.Content != "The answer starts here. " {
		t.Fatalf("Content = %q, want trailing space preserved", msg.Content)
	}
	if len(msg.Steps) != 1 || msg.Steps[0].Content != "The answer starts here. " {
		t.Fatalf("Steps = %+v, want the same trailing-space text", msg.Steps)
	}
}
