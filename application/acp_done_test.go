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
	app.trackPendingRun("c1", "run_done", "subagent")

	acpRun := &domain.AcpRun{
		TaskState:        domain.TaskState[domain.AcpRunStatus]{ID: "run_done", Status: domain.AcpRunCompleted},
		ConversationID:   "c1",
		ParentToolCallID: "call_parent",
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
	if !app.hasPendingRuns("c1") {
		t.Fatal("must stay pending until the parent turn drains the result")
	}

	applied, err := app.applyQueuedRunResults(parent)
	if err != nil {
		t.Fatalf("applyQueuedRunResults: %v", err)
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
	if app.hasPendingRuns("c1") {
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
	app.trackPendingRun("c1", "run_done", "subagent")
	acpRun := &domain.AcpRun{
		TaskState:        domain.TaskState[domain.AcpRunStatus]{ID: "run_done", Status: domain.AcpRunCompleted},
		ConversationID:   "c1",
		ParentToolCallID: "call_parent",
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
	if app.hasPendingRuns("c1") {
		t.Fatal("idle completion must untrack the run")
	}
}

func TestRoundBoundaryPlacesSteerAfterBackgroundResults(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "research", Status: domain.StatusDone},
			{ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{ID: "call_parent", Name: "subagent", Status: domain.ToolRunning}}},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}, runs: map[string]*TurnRun{}}
	parent := &TurnRun{ID: "turn1", ConversationID: "c1"}
	app.runs[parent.ID] = parent
	app.trackPendingRun("c1", "run_done", "subagent")
	parent.queueRunDone(pendingRunDone{
		RunID: "run_done",
		Complete: func(cid string) error {
			return app.completeSubagentRunLocked(cid, "call_parent", domain.ToolOK, &domain.AcpRun{
				TaskState:        domain.TaskState[domain.AcpRunStatus]{ID: "run_done", Status: domain.AcpRunCompleted},
				ConversationID:   cid,
				ParentToolCallID: "call_parent",
				Transcript:       []domain.AcpTranscriptChunk{{Kind: "text", Text: "finished"}},
			}, "")
		},
	})
	if !parent.queueSteer(newSteerEntry("prioritize this", nil)) {
		t.Fatal("queue steer")
	}

	rules := app.newConversationRules(parent, ProviderContext{}, conv, domain.DefaultSettings(), nil, "", "", "m2", ModelCapabilities{}, nil, 0, nil, false).rules()
	continued, err := rules.AfterRound(nil, ChatResponse{}, nil)
	if err != nil {
		t.Fatalf("round boundary: %v", err)
	}
	if !continued {
		t.Fatal("injected boundary state must continue the agent")
	}

	saved, err := app.Conversations.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 5 {
		t.Fatalf("messages = %d, want original messages plus result, steer, and next assistant: %+v", len(saved.Messages), saved.Messages)
	}
	if len(saved.Messages[2].ToolCalls) != 1 || saved.Messages[2].ToolCalls[0].Name != domain.SubagentResultToolName {
		t.Fatalf("background result must precede steer, got message[2] = %+v", saved.Messages[2])
	}
	if saved.Messages[3].Role != domain.RoleUser || !saved.Messages[3].Steer || saved.Messages[3].Content != "prioritize this" {
		t.Fatalf("steer must be the last injected user instruction, got message[3] = %+v", saved.Messages[3])
	}
}
