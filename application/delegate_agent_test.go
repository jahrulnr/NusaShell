package application

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

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
