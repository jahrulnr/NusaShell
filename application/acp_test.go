package application

import (
	"context"
	"encoding/json"
	"errors"
	"nusashell/application/service/tooloutput"
	"nusashell/contracts"
	"nusashell/domain"
	"strings"
	"testing"
	"time"
)

// --- from acp_done_test.go ---

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
		ID: "c1", Status: "idle",
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

func TestIdleSubagentCompletionStartsTurnAndResumesAutoContinue(t *testing.T) {
	const conversationID = "c1"

	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	settings.MaxAutoContinues = 1
	todos := &fakeTodoPort{
		items: map[string][]domain.TodoItem{
			conversationID: {{
				ID:      "todo-1",
				Content: "finish the delegated task",
				Status:  domain.TodoInProgress,
			}},
		},
	}
	provider := &domain.Provider{
		ID:      "p",
		Name:    "test provider",
		Kind:    domain.ProviderChat,
		Enabled: true,
		Models:  []domain.Model{{ID: "model", Context: 128000}},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{
		conversationID: {
			ID:     conversationID,
			Model:  "model",
			Status: "running",
			Messages: []domain.Message{
				{ID: "u1", Role: domain.RoleUser, Content: "delegate this", Status: domain.StatusDone},
				{
					ID:     "a1",
					Role:   domain.RoleAssistant,
					Status: domain.StatusDone,
					ToolCalls: []domain.ToolCall{{
						ID:     "call-subagent",
						Name:   domain.SubagentToolName,
						Status: domain.ToolRunning,
						Output: "starting",
						Args:   `{"prompt":"finish the delegated task"}`,
					}},
				},
				{ID: "a2", Role: domain.RoleAssistant},
			},
		},
	}}
	app := &App{
		Conversations: store,
		Providers:     &fakeProviderStore{items: map[string]*domain.Provider{"p": provider}},
		Credentials:   &memCreds{m: map[string]string{"p": "test-key"}},
		Settings:      &fakeSettingsStore{settings: settings},
		Factory: func(context.Context, *domain.Provider, string) (AIProvider, error) {
			return adapter, nil
		},
		Toolbox:     &recordingToolbox{},
		Todos:       todos,
		Logs:        &fakeLogStore{},
		Bus:         NewBus(),
		runs:        map[string]*TurnRun{},
		pendingRuns: map[string]map[string]string{},
	}
	app.trackPendingRun(conversationID, "run-subagent", domain.SubagentToolName)

	_, events, unsubscribe := app.Bus.Subscribe()
	defer unsubscribe()

	parent := &TurnRun{
		ID:             "parent-turn",
		ConversationID: conversationID,
		Ctx:            context.Background(),
		Cancel:         func() {},
	}
	app.runTurn(parent, provider, "test-key", "model", "", "a2", false, ModelCapabilities{})

	parentDone := waitForTurnDoneEvent(t, events, conversationID, parent.ID, string(domain.AutoContinueAwaitingBackground))
	if parentDone.AutoContinue.ShouldContinue {
		t.Fatal("parent turn must pause auto-continue while the subagent is pending")
	}
	if !app.hasPendingRuns(conversationID) {
		t.Fatal("subagent must remain pending after the parent turn ends")
	}
	assertNoAutoContinueAnnouncement(t, store.convs[conversationID])

	app.onAcpRunDone(&domain.AcpRun{
		TaskState:        domain.TaskState[domain.AcpRunStatus]{ID: "run-subagent", Status: domain.AcpRunCompleted},
		ConversationID:   conversationID,
		ParentToolCallID: "call-subagent",
		Transcript:       []domain.AcpTranscriptChunk{{Kind: "text", Text: "delegated work is complete"}},
	})

	waitForTurnDoneEvent(t, events, conversationID, "", string(domain.AutoContinueMaxReached))
	deadline := time.Now().Add(2 * time.Second)
	for app.activeRunForConversation(conversationID) != nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if app.activeRunForConversation(conversationID) != nil {
		t.Fatal("background completion turn is still active")
	}

	if app.hasPendingRuns(conversationID) {
		t.Fatal("completed subagent must be removed from pending runs")
	}
	assertCompletedSubagentAndContinuation(t, store.convs[conversationID])
}

func assertNoAutoContinueAnnouncement(t *testing.T, conversation *domain.Conversation) {
	t.Helper()
	for _, message := range conversation.Messages {
		for _, call := range message.ToolCalls {
			if call.Name == domain.AnnouncementToolName {
				t.Fatal("parent turn must not append auto-continue before subagent completion")
			}
		}
	}
}

func assertCompletedSubagentAndContinuation(t *testing.T, conversation *domain.Conversation) {
	t.Helper()
	if conversation.Status != "idle" {
		t.Fatalf("conversation status = %q, want idle", conversation.Status)
	}
	if conversation.Messages[1].ToolCalls[0].Status != domain.ToolOK {
		t.Fatalf("subagent tool status = %q, want ok", conversation.Messages[1].ToolCalls[0].Status)
	}

	var sawResult, sawAutoContinue bool
	textReplies := 0
	for _, message := range conversation.Messages {
		if message.Content == "hello" {
			textReplies++
		}
		for _, call := range message.ToolCalls {
			switch call.Name {
			case domain.SubagentResultToolName:
				sawResult = true
			case domain.AnnouncementToolName:
				sawAutoContinue = true
			}
		}
	}
	if !sawResult {
		t.Fatal("subagent completion must be injected into the parent transcript")
	}
	if !sawAutoContinue {
		t.Fatal("auto-continue must resume after the completion turn when todos remain")
	}
	if textReplies < 3 {
		t.Fatalf("text replies = %d, want parent plus completion and auto-continue turns", textReplies)
	}
}

func waitForTurnDoneEvent(t *testing.T, events <-chan contracts.Event, conversationID, wantRunID, wantReason string) contracts.TurnDoneEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			payload, ok := decodeTurnDoneEvent(event)
			if !ok || payload.ConversationID != conversationID || !matchesTurnDoneEvent(payload, wantRunID, wantReason) {
				continue
			}
			return payload
		case <-deadline:
			t.Fatalf("timed out waiting for turn.done run=%q reason=%q", wantRunID, wantReason)
			return contracts.TurnDoneEvent{}
		}
	}
}

func decodeTurnDoneEvent(event contracts.Event) (contracts.TurnDoneEvent, bool) {
	if event.Type != contracts.EventTurnDone {
		return contracts.TurnDoneEvent{}, false
	}
	var payload contracts.TurnDoneEvent
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return contracts.TurnDoneEvent{}, false
	}
	return payload, true
}

func matchesTurnDoneEvent(payload contracts.TurnDoneEvent, wantRunID, wantReason string) bool {
	if wantRunID != "" && payload.RunID != wantRunID {
		return false
	}
	return payload.AutoContinue != nil && payload.AutoContinue.Reason == wantReason
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
	parent.QueueRunDone(pendingRunDone{
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
	if !parent.QueueSteer(newSteerEntry("prioritize this", nil)) {
		t.Fatal("queue steer")
	}

	rules := app.newConversationRules(parent, ProviderContext{}, conv, domain.DefaultSettings(), nil, "", "", "m2", ModelCapabilities{}, nil, 0, nil, false).Rules()
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

// --- from acp_steer_stop_test.go ---

type steerStopAcpRuntime struct {
	AcpRuntime
	run *domain.AcpRun
}

func (f *steerStopAcpRuntime) Get(runID string) (*domain.AcpRun, bool) {
	return f.run, f.run != nil && f.run.ID == runID
}

func (f *steerStopAcpRuntime) Steer(runID, text string) error {
	if f.run == nil || f.run.ID != runID {
		return errors.New("run not found")
	}
	f.run.QueuedSteer = text
	return nil
}

func (f *steerStopAcpRuntime) Stop(runID string) error {
	if f.run == nil || f.run.ID != runID {
		return errors.New("run not found")
	}
	f.run.Finish(domain.AcpRunCancelled, "", "cancelled", time.Unix(1, 0))
	return nil
}

func bloatedSubagentRun(id string, status domain.AcpRunStatus) *domain.AcpRun {
	chunks := []domain.AcpTranscriptChunk{
		{Kind: "thought", Text: "long private reasoning " + strings.Repeat("r", 2500)},
		{Kind: "text", Text: "Intermediate progress. " + strings.Repeat("p", 2500)},
		{Kind: "tool", ToolID: "tool-1", ToolTitle: "edit_file", ToolStatus: "completed"},
	}
	for i := 0; i < 4; i++ {
		chunks = append(chunks, domain.AcpTranscriptChunk{
			Kind: "text", Text: "Progress chunk that must not leak. " + strings.Repeat("x", 1800),
		})
	}
	chunks = append(chunks, domain.AcpTranscriptChunk{Kind: "text", Text: "Last meaningful turn only."})
	return &domain.AcpRun{
		TaskState:      domain.TaskState[domain.AcpRunStatus]{ID: id, Status: status},
		AgentID:        "acp_1",
		AgentName:      "Codex",
		ConversationID: "conv_1",
		Workspace:      "/tmp/project",
		Prompt:         "private parent prompt that must not be repeated " + strings.Repeat("q", 2000),
		Transcript:     chunks,
	}
}

func TestSteerAcpRunReturnsCompactAckWithoutTranscript(t *testing.T) {
	run := bloatedSubagentRun("acprun_steer", domain.AcpRunRunning)
	rt := &steerStopAcpRuntime{run: run}
	app := &App{Acp: rt}

	output, err := app.SteerAcpRun(context.Background(), []byte(`{"id":"acprun_steer","text":"focus on tests"}`))
	if err != nil {
		t.Fatalf("SteerAcpRun: %v", err)
	}
	if len(output) >= 5000 {
		t.Fatalf("steer tool output too large: %d", len(output))
	}
	for _, want := range []string{
		"status: running",
		"id: acprun_steer",
		"workspace: /tmp/project",
		"Steer accepted.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("steer output missing %q:\n%s", want, output)
		}
	}
	for _, leaked := range []string{
		"transcript:",
		"prompt:",
		run.Prompt,
		"long private reasoning",
		"Intermediate progress.",
		"Progress chunk that must not leak.",
		"Last meaningful turn only.",
		"edit_file",
		"availablemodes",
		"queuedsteer:",
	} {
		if strings.Contains(output, leaked) {
			t.Errorf("steer output leaked %q:\n%s", leaked, output)
		}
	}
	if run.QueuedSteer != "focus on tests" {
		t.Fatalf("steer was not applied, queued=%q", run.QueuedSteer)
	}
}

func TestStopAcpRunReturnsCompletionShapeWithoutTranscript(t *testing.T) {
	run := bloatedSubagentRun("acprun_stop", domain.AcpRunRunning)
	storage := &waitAcpStorage{path: "/data/conversations/conv_1.acp/acprun_stop.json"}
	app := &App{
		Acp:           &steerStopAcpRuntime{run: run},
		AcpRunStorage: storage,
	}

	output, err := app.StopAcpRun(context.Background(), []byte(`{"id":"acprun_stop"}`))
	if err != nil {
		t.Fatalf("StopAcpRun: %v", err)
	}
	if len(output) >= 5000 {
		t.Fatalf("stop tool output too large: %d", len(output))
	}
	for _, want := range []string{
		"status: cancelled",
		"id: acprun_stop",
		"output_path: /data/conversations/conv_1.acp/acprun_stop.json",
		"Last meaningful turn only.",
		"[Subagent was cancelled.]",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("stop output missing %q:\n%s", want, output)
		}
	}
	for _, leaked := range []string{
		"transcript:",
		"prompt:",
		run.Prompt,
		"long private reasoning",
		"Intermediate progress.",
		"Progress chunk that must not leak.",
		"edit_file",
	} {
		if strings.Contains(output, leaked) {
			t.Errorf("stop output leaked %q:\n%s", leaked, output)
		}
	}
	if storage.record.ID != run.ID || len(storage.record.Transcript) != len(run.Transcript) {
		t.Fatalf("full run was not persisted: %+v", storage.record)
	}
}

func TestHandleAcpRunsSteerStillReturnsFullDTOForUI(t *testing.T) {
	run := bloatedSubagentRun("acprun_rpc", domain.AcpRunRunning)
	app := &App{Acp: &steerStopAcpRuntime{run: run}}

	res, rpcErr := app.handleAcpRunsSteer(contracts.AcpRunSteerRequest{ID: "acprun_rpc", Text: "keep going"})
	if rpcErr != nil {
		t.Fatalf("handleAcpRunsSteer: %+v", rpcErr)
	}
	dto, ok := res.(contracts.AcpRunDTO)
	if !ok {
		t.Fatalf("RPC result type %T, want AcpRunDTO", res)
	}
	if len(dto.Transcript) != len(run.Transcript) {
		t.Fatalf("UI DTO transcript len=%d, want %d", len(dto.Transcript), len(run.Transcript))
	}
	if dto.Prompt != run.Prompt {
		t.Fatalf("UI DTO dropped prompt")
	}
}

// --- from acp_wait_test.go ---

type waitAcpRuntime struct {
	AcpRuntime
	run     *domain.AcpRun
	waitErr error
}

func (f *waitAcpRuntime) Get(runID string) (*domain.AcpRun, bool) {
	return f.run, f.run != nil && f.run.ID == runID
}

func (f *waitAcpRuntime) Wait(context.Context, string) (*domain.AcpRun, error) {
	return f.run, f.waitErr
}

type waitAcpStorage struct {
	record domain.AcpRunRecord
	path   string
}

func (f *waitAcpStorage) Save(record domain.AcpRunRecord) error {
	f.record = record
	return nil
}

func (f *waitAcpStorage) Load(string) (domain.AcpRunRecord, bool) {
	return domain.AcpRunRecord{}, false
}

func (f *waitAcpStorage) List(string) []domain.AcpRunRecord {
	return nil
}

func (f *waitAcpStorage) Path(string, string) string {
	return f.path
}

func TestWaitAcpRunReturnsPersistedPathAndLastTurnOnly(t *testing.T) {
	run := &domain.AcpRun{
		TaskState:      domain.TaskState[domain.AcpRunStatus]{ID: "acprun_1", Status: domain.AcpRunCompleted},
		AgentID:        "acp_1",
		ConversationID: "conv_1",
		Workspace:      "/tmp/project",
		Prompt:         "private parent prompt that must not be repeated",

		Transcript: []domain.AcpTranscriptChunk{
			{Kind: "thought", Text: "long private reasoning"},
			{Kind: "text", Text: "Intermediate progress."},
			{Kind: "tool", ToolID: "tool-1", ToolTitle: "edit_file", ToolStatus: "completed"},
			{Kind: "text", Text: "Finished the fix.\n\n---\nAll focused tests pass."},
		},
	}
	storage := &waitAcpStorage{path: "/data/conversations/conv_1.acp/acprun_1.json"}
	app := &App{
		Acp:           &waitAcpRuntime{run: run, waitErr: context.DeadlineExceeded},
		AcpRunStorage: storage,
	}

	output, err := app.WaitAcpRun(context.Background(), []byte(`{"id":"acprun_1","timeout_ms":100}`))
	if err != nil {
		t.Fatalf("WaitAcpRun: %v", err)
	}
	for _, want := range []string{
		"status: completed",
		"id: acprun_1",
		"output_path: /data/conversations/conv_1.acp/acprun_1.json",
		"Finished the fix.",
		"All focused tests pass.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
	for _, leaked := range []string{
		run.Prompt,
		"long private reasoning",
		"Intermediate progress.",
		"edit_file",
		"transcript:",
	} {
		if strings.Contains(output, leaked) {
			t.Errorf("compact wait output leaked %q:\n%s", leaked, output)
		}
	}
	if storage.record.ID != run.ID || len(storage.record.Transcript) != len(run.Transcript) {
		t.Fatalf("full run was not persisted: %+v", storage.record)
	}

	providerOutput := tooloutput.ProviderToolContent("subagent_wait", output)
	if !strings.Contains(providerOutput, "Finished the fix.") ||
		!strings.Contains(providerOutput, storage.path) {
		t.Fatalf("provider summary lost compact result:\n%s", providerOutput)
	}
	if strings.Contains(providerOutput, run.Prompt) || strings.Contains(providerOutput, "long private reasoning") {
		t.Fatalf("provider summary leaked full run:\n%s", providerOutput)
	}
}

func TestWaitAcpRunDoesNotPersistRunningTimeoutSnapshot(t *testing.T) {
	run := &domain.AcpRun{
		TaskState:      domain.TaskState[domain.AcpRunStatus]{ID: "acprun_live", Status: domain.AcpRunRunning},
		ConversationID: "conv_1",
		Transcript:     []domain.AcpTranscriptChunk{{Kind: "text", Text: "Still working."}},
	}
	storage := &waitAcpStorage{path: "/data/conversations/conv_1.acp/acprun_live.json"}
	app := &App{
		Acp:           &waitAcpRuntime{run: run},
		AcpRunStorage: storage,
	}

	output, err := app.WaitAcpRun(context.Background(), []byte(`{"id":"acprun_live","timeout_ms":1}`))
	if err != nil {
		t.Fatalf("WaitAcpRun: %v", err)
	}
	if strings.Contains(output, "output_path:") {
		t.Fatalf("running snapshot must not advertise a persisted path:\n%s", output)
	}
	if storage.record.ID != "" {
		t.Fatalf("running snapshot must not be persisted: %+v", storage.record)
	}
}

func TestWaitAcpRunReturnsParentCancellation(t *testing.T) {
	run := &domain.AcpRun{
		TaskState:      domain.TaskState[domain.AcpRunStatus]{ID: "acprun_live", Status: domain.AcpRunRunning},
		ConversationID: "conv_1",
	}
	app := &App{
		Acp: &waitAcpRuntime{run: run, waitErr: context.Canceled},
	}
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	_, rpcErr := app.waitAcpRun(parent, contracts.AcpRunWaitRequest{ID: run.ID, TimeoutMS: 100})
	if rpcErr == nil {
		t.Fatal("parent cancellation must not return a successful running snapshot")
	}
	if rpcErr.Code != contracts.CodeInternal || !strings.Contains(rpcErr.Message, context.Canceled.Error()) {
		t.Fatalf("parent cancellation error = %+v", rpcErr)
	}
}

// --- from acp_runs_list_test.go ---

// fakeAcpRuntime satisfies AcpRuntime via embedding; only List is used by
// handleAcpRunsList.
type fakeAcpRuntime struct {
	AcpRuntime
	runs []*domain.AcpRun
}

func (f *fakeAcpRuntime) List(conversationID string) []*domain.AcpRun {
	if conversationID == "" {
		return f.runs
	}
	var out []*domain.AcpRun
	for _, r := range f.runs {
		if r.ConversationID == conversationID {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeAcpRuntime) Get(runID string) (*domain.AcpRun, bool) {
	for _, run := range f.runs {
		if run.ID == runID {
			return run, true
		}
	}
	return nil, false
}

// fakeAcpRunStorage implements domain.AcpRunStorage for list-merge tests.
type fakeAcpRunStorage struct {
	recs []domain.AcpRunRecord
}

func (f *fakeAcpRunStorage) Save(record domain.AcpRunRecord) error {
	f.recs = append(f.recs, record)
	return nil
}
func (f *fakeAcpRunStorage) Load(runID string) (domain.AcpRunRecord, bool) {
	for _, r := range f.recs {
		if r.ID == runID {
			return r, true
		}
	}
	return domain.AcpRunRecord{}, false
}
func (f *fakeAcpRunStorage) List(conversationID string) []domain.AcpRunRecord {
	var out []domain.AcpRunRecord
	for _, r := range f.recs {
		if conversationID == "" || r.ConversationID == conversationID {
			out = append(out, r)
		}
	}
	return out
}
func (f *fakeAcpRunStorage) Path(conversationID, runID string) string { return "" }

func TestHandleAcpRunsListMergesSettledStorageRecords(t *testing.T) {
	app := &App{
		Bus: NewBus(),
		Acp: &fakeAcpRuntime{runs: []*domain.AcpRun{
			{TaskState: domain.TaskState[domain.AcpRunStatus]{ID: "acprun_live", Status: domain.AcpRunRunning}, ConversationID: "conv_1", AgentName: "Live Agent"},
		}},
		AcpRunStorage: &fakeAcpRunStorage{recs: []domain.AcpRunRecord{
			{
				ID: "acprun_settled", ConversationID: "conv_1", Status: domain.AcpRunCompleted,
				AgentName: "Settled Agent", Workspace: "/tmp/ws",
				Transcript: []domain.AcpTranscriptChunk{{Kind: "text", Text: "all done"}},
			},
		}},
	}

	resp, rpcErr := app.handleAcpRunsList(contracts.AcpRunsListRequest{ConversationID: "conv_1"})
	if rpcErr != nil {
		t.Fatalf("runs.list: %v", rpcErr)
	}
	out, ok := resp.(contracts.AcpRunsListResult)
	if !ok || len(out.Runs) != 2 {
		t.Fatalf("expected 2 merged runs, got %+v", resp)
	}
	if out.Runs[0].ID != "acprun_live" || out.Runs[1].ID != "acprun_settled" {
		t.Fatalf("live runs must come first: %+v", out.Runs)
	}
	settled := out.Runs[1]
	if settled.Status != string(domain.AcpRunCompleted) {
		t.Fatalf("settled status = %q", settled.Status)
	}
	if len(settled.Transcript) != 1 || settled.Transcript[0].Text != "all done" {
		t.Fatalf("settled transcript not carried into DTO: %+v", settled.Transcript)
	}
}

func TestHandleAcpRunsListDedupesLiveAndStorage(t *testing.T) {
	app := &App{
		Bus: NewBus(),
		Acp: &fakeAcpRuntime{runs: []*domain.AcpRun{
			{TaskState: domain.TaskState[domain.AcpRunStatus]{ID: "acprun_shared", Status: domain.AcpRunRunning}, ConversationID: "conv_1"},
		}},
		AcpRunStorage: &fakeAcpRunStorage{recs: []domain.AcpRunRecord{
			{ID: "acprun_shared", ConversationID: "conv_1", Status: domain.AcpRunCompleted},
		}},
	}

	resp, rpcErr := app.handleAcpRunsList(contracts.AcpRunsListRequest{ConversationID: "conv_1"})
	if rpcErr != nil {
		t.Fatalf("runs.list: %v", rpcErr)
	}
	out := resp.(contracts.AcpRunsListResult)
	if len(out.Runs) != 1 {
		t.Fatalf("same run id in live + storage must appear once, got %+v", out.Runs)
	}
	if out.Runs[0].Status != string(domain.AcpRunRunning) {
		t.Fatalf("live status must win over stored copy, got %q", out.Runs[0].Status)
	}
}

func TestHandleAcpRunsListScopesToConversation(t *testing.T) {
	app := &App{
		Bus: NewBus(),
		Acp: &fakeAcpRuntime{runs: []*domain.AcpRun{
			{TaskState: domain.TaskState[domain.AcpRunStatus]{ID: "acprun_a", Status: domain.AcpRunRunning}, ConversationID: "conv_a"},
			{TaskState: domain.TaskState[domain.AcpRunStatus]{ID: "acprun_b", Status: domain.AcpRunRunning}, ConversationID: "conv_b"},
		}},
		AcpRunStorage: &fakeAcpRunStorage{recs: []domain.AcpRunRecord{
			{ID: "acprun_old", ConversationID: "conv_a", Status: domain.AcpRunCompleted},
			{ID: "acprun_other", ConversationID: "conv_b", Status: domain.AcpRunCompleted},
		}},
	}

	resp, rpcErr := app.handleAcpRunsList(contracts.AcpRunsListRequest{ConversationID: "conv_a"})
	if rpcErr != nil {
		t.Fatalf("runs.list: %v", rpcErr)
	}
	out := resp.(contracts.AcpRunsListResult)
	if len(out.Runs) != 2 {
		t.Fatalf("conv_a should see only its own runs, got %+v", out.Runs)
	}
	for _, r := range out.Runs {
		if r.ConversationID != "conv_a" {
			t.Fatalf("leaked run from another conversation: %+v", r)
		}
	}
}

func TestHandleAcpRunsGetLoadsPersistedRecordAfterRuntimeRestart(t *testing.T) {
	app := &App{
		Bus: NewBus(),
		Acp: &fakeAcpRuntime{},
		AcpRunStorage: &fakeAcpRunStorage{recs: []domain.AcpRunRecord{
			{
				ID: "acprun_historic", ConversationID: "conv_1", AgentName: "Devin",
				Status:     domain.AcpRunCompleted,
				Transcript: []domain.AcpTranscriptChunk{{Kind: "text", Text: "persisted result"}},
			},
		}},
	}

	result, rpcErr := app.handleAcpRunsGet(contracts.AcpRunIDRequest{ID: "acprun_historic"})
	if rpcErr != nil {
		t.Fatalf("runs.get: %v", rpcErr)
	}
	run, ok := result.(contracts.AcpRunDTO)
	if !ok {
		t.Fatalf("runs.get result = %T, want contracts.AcpRunDTO", result)
	}
	if run.ID != "acprun_historic" || run.AgentName != "Devin" || len(run.Transcript) != 1 {
		t.Fatalf("persisted run not restored: %+v", run)
	}
}

func TestHandleAcpRunsListLoadsPersistedRecordsWithoutRuntime(t *testing.T) {
	app := &App{
		AcpRunStorage: &fakeAcpRunStorage{recs: []domain.AcpRunRecord{
			{ID: "acprun_after_restart", ConversationID: "conv_1", Status: domain.AcpRunCompleted},
		}},
	}

	result, rpcErr := app.handleAcpRunsList(contracts.AcpRunsListRequest{ConversationID: "conv_1"})
	if rpcErr != nil {
		t.Fatalf("runs.list: %v", rpcErr)
	}
	out, ok := result.(contracts.AcpRunsListResult)
	if !ok || len(out.Runs) != 1 || out.Runs[0].ID != "acprun_after_restart" {
		t.Fatalf("persisted runs without runtime = %+v, want one historical run", result)
	}
}

// --- from delegate_agent_test.go ---

type delegateSettingsStore struct {
	settings domain.Settings
}

func (s *delegateSettingsStore) Get() domain.Settings { return s.settings }

func (s *delegateSettingsStore) Set(settings domain.Settings) error {
	s.settings = settings
	return nil
}

func TestResolveDelegateModelUsesTheConfiguredModel(t *testing.T) {
	app := &App{
		Settings: &delegateSettingsStore{settings: domain.Settings{DelegateModel: "cheap:model"}},
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{
			"c1": {ID: "c1", Model: "parent:model"},
		}},
	}

	got, err := app.resolveDelegateModel("c1")
	if err != nil {
		t.Fatalf("resolveDelegateModel: %v", err)
	}
	if got != "cheap:model" {
		t.Fatalf("delegate model = %q, want configured model", got)
	}
}

func TestResolveDelegateModelDefaultsToTheParentModel(t *testing.T) {
	app := &App{
		Settings: &delegateSettingsStore{},
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{
			"c1": {ID: "c1", Model: "parent:model"},
		}},
	}

	got, err := app.resolveDelegateModel("c1")
	if err != nil {
		t.Fatalf("resolveDelegateModel: %v", err)
	}
	if got != "parent:model" {
		t.Fatalf("delegate model = %q, want parent model", got)
	}
}

func TestDelegateModelSettingRoundTripsThroughSettingsRPC(t *testing.T) {
	settings := &delegateSettingsStore{settings: domain.DefaultSettings()}
	app := NewApp(Deps{Settings: settings})
	configured := "  cheap:model  "

	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{DelegateModel: &configured}); rpcErr != nil {
		t.Fatalf("settings.set delegate model: %v", rpcErr)
	}
	if got := settings.Get().DelegateModel; got != "cheap:model" {
		t.Fatalf("stored delegate model = %q, want trimmed value", got)
	}
	result, rpcErr := app.handleSettingsGet()
	if rpcErr != nil {
		t.Fatalf("settings.get: %v", rpcErr)
	}
	if got := result.(contracts.SettingsGetResult).Settings.DelegateModel; got != "cheap:model" {
		t.Fatalf("settings DTO delegate model = %q, want configured value", got)
	}

	empty := ""
	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{DelegateModel: &empty}); rpcErr != nil {
		t.Fatalf("clear delegate model: %v", rpcErr)
	}
	if got := settings.Get().DelegateModel; got != "" {
		t.Fatalf("cleared delegate model = %q, want empty inherit setting", got)
	}
}

func TestDelegateRunSurfaceUsesTheCompleteHeadlessTranscript(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	hidden := &domain.Conversation{
		ID: "conv_delegate",
		Messages: []domain.Message{
			{
				ID: "msg_round_1", Role: domain.RoleAssistant, CreatedAt: now, Status: domain.StatusDone,
				Steps: []domain.MessageStep{
					{Type: domain.StepText, Content: "I will inspect the file first."},
					{Type: domain.StepToolCalls, ToolCalls: []domain.ToolCall{{
						ID: "call_read", Name: "file_read", Status: domain.ToolOK, Output: "file contents",
					}}},
				},
			},
			{
				ID: "msg_round_2", Role: domain.RoleAssistant, CreatedAt: now.Add(time.Second), Status: domain.StatusDone,
				Steps: []domain.MessageStep{{Type: domain.StepText, Content: "The requested change is complete."}},
			},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"conv_delegate": hidden}},
		Bus:           NewBus(),
	}
	_, running := app.registerDelegateRun("run_delegate", "call_parent", "conv_parent", "/workspace", "Inspect and fix the file", "cheap:model")
	if running.Status != domain.AcpRunRunning {
		t.Fatalf("registered delegate status = %q, want running", running.Status)
	}

	finished := app.finishDelegateRun("run_delegate", "conv_delegate", "The requested change is complete.", nil)
	if finished == nil || finished.Status != domain.AcpRunCompleted {
		t.Fatalf("finished delegate = %+v, want completed run", finished)
	}
	if len(finished.Transcript) != 3 {
		t.Fatalf("transcript chunks = %+v, want acknowledgement, tool, and final output", finished.Transcript)
	}
	if finished.Transcript[0].Text != "I will inspect the file first." {
		t.Fatalf("first transcript chunk = %+v, want the preliminary acknowledgement", finished.Transcript[0])
	}
	if finished.Transcript[1].Kind != "tool" || finished.Transcript[1].Text != "file contents" {
		t.Fatalf("tool transcript chunk = %+v", finished.Transcript[1])
	}
	if finished.Transcript[2].Text != "The requested change is complete." {
		t.Fatalf("delegate result lost final assistant output: %+v", finished.Transcript)
	}

	listed, rpcErr := app.handleAcpRunsList(contracts.AcpRunsListRequest{ConversationID: "conv_parent"})
	if rpcErr != nil {
		t.Fatalf("delegate run list: %v", rpcErr)
	}
	runs := listed.(contracts.AcpRunsListResult).Runs
	if len(runs) != 1 || runs[0].ID != "run_delegate" || len(runs[0].Transcript) != 3 {
		t.Fatalf("delegate run must be visible through ACP-shaped list: %+v", runs)
	}
	got, rpcErr := app.handleAcpRunsGet(contracts.AcpRunIDRequest{ID: "run_delegate"})
	if rpcErr != nil {
		t.Fatalf("delegate run get: %v", rpcErr)
	}
	if got.(contracts.AcpRunDTO).CurrentModelID != "model" {
		t.Fatalf("delegate UI model = %q, want bare model id", got.(contracts.AcpRunDTO).CurrentModelID)
	}
}

// TestDeliverRunDoneQueuesWhileParentTurnActive pins the shared
// background-run delivery path: a live parent turn queues the completion
// for the next tool-round boundary; an idle parent gets the injection
// immediately plus a completion turn.
func TestDeliverRunDoneQueuesWhileParentTurnActive(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "work", Status: domain.StatusDone},
			{
				ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{ID: "call_parent", Name: "delegate", Status: domain.ToolRunning}},
			},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), runs: map[string]*TurnRun{}}
	parent := &TurnRun{ID: "turn1", ConversationID: "c1"}
	app.runs[parent.ID] = parent
	app.trackPendingRun("c1", "run_del", "delegate")

	turnLock := app.conversationTurnLock("c1")
	turnLock.Lock()
	defer turnLock.Unlock()

	done := make(chan struct{})
	go func() {
		app.deliverRunDone("c1", pendingRunDone{
			RunID: "run_del",
			Complete: func(cid string) error {
				return app.completeDelegateRunLocked(cid, "run_del", "call_parent", domain.ToolOK, "result text", "run_del_conv")
			},
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("deliverRunDone blocked on the parent turn lock")
	}

	if store.convs["c1"].Messages[1].ToolCalls[0].Status != domain.ToolRunning {
		t.Fatal("must not mutate the conversation until the tool-round boundary")
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
		t.Fatal("queued delegate completion was not applied")
	}
	saved := store.convs["c1"]
	if saved.Messages[1].ToolCalls[0].Status != domain.ToolOK {
		t.Fatalf("original tool call status = %v, want ok", saved.Messages[1].ToolCalls[0].Status)
	}
	if len(saved.Messages) != 3 || saved.Messages[2].ToolCalls[0].Name != domain.DelegateResultToolName {
		t.Fatalf("synthetic delegate_result missing: %+v", saved.Messages)
	}
	if app.hasPendingRuns("c1") {
		t.Fatal("pending delegate must be untracked after drain")
	}
}

func TestCompleteDelegateRunInjectsSyntheticMessage(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "work", Status: domain.StatusDone},
			{
				ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{ID: "call_parent", Name: "delegate", Args: `{"prompt":"delegate this"}`, Status: domain.ToolRunning}},
			},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus()}
	_, events, unsubscribe := app.Bus.Subscribe()
	defer unsubscribe()

	if err := app.completeDelegateRunLocked("c1", "run_del", "call_parent", domain.ToolOK, "all done", "run_del_conv"); err != nil {
		t.Fatalf("completeDelegateRunLocked: %v", err)
	}
	saved := store.convs["c1"]
	if saved.Messages[1].ToolCalls[0].Status != domain.ToolOK {
		t.Fatalf("original tool call status = %v, want ok", saved.Messages[1].ToolCalls[0].Status)
	}
	if !strings.Contains(saved.Messages[1].ToolCalls[0].Output, "completed") {
		t.Fatalf("original tool call must carry the brief: %q", saved.Messages[1].ToolCalls[0].Output)
	}
	synthetic := saved.Messages[2]
	if synthetic.Role != domain.RoleAssistant || len(synthetic.ToolCalls) != 1 {
		t.Fatalf("synthetic message missing: %+v", synthetic)
	}
	stc := synthetic.ToolCalls[0]
	if stc.Name != domain.DelegateResultToolName || !domain.IsDelegateResultCallID(stc.ID) {
		t.Fatalf("synthetic tool call wrong: %+v", stc)
	}
	if stc.Output != "all done" {
		t.Fatalf("synthetic tool call must carry the full output: %q", stc.Output)
	}
	if !strings.Contains(stc.Args, "run_del") || !strings.Contains(stc.Args, "run_del_conv") {
		t.Fatalf("synthetic args must carry run + conversation ids: %q", stc.Args)
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
		if string(completed.Args) != `{"prompt":"delegate this"}` {
			t.Fatalf("completion args = %s, want original delegate args", completed.Args)
		}
		if completed.Presentation == nil || !strings.Contains(completed.Presentation.Request, "delegate this") {
			t.Fatalf("completion presentation must retain the request: %+v", completed.Presentation)
		}
	case <-time.After(time.Second):
		t.Fatal("delegate completion event was not published")
	}
}

// TestCompleteDelegateRunInjectsFailure pins the failure path: a failed
// delegate still delivers a synthetic result carrying the error text.
func TestCompleteDelegateRunInjectsFailure(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "work", Status: domain.StatusDone},
			{
				ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{ID: "call_parent", Name: "delegate", Status: domain.ToolRunning}},
			},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus()}

	if err := app.completeDelegateRunLocked("c1", "run_del", "call_parent", domain.ToolFailed, "error: boom", "run_del_conv"); err != nil {
		t.Fatalf("completeDelegateRunLocked: %v", err)
	}
	saved := store.convs["c1"]
	if saved.Messages[1].ToolCalls[0].Status != domain.ToolFailed {
		t.Fatalf("original tool call status = %v, want failed", saved.Messages[1].ToolCalls[0].Status)
	}
	if len(saved.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (synthetic result must be injected)", len(saved.Messages))
	}
	stc := saved.Messages[2].ToolCalls[0]
	if stc.Status != domain.ToolFailed || !strings.Contains(stc.Output, "error: boom") {
		t.Fatalf("failed delegate result wrong: %+v", stc)
	}
}
