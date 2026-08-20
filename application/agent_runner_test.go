package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func TestUpdateToolResultUpdatesChronologicalToolCallStep(t *testing.T) {
	app := &App{}
	conversation := &domain.Conversation{Messages: []domain.Message{{
		ID:        "message-1",
		ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "docs_search", Status: domain.ToolRunning}},
		Steps: []domain.MessageStep{{
			Type:      domain.StepToolCalls,
			ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "docs_search", Status: domain.ToolRunning}},
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
		if _, err := app.streamTurnRoundOnce(run, adapter, conversation, messageID, "model", "", nil, domain.Settings{}, false, 0, true, nil, ModelCapabilities{}, ""); err != nil {
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
	if got := resolveContextWindow(provider, "long-model", settings); got != 1_000_000 {
		t.Fatalf("long model context = %d, want model window 1000000", got)
	}
	if got := resolveContextWindow(provider, "small-model", settings); got != 128_000 {
		t.Fatalf("small model context = %d, want model window 128000", got)
	}
	if got := resolveContextWindow(provider, "unknown", settings); got != 200_000 {
		t.Fatalf("unknown model context = %d, want fallback 200000", got)
	}
	if got := effectiveContextWindow(1_000_000, 0); got != 1_000_000 {
		t.Fatalf("uncapped model context = %d, want 1000000", got)
	}
	if got := effectiveContextWindow(1_000_000, 200_000); got != 1_000_000 {
		t.Fatalf("catalog model should ignore global cap, got %d", got)
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
			ToolCalls: []domain.ToolCall{{ID: "t1", Name: "docs_search"}, {ID: "t2", Name: "memory_list"}},
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

	app.runTurn(run, provider, "active-token", "gpt-5.3-codex", "", "m1", false, ModelCapabilities{Vision: true}, "")

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
