package application

import (
	"context"
	"testing"

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

	got := app.updateToolResult(conversation, "message-1", "call-1", domain.ToolOK, "first\nsecond")

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
	got := chatMessages(c, "pending")
	if len(got) != 2 {
		t.Fatalf("got %d messages, want user + reasoning-only assistant", len(got))
	}
	if got[1].Reasoning != "thinking" {
		t.Fatalf("assistant reasoning = %q", got[1].Reasoning)
	}
}

func TestCompactionTriggerUsesThresholdCappedByWindow(t *testing.T) {
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
	if err := app.executeTurnTools(run, "m1", conv.Messages[0].ToolCalls); err == nil {
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
