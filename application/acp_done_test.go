package application

import (
	"testing"
	"time"

	"nusashell/domain"
)

func TestOnAcpRunDoneQueuesWhileParentTurnActive(t *testing.T) {
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
	app := &App{Conversations: store, Bus: NewBus(), runs: map[string]*TurnRun{}}
	parent := &TurnRun{ID: "turn1", ConversationID: "c1"}
	app.runs[parent.ID] = parent
	app.trackPendingSubagent("c1", "run_done")

	acpRun := &domain.AcpRun{
		ID:               "run_done",
		ConversationID:   "c1",
		ParentToolCallID: "call_parent",
		Status:           domain.AcpRunCompleted,
		Transcript:       []domain.AcpTranscriptChunk{{Kind: "text", Text: "done"}},
	}

	turnLock := app.conversationTurnLock("c1")
	turnLock.Lock()
	defer turnLock.Unlock()

	done := make(chan struct{})
	go func() {
		app.onAcpRunDone(acpRun)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("onAcpRunDone blocked on the parent turn lock")
	}

	if store.convs["c1"].Messages[1].ToolCalls[0].Status != domain.ToolRunning {
		t.Fatal("must not mutate the conversation until the steer-style turn boundary")
	}
	if len(store.convs["c1"].Messages) != 2 {
		t.Fatalf("messages = %d, want 2 before drain", len(store.convs["c1"].Messages))
	}
	if !app.hasPendingSubagents("c1") {
		t.Fatal("must stay pending until the parent turn drains the result")
	}

	applied, err := app.applyQueuedSubagentResults(parent)
	if err != nil {
		t.Fatalf("applyQueuedSubagentResults: %v", err)
	}
	if !applied {
		t.Fatal("queued subagent completion was not applied")
	}
	saved := store.convs["c1"]
	if saved.Messages[1].ToolCalls[0].Status != domain.ToolOK {
		t.Fatalf("original tool call status = %v, want ok", saved.Messages[1].ToolCalls[0].Status)
	}
	if len(saved.Messages) != 3 || saved.Messages[2].ToolCalls[0].Name != domain.SubagentResultToolName {
		t.Fatalf("synthetic subagent_result missing: %+v", saved.Messages)
	}
	if app.hasPendingSubagents("c1") {
		t.Fatal("pending subagent must be untracked after drain")
	}
}

func TestOnAcpRunDoneInjectsImmediatelyWhenParentIdle(t *testing.T) {
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
	app := &App{Conversations: store, Bus: NewBus(), runs: map[string]*TurnRun{}}
	app.trackPendingSubagent("c1", "run_done")
	acpRun := &domain.AcpRun{
		ID:               "run_done",
		ConversationID:   "c1",
		ParentToolCallID: "call_parent",
		Status:           domain.AcpRunCompleted,
		Transcript:       []domain.AcpTranscriptChunk{{Kind: "text", Text: "done"}},
	}

	app.onAcpRunDone(acpRun)

	saved := store.convs["c1"]
	if saved.Messages[1].ToolCalls[0].Status != domain.ToolOK {
		t.Fatalf("idle completion must inject immediately, status=%v", saved.Messages[1].ToolCalls[0].Status)
	}
	if len(saved.Messages) != 3 || saved.Messages[2].ToolCalls[0].Name != domain.SubagentResultToolName {
		t.Fatalf("idle completion must append subagent_result: %+v", saved.Messages)
	}
	if app.hasPendingSubagents("c1") {
		t.Fatal("idle completion must untrack the run")
	}
}
