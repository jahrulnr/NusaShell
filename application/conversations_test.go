package application

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	convs    map[string]*domain.Conversation
	archived []domain.Message
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
	f.archived = append(f.archived, messages...)
	return 0, nil
}

func (f *fakeConvStore) GetChunk(id string, index int) ([]domain.Message, error) {
	return nil, errNotFound
}

// memCreds is an in-memory CredentialStore for tests.
type memCreds struct {
	m map[string]string
}

func (c *memCreds) Get(id string) (string, bool, error) {
	v, ok := c.m[id]
	return v, ok, nil
}

func (c *memCreds) Set(id, v string) error {
	if c.m == nil {
		c.m = map[string]string{}
	}
	c.m[id] = v
	return nil
}

func (c *memCreds) Delete(id string) error {
	delete(c.m, id)
	return nil
}

func (c *memCreds) ListByPrefix(prefix string) ([]string, error) {
	var ids []string
	for k := range c.m {
		if strings.HasPrefix(k, prefix) {
			ids = append(ids, k)
		}
	}
	return ids, nil
}

// fakeProviderStore is a minimal in-memory ProviderStore for tests.
type fakeProviderStore struct {
	items map[string]*domain.Provider
}

func (f *fakeProviderStore) List() []*domain.Provider {
	out := make([]*domain.Provider, 0, len(f.items))
	for _, p := range f.items {
		out = append(out, p)
	}
	return out
}

func (f *fakeProviderStore) Get(id string) (*domain.Provider, error) {
	p, ok := f.items[id]
	if !ok {
		return nil, errNotFound
	}
	return p, nil
}

func (f *fakeProviderStore) Save(p *domain.Provider) error {
	if f.items == nil {
		f.items = map[string]*domain.Provider{}
	}
	f.items[p.ID] = p
	return nil
}

func (f *fakeProviderStore) Delete(id string) error {
	delete(f.items, id)
	return nil
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

func TestMsgDTOIncludesToolOutputAttachmentsWithoutDataURL(t *testing.T) {
	dto := msgDTO(domain.Message{
		ID:        "m1",
		Role:      domain.RoleAssistant,
		Content:   "done",
		CreatedAt: time.Time{},
		ToolCalls: []domain.ToolCall{{
			ID: "tc1", Name: "generate_image", Status: domain.ToolOK, Output: "saved",
			OutputAttachments: []domain.Attachment{{
				Type: "image", Name: "gen-tc1.png", MediaType: "image/png",
				DataURL: "data:image/png;base64,AAAA", FilePath: "/tmp/gen-tc1.png",
			}},
		}},
	})
	if len(dto.ToolCalls) != 1 || len(dto.ToolCalls[0].OutputAttachments) != 1 {
		t.Fatalf("dto = %+v", dto.ToolCalls)
	}
	att := dto.ToolCalls[0].OutputAttachments[0]
	if att.FilePath != "/tmp/gen-tc1.png" || att.DataURL != "" {
		t.Fatalf("attachment = %+v, DataURL must be omitted", att)
	}
}
