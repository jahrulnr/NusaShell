package application

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/domain"
)

type fakeChangeJournal struct {
	mu      sync.Mutex
	wraps   []journalWrapRecord
	session func(ctx context.Context, conversationID, workspaceRoot string) (*WorkspaceState, error)
}

type journalWrapRecord struct {
	req     MutationRequest
	execRan bool
}

func (f *fakeChangeJournal) WrapMutation(_ context.Context, req MutationRequest, exec func() error) error {
	f.mu.Lock()
	f.wraps = append(f.wraps, journalWrapRecord{req: req})
	idx := len(f.wraps) - 1
	f.mu.Unlock()
	err := exec()
	f.mu.Lock()
	f.wraps[idx].execRan = true
	f.mu.Unlock()
	return err
}

func (f *fakeChangeJournal) SessionState(ctx context.Context, conversationID, workspaceRoot string) (*WorkspaceState, error) {
	if f.session != nil {
		return f.session(ctx, conversationID, workspaceRoot)
	}
	return nil, nil
}

func (f *fakeChangeJournal) RecordCompaction(_ string, _ domain.CompactionEvent) error { return nil }

func (f *fakeChangeJournal) lastWrap() (journalWrapRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.wraps) == 0 {
		return journalWrapRecord{}, false
	}
	return f.wraps[len(f.wraps)-1], true
}

func TestRunOneToolWrapsDeclaredMutation(t *testing.T) {
	journal := &fakeChangeJournal{}
	app := &App{
		Bus:     NewBus(),
		Toolbox: &recordingToolbox{},
		Journal: journal,
	}
	run := &TurnRun{
		ID:             "run1",
		ConversationID: "conv1",
		Workspace:      "/tmp/ws",
		Ctx:            context.Background(),
	}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "file_write",
		Args: `{"path":"/tmp/ws/foo.txt","content":"hi"}`,
	}

	res := app.runOneTool(run, "m1", toolCall, ModelCapabilities{}, domain.Settings{}, 1)
	if res.status != domain.ToolOK {
		t.Fatalf("status = %v, want ok", res.status)
	}
	rec, ok := journal.lastWrap()
	if !ok {
		t.Fatal("WrapMutation not called")
	}
	if rec.req.Class != domain.MutationDeclared {
		t.Fatalf("class = %q, want declared", rec.req.Class)
	}
	if rec.req.ConversationID != "conv1" || rec.req.RunID != "run1" || rec.req.ToolCallID != "tc1" {
		t.Fatalf("ids = %+v", rec.req)
	}
	if rec.req.WorkspaceRoot != "/tmp/ws" {
		t.Fatalf("workspace root = %q, want /tmp/ws", rec.req.WorkspaceRoot)
	}
	if !rec.execRan {
		t.Fatal("exec closure not invoked")
	}
}

func TestRunOneToolSkipsJournalForNonMutating(t *testing.T) {
	journal := &fakeChangeJournal{}
	app := &App{
		Bus:     NewBus(),
		Toolbox: &recordingToolbox{},
		Journal: journal,
	}
	run := &TurnRun{ID: "run1", ConversationID: "conv1", Workspace: "/tmp/ws", Ctx: context.Background()}
	toolCall := domain.ToolCall{ID: "tc1", Name: "file_read", Args: `{"path":"/tmp/ws/foo.txt"}`}

	app.runOneTool(run, "m1", toolCall, ModelCapabilities{}, domain.Settings{}, 1)
	if _, ok := journal.lastWrap(); ok {
		t.Fatal("WrapMutation should not be called for file_read")
	}
}

func TestRunOneToolSkipsJournalWhenNil(t *testing.T) {
	app := &App{Bus: NewBus(), Toolbox: &recordingToolbox{}}
	run := &TurnRun{ID: "run1", ConversationID: "conv1", Workspace: "/tmp/ws", Ctx: context.Background()}
	toolCall := domain.ToolCall{ID: "tc1", Name: "file_write", Args: `{"path":"/tmp/ws/foo.txt"}`}

	app.runOneTool(run, "m1", toolCall, ModelCapabilities{}, domain.Settings{}, 1)
}

type serializeProbeToolbox struct {
	mu                           sync.Mutex
	mutatingInFlight             int
	mutatingMaxConcurrent        int
	sawNonMutatingDuringMutating bool
}

func (p *serializeProbeToolbox) ListTools() []ToolInfo { return nil }

func (p *serializeProbeToolbox) Execute(_ context.Context, name string, _ []byte) (string, error) {
	isMutating := name == "file_write"
	if !isMutating {
		deadline := time.Now().Add(200 * time.Millisecond)
		for time.Now().Before(deadline) {
			p.mu.Lock()
			active := p.mutatingInFlight > 0
			p.mu.Unlock()
			if active {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
	}

	p.mu.Lock()
	if isMutating {
		p.mutatingInFlight++
		if p.mutatingInFlight > p.mutatingMaxConcurrent {
			p.mutatingMaxConcurrent = p.mutatingInFlight
		}
	} else if p.mutatingInFlight > 0 {
		p.sawNonMutatingDuringMutating = true
	}
	p.mu.Unlock()

	if isMutating {
		time.Sleep(80 * time.Millisecond)
	} else {
		time.Sleep(10 * time.Millisecond)
	}

	p.mu.Lock()
	if isMutating {
		p.mutatingInFlight--
	}
	p.mu.Unlock()
	return "ok", nil
}

func TestExecuteTurnToolsSerializesMutationsPerRoot(t *testing.T) {
	box := &serializeProbeToolbox{}
	journal := &fakeChangeJournal{}
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{{
			ID: "m1",
			ToolCalls: []domain.ToolCall{
				{ID: "t1", Name: "file_write", Args: `{"path":"/tmp/ws/a.txt"}`},
				{ID: "t2", Name: "file_write", Args: `{"path":"/tmp/ws/b.txt"}`},
				{ID: "t3", Name: "file_read", Args: `{"path":"/tmp/ws/a.txt"}`},
			},
		}},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       box,
		Journal:       journal,
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Workspace: "/tmp/ws", Ctx: context.Background(), Cancel: func() {}}

	if err := app.executeTurnTools(run, "m1", conv.Messages[0].ToolCalls, ModelCapabilities{}, domain.Settings{}, 1); err != nil {
		t.Fatalf("executeTurnTools: %v", err)
	}
	if box.mutatingMaxConcurrent > 1 {
		t.Fatalf("mutating max concurrent = %d, want 1 (serialized per root)", box.mutatingMaxConcurrent)
	}
	if !box.sawNonMutatingDuringMutating {
		t.Fatal("non-mutating tool should overlap with in-flight mutating tool")
	}
	if len(journal.wraps) != 2 {
		t.Fatalf("wrap count = %d, want 2 (only file_write calls)", len(journal.wraps))
	}
}

func TestHydrationWorkspaceStateSlot(t *testing.T) {
	journal := &fakeChangeJournal{
		session: func(_ context.Context, convID, workspace string) (*WorkspaceState, error) {
			return &WorkspaceState{
				ConversationID: convID,
				WorkspaceRoot:  workspace,
				Changes: []domain.FileChange{{
					Path: "/tmp/ws/changed.go",
					Kind: domain.ChangeModified,
				}},
			}, nil
		},
	}
	builder := NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/tmp/ws"},
		ConvID:         "conv1",
		Journal:        journal,
	})
	result := builder.Build()
	found := false
	for _, m := range result.Messages {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Name == "workspace_state" {
				found = true
				var args map[string]string
				if err := json.Unmarshal([]byte(tc.Args), &args); err != nil {
					t.Fatalf("args json: %v", err)
				}
				if args["conversation_id"] != "conv1" || args["workspace"] != "/tmp/ws" {
					t.Fatalf("args = %+v", args)
				}
			}
		}
		if m.Role == "tool" && m.ToolResult != nil {
			var state WorkspaceState
			if err := json.Unmarshal([]byte(m.ToolResult.Content), &state); err != nil {
				t.Fatalf("content json: %v", err)
			}
			if len(state.Changes) != 1 || state.Changes[0].Path != "/tmp/ws/changed.go" {
				t.Fatalf("state = %+v", state)
			}
			if !strings.Contains(state.Hint, `docs_read(id="data-locations")`) {
				t.Fatalf("workspace_state hint missing docs pointer: %q", state.Hint)
			}
		}
	}
	if !found {
		t.Fatal("workspace_state slot missing")
	}
}

func TestHydrationWorkspaceStateHiddenWhenEmpty(t *testing.T) {
	cases := []struct {
		name    string
		journal ChangeJournal
	}{
		{"nil journal", nil},
		{"empty changes", &fakeChangeJournal{session: func(context.Context, string, string) (*WorkspaceState, error) {
			return &WorkspaceState{Changes: nil}, nil
		}}},
		{"session error", &fakeChangeJournal{session: func(context.Context, string, string) (*WorkspaceState, error) {
			return nil, context.Canceled
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builder := NewHydrationBuilder(HydrationSource{
				RuntimeContext: RuntimeContextSnapshot{Workspace: "/tmp/ws"},
				ConvID:         "conv1",
				Journal:        tc.journal,
			})
			for _, m := range builder.Build().Messages {
				for _, call := range m.ToolCalls {
					if call.Name == "workspace_state" {
						t.Fatal("workspace_state slot should be hidden")
					}
				}
			}
		})
	}
}
