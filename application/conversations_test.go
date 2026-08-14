package application

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

var errNotFound = errors.New("not found")

// fakeLogStore is a no-op LogStore for testing.
type fakeLogStore struct{}

func (f *fakeLogStore) Append(e *domain.LogEntry) {}
func (f *fakeLogStore) List(level string, limit int) []*domain.LogEntry {
	return nil
}
func (f *fakeLogStore) Clear() {}

// fakeConvStore is a minimal in-memory ConversationStore for testing.
type fakeConvStore struct {
	convs map[string]*domain.Conversation
}

func (f *fakeConvStore) List() []*domain.Conversation {
	out := make([]*domain.Conversation, 0, len(f.convs))
	for _, c := range f.convs {
		out = append(out, c)
	}
	return out
}

func (f *fakeConvStore) Get(id string) (*domain.Conversation, error) {
	c, ok := f.convs[id]
	if !ok {
		return nil, errNotFound
	}
	return c, nil
}

func (f *fakeConvStore) Save(c *domain.Conversation) error {
	if f.convs == nil {
		f.convs = map[string]*domain.Conversation{}
	}
	f.convs[c.ID] = c
	return nil
}

func (f *fakeConvStore) Delete(id string) error {
	delete(f.convs, id)
	return nil
}

func (f *fakeConvStore) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	return 0, nil
}

func (f *fakeConvStore) GetChunk(id string, index int) ([]domain.Message, error) {
	return nil, errNotFound
}

func TestHandleConversationsDeleteClearsTodos(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}
	todoPort := &fakeTodoPort{items: map[string][]domain.TodoItem{
		"conv_1": {{ID: "1", Content: "Task", Status: domain.TodoPending}},
	}}

	app := &App{Conversations: convStore, Todos: todoPort, Logs: &fakeLogStore{}, Bus: NewBus()}

	resp, rpcErr := app.handleConversationsDelete(contracts.ConversationIDRequest{ID: "conv_1"})
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	if m, ok := resp.(map[string]bool); !ok || !m["ok"] {
		t.Errorf("expected {ok:true}, got %+v", resp)
	}
	// Conversation should be gone
	if _, err := convStore.Get("conv_1"); err == nil {
		t.Error("expected conversation to be deleted")
	}
	// Todos should be cleared
	if items := todoPort.Get("conv_1"); items != nil {
		t.Errorf("expected todos cleared, got %+v", items)
	}
}

func TestHandleConversationsDeleteMissingConversation(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{}}
	todoPort := &fakeTodoPort{items: map[string][]domain.TodoItem{}}

	app := &App{Conversations: convStore, Todos: todoPort, Logs: &fakeLogStore{}, Bus: NewBus()}

	_, rpcErr := app.handleConversationsDelete(contracts.ConversationIDRequest{ID: "nope"})
	if rpcErr == nil {
		t.Fatal("expected rpc error for missing conversation")
	}
	// Todos should not be touched
	if items := todoPort.Get("nope"); items != nil {
		t.Errorf("expected no todo mutation, got %+v", items)
	}
}

func TestHandleConversationsDeleteNilTodos(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}

	app := &App{Conversations: convStore, Todos: nil, Logs: &fakeLogStore{}, Bus: NewBus()}

	resp, rpcErr := app.handleConversationsDelete(contracts.ConversationIDRequest{ID: "conv_1"})
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error with nil Todos: %v", rpcErr)
	}
	if m, ok := resp.(map[string]bool); !ok || !m["ok"] {
		t.Errorf("expected {ok:true}, got %+v", resp)
	}
}

func TestHandleConversationsDeleteCancelsActiveRun(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	app := NewApp(Deps{Conversations: convStore, Logs: &fakeLogStore{}, Bus: NewBus()})
	app.runs["run_1"] = &TurnRun{ID: "run_1", ConversationID: "conv_1", Ctx: ctx, Cancel: cancel}

	if _, rpcErr := app.handleConversationsDelete(contracts.ConversationIDRequest{ID: "conv_1"}); rpcErr != nil {
		t.Fatalf("delete: %v", rpcErr)
	}
	if ctx.Err() == nil {
		t.Fatal("expected active run to be cancelled")
	}
}

func TestHandleConversationsPickWorkspaceRejectsRelativePath(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}
	app := &App{
		Conversations: convStore,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			return filepath.Join("rel", "workspace"), nil
		}),
	}

	_, rpcErr := app.handleConversationsPickWorkspace(contracts.ConversationIDRequest{ID: "conv_1"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR for a relative workspace, got %+v", rpcErr)
	}
}

func TestHandleConversationsPickWorkspaceAcceptsAbsolutePath(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}
	workspace := t.TempDir()
	app := &App{
		Conversations: convStore,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			return workspace, nil
		}),
	}

	resp, rpcErr := app.handleConversationsPickWorkspace(contracts.ConversationIDRequest{ID: "conv_1"})
	if rpcErr != nil {
		t.Fatalf("absolute workspace rejected: %v", rpcErr)
	}
	got, ok := resp.(contracts.ConversationGetResult)
	if !ok || got.Conversation.Workspace != workspace {
		t.Fatalf("workspace = %+v, want %q", resp, workspace)
	}
}
