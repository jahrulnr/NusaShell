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
	saveErr  error
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
	if f.saveErr != nil {
		return f.saveErr
	}
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

// TestHandleConversationsPickWorkspaceSerializesTurnSave reproduces the
// workspace-picker/turn-completion race. The real JSON store returns clones;
// if the picker saves its pre-picker clone after a turn saves the latest
// message, that completed turn disappears from the conversation file.
func TestHandleConversationsPickWorkspaceSerializesTurnSave(t *testing.T) {
	workspace := t.TempDir()
	store := &cloningConvStore{
		conv: &domain.Conversation{
			ID:    "conv_1",
			Title: "Test",
			Messages: []domain.Message{
				{ID: "m1", Role: domain.RoleUser, Content: "first", Status: domain.StatusDone},
			},
		},
	}
	pickerStarted := make(chan struct{})
	releasePicker := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releasePicker)
		}
	}()
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			close(pickerStarted)
			<-releasePicker
			return workspace, nil
		}),
	}

	pickDone := make(chan *contracts.RPCError, 1)
	go func() {
		_, rpcErr := app.handleConversationsPickWorkspace(contracts.ConversationIDRequest{ID: "conv_1"})
		pickDone <- rpcErr
	}()
	<-pickerStarted

	// Model the final persistence step of a concurrent turn. It is allowed to
	// complete while the native picker is open; the picker must re-read the
	// latest snapshot before it applies the workspace change.
	turnSaveStarted := make(chan struct{})
	turnSaveDone := make(chan struct{})
	go func() {
		close(turnSaveStarted)
		turnLock := app.conversationTurnLock("conv_1")
		turnLock.Lock()
		defer turnLock.Unlock()
		latest, err := store.Get("conv_1")
		if err != nil {
			t.Errorf("turn Get: %v", err)
			close(turnSaveDone)
			return
		}
		latest.AddMessage(domain.Message{ID: "m2", Role: domain.RoleAssistant, Content: "latest turn", Status: domain.StatusDone})
		if err := store.Save(latest); err != nil {
			t.Errorf("turn Save: %v", err)
		}
		close(turnSaveDone)
	}()
	<-turnSaveStarted

	select {
	case <-turnSaveDone:
	case <-time.After(time.Second):
		t.Fatal("turn save did not complete while workspace picker was open")
	}

	close(releasePicker)
	released = true
	if rpcErr := <-pickDone; rpcErr != nil {
		t.Fatalf("pick workspace: %v", rpcErr)
	}
	select {
	case <-turnSaveDone:
	case <-time.After(time.Second):
		t.Fatal("turn save did not complete after workspace picker released")
	}

	got, err := store.Get("conv_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != workspace {
		t.Fatalf("workspace = %q, want %q", got.Workspace, workspace)
	}
	foundLatest := false
	for _, message := range got.Messages {
		if message.ID == "m2" && message.Content == "latest turn" {
			foundLatest = true
			break
		}
	}
	if !foundLatest {
		t.Fatalf("messages = %+v, want completed turn preserved", got.Messages)
	}
}

// TestHandleConversationsPickWorkspaceEmptyRoomDoesNotInsertHydration pins
// that picking a workspace before the first user must not persist a
// checkpoint at index 0. The first turn's ensureFreshRoomHydration parks it
// after the user so OpenAI/Claude see system → user → hydration.
func TestHandleConversationsPickWorkspaceEmptyRoomDoesNotInsertHydration(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}
	workspace := t.TempDir()
	app := &App{
		Conversations: convStore,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			return workspace, nil
		}),
	}
	if _, rpcErr := app.handleConversationsPickWorkspace(contracts.ConversationIDRequest{ID: "conv_1"}); rpcErr != nil {
		t.Fatalf("pick workspace: %v", rpcErr)
	}
	saved := convStore.convs["conv_1"]
	for _, m := range saved.Messages {
		if isHydrationMessage(m) {
			t.Fatalf("empty room persisted hydration before any user: %+v", m)
		}
	}
}

// TestHandleConversationsPickWorkspaceInvalidatesHydration pins the
// workspace-switch epoch reset: a persisted hydration checkpoint (stale
// runtime_context + AGENTS.md for the OLD workspace) is stripped when the
// workspace changes, and a fresh checkpoint for the NEW workspace is rebuilt
// in the same Save — same epoch semantics as compaction. The turn loop no
// longer re-injects hydration, so the rebuild must happen here.
func TestHandleConversationsPickWorkspaceInvalidatesHydration(t *testing.T) {
	hydID := domain.HydrateToolCallPrefix + "abc123_0"
	conv := &domain.Conversation{
		ID:    "conv_1",
		Title: "Test",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "hello"},
			{ID: "m2", Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{
				{ID: hydID, Name: "runtime_context", Args: "{}", Output: `{"workspace":"/old/ws"}`},
				{ID: domain.HydrateToolCallPrefix + "abc123_1", Name: "file_read",
					Args: `{"path":"/old/ws/AGENTS.md"}`, Output: "old rules"},
			}, Status: domain.StatusDone},
			{ID: "m3", Role: domain.RoleAssistant, Content: "hi there"},
		},
	}
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	newWS := t.TempDir()
	app := &App{
		Conversations: convStore,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			return newWS, nil
		}),
	}
	if _, rpcErr := app.handleConversationsPickWorkspace(contracts.ConversationIDRequest{ID: "conv_1"}); rpcErr != nil {
		t.Fatalf("pick workspace: %v", rpcErr)
	}
	saved := convStore.convs["conv_1"]
	if saved.Workspace != newWS {
		t.Fatalf("workspace = %q, want %q", saved.Workspace, newWS)
	}
	// The OLD hydration checkpoint is gone; a fresh one is rebuilt in its
	// place (after the first user, same epoch anchor). Real messages survive.
	oldHydGone := true
	freshHydCount := 0
	for _, m := range saved.Messages {
		for _, tc := range m.ToolCalls {
			if domain.IsHydrationCallID(tc.ID) {
				freshHydCount++
				// The old checkpoint's runtime_context output referenced /old/ws.
				if tc.Name == "runtime_context" && strings.Contains(tc.Output, "/old/ws") {
					oldHydGone = false
				}
			}
		}
	}
	if !oldHydGone {
		t.Fatal("stale hydration checkpoint referencing /old/ws survived workspace switch")
	}
	if freshHydCount == 0 {
		t.Fatal("no fresh hydration checkpoint rebuilt after workspace switch; the turn loop no longer re-injects")
	}
	// Real messages survive and keep their order: user first, assistant last.
	if saved.Messages[0].Content != "hello" {
		t.Errorf("first real message lost or reordered: %+v", saved.Messages[0])
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

func TestMsgDTOSeparatesRawToolOutputFromFrontendPresentation(t *testing.T) {
	rawOutput := "---\ncount: 2\ntotal: 12K\n---\n-rw-r--r-- 4K Aug 30 15:36 file-a\n-rw-r--r-- 8K Aug 30 15:38 file with spaces-b"
	dto := msgDTO(domain.Message{
		ID:        "m1",
		Role:      domain.RoleAssistant,
		CreatedAt: time.Time{},
		ToolCalls: []domain.ToolCall{{
			ID: "tc1", Name: "file_list", Args: `{"path":"/workspace"}`,
			Status: domain.ToolOK, Output: rawOutput,
		}},
	})
	if len(dto.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", dto.ToolCalls)
	}
	call := dto.ToolCalls[0]
	if call.Output != rawOutput {
		t.Fatalf("raw output changed: got %q, want %q", call.Output, rawOutput)
	}
	if call.Presentation == nil {
		t.Fatal("frontend presentation is missing")
	}
	if call.Presentation.Variant != "file-list" || call.Presentation.Action != "Files listed" {
		t.Fatalf("presentation header = %+v", call.Presentation)
	}
	if call.Presentation.Request != "file_list({\n  \"path\": \"/workspace\"\n})" {
		t.Errorf("presentation request = %q", call.Presentation.Request)
	}
	if call.Presentation.Result.Summary != "2 entries · 12K" || call.Presentation.Result.Format != "list" {
		t.Errorf("presentation result = %+v", call.Presentation.Result)
	}
	if len(call.Presentation.Result.Items) != 2 {
		t.Fatalf("presentation items = %+v", call.Presentation.Result.Items)
	}
	if got := call.Presentation.Result.Items[1]["name"]; got != "file with spaces-b" {
		t.Errorf("file-list item name = %v", got)
	}
}
