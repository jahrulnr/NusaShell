package application

import (
	"context"
	"errors"
	"strings"
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

func TestChatMessagesKeepsReasoningOnlyAssistantTurns(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "hi"},
		{ID: "a1", Role: domain.RoleAssistant, Reasoning: "thinking", Status: domain.StatusDone},
		{ID: "pending", Role: domain.RoleAssistant},
	}}
	got := chatMessages(c, "pending", true)
	if len(got) != 2 {
		t.Fatalf("got %d messages, want user + reasoning-only assistant", len(got))
	}
	if got[1].Reasoning != "thinking" {
		t.Fatalf("assistant reasoning = %q", got[1].Reasoning)
	}
}

func TestCompactionTriggerUsesThresholdCappedByWindow(t *testing.T) {
	// Explicit threshold: capped at 80% of window for small windows.
	settings := domain.Settings{CompactionThreshold: 40000}
	if got := compactionTriggerTokens(1000, settings); got != 800 {
		t.Fatalf("small window: got %d, want 800 (80%% of 1000)", got)
	}
	if got := compactionTriggerTokens(200000, settings); got != 40000 {
		t.Fatalf("large window: got %d, want threshold 40000", got)
	}
	settings.CompactionThreshold = 5000
	if got := compactionTriggerTokens(200000, settings); got != 5000 {
		t.Fatalf("custom threshold: got %d, want 5000", got)
	}
}

func TestCompactionTriggerAutoUsesWindowPercentage(t *testing.T) {
	// Threshold=0 means "auto": compaction triggers at 80% of the model's
	// context window, not at a flat token count. This is the default for
	// new installations and after migration from the old 40k default.
	settings := domain.Settings{CompactionThreshold: 0}
	if got := compactionTriggerTokens(1048576, settings); got != 838860 {
		t.Fatalf("1M window auto: got %d, want 838860 (80%% of 1M)", got)
	}
	if got := compactionTriggerTokens(200000, settings); got != 160000 {
		t.Fatalf("200k window auto: got %d, want 160000 (80%% of 200k)", got)
	}
	if got := compactionTriggerTokens(1000, settings); got != 800 {
		t.Fatalf("1k window auto: got %d, want 800", got)
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
	app.interruptTurn(run, "m1", streamedTurnRound{Content: "hello", Reasoning: "think"}, ChatUsage{OutputTokens: 4}, "model-x")

	got := conv.Messages[0]
	if got.Content != "hello" || got.Reasoning != "think" || got.Status != domain.StatusInterrupted {
		t.Fatalf("interrupted message = %+v", got)
	}
	if got.Model != "model-x" {
		t.Fatalf("model = %q", got.Model)
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
	if err := app.executeTurnTools(run, "m1", conv.Messages[0].ToolCalls, true, domain.Settings{}); err == nil {
		t.Fatal("want context error")
	}
	if len(box.names) != 0 {
		t.Fatalf("executed tools %v, want none after cancel", box.names)
	}
	if conv.Messages[0].ToolCalls[0].Status != domain.ToolInterrupted || conv.Messages[0].ToolCalls[1].Status != domain.ToolInterrupted {
		t.Fatalf("tool statuses = %+v", conv.Messages[0].ToolCalls)
	}
}

type recordingToolbox struct{ names []string }

func (r *recordingToolbox) ListTools() []ToolInfo { return nil }
func (r *recordingToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	r.names = append(r.names, name)
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

	app.runTurn(run, provider, "active-token", "gpt-5.3-codex", "", "m1", false, true, "")

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
