package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"nusashell/contracts"
	"nusashell/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- from conversations_test.go ---

var errNotFound = errors.New("not found")

// recordingAcpStorage captures Save calls so persistAcpRun can be asserted
// without dragging in the JSON store. It implements domain.AcpRunStorage.
type recordingAcpStorage struct {
	saved []domain.AcpRunRecord
}

func (r *recordingAcpStorage) Save(rec domain.AcpRunRecord) error {
	r.saved = append(r.saved, rec)
	return nil
}
func (r *recordingAcpStorage) Load(runID string) (domain.AcpRunRecord, bool) {
	return domain.AcpRunRecord{}, false
}
func (r *recordingAcpStorage) List(conversationID string) []domain.AcpRunRecord {
	return nil
}
func (r *recordingAcpStorage) Path(conversationID, runID string) string {
	return "/tmp/recording-acp/" + conversationID + "/" + runID + ".json"
}

// fakeLogStore is a no-op LogStore for testing.
type fakeLogStore struct{}

func (f *fakeLogStore) Append(e *domain.LogEntry) {}
func (f *fakeLogStore) List(level string, limit int) []*domain.LogEntry {
	return nil
}
func (f *fakeLogStore) Clear() {}

// fakeConvStore is a minimal in-memory ConversationStore for testing.
type fakeConvStore struct {
	convs      map[string]*domain.Conversation
	archived   []domain.Message
	archiveErr error
	saveErr    error
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
	if f.archiveErr != nil {
		return 0, f.archiveErr
	}
	f.archived = append(f.archived, messages...)
	return 0, nil
}

func (f *fakeConvStore) GetChunk(id string, index int) ([]domain.Message, error) {
	return nil, errNotFound
}

func writeInstructionFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func instructionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeInstructionFile(t, filepath.Join(dir, "AGENTS.md"), "# root\n")
	writeInstructionFile(t, filepath.Join(dir, "application", "AGENTS.md"), "# app\n")
	writeInstructionFile(t, filepath.Join(dir, "frontend", "AGENTS.md"), "# ui\n")
	writeInstructionFile(t, filepath.Join(dir, "node_modules", "pkg", "AGENTS.md"), "# noisy npm\n")
	writeInstructionFile(t, filepath.Join(dir, "vendor", "lib", "AGENTS.md"), "# noisy vendor\n")
	writeInstructionFile(t, filepath.Join(dir, ".experimental", "fork", "AGENTS.md"), "# noisy experimental\n")
	writeInstructionFile(t, filepath.Join(dir, ".gitignore"), "node_modules/\nvendor/\n.experimental/\n")
	return dir
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

// cascadeFakeAcp records Stop calls and returns a fixed set of live runs
// for List(convID). Only List and Stop are exercised by the cascade tests;
// the remaining AcpRuntime methods panic so an accidental dependency
// surfaces immediately.
type cascadeFakeAcp struct {
	liveRuns map[string][]*domain.AcpRun
	stops    []string
}

func (f *cascadeFakeAcp) List(conversationID string) []*domain.AcpRun {
	return f.liveRuns[conversationID]
}

func (f *cascadeFakeAcp) Stop(runID string) error {
	f.stops = append(f.stops, runID)
	return nil
}

func (f *cascadeFakeAcp) panicOther() {}

func (f *cascadeFakeAcp) Probe(context.Context, *domain.AcpAgent) (domain.AcpAgent, error) {
	f.panicOther()
	return domain.AcpAgent{}, nil
}
func (f *cascadeFakeAcp) Authenticate(context.Context, *domain.AcpAgent, string) error {
	f.panicOther()
	return nil
}
func (f *cascadeFakeAcp) RefreshCatalog(context.Context, *domain.AcpAgent) (domain.AcpAgent, error) {
	f.panicOther()
	return domain.AcpAgent{}, nil
}
func (f *cascadeFakeAcp) Spawn(context.Context, AcpSpawnRequest) (*domain.AcpRun, error) {
	f.panicOther()
	return nil, nil
}
func (f *cascadeFakeAcp) Steer(string, string) error { f.panicOther(); return nil }
func (f *cascadeFakeAcp) Wait(context.Context, string) (*domain.AcpRun, error) {
	f.panicOther()
	return nil, nil
}
func (f *cascadeFakeAcp) Get(string) (*domain.AcpRun, bool) { f.panicOther(); return nil, false }
func (f *cascadeFakeAcp) DecidePermission(string, string, string, domain.PermissionOutcome) error {
	f.panicOther()
	return nil
}
func (f *cascadeFakeAcp) PromoteRisk(string, domain.RiskTier) error { f.panicOther(); return nil }
func (f *cascadeFakeAcp) SetMode(context.Context, string, string) error {
	f.panicOther()
	return nil
}
func (f *cascadeFakeAcp) Close() {}

// recordingAttachmentStore wraps a memAttachmentStore and captures Remove
// calls so the cascade test can assert the conversation id is forwarded.
type recordingAttachmentStore struct {
	*memAttachmentStore
	removed []string
}

func (r *recordingAttachmentStore) Remove(conversationID string) error {
	r.removed = append(r.removed, conversationID)
	return r.memAttachmentStore.Remove(conversationID)
}

func TestHandleConversationsDeleteCascadesSidecars(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
		"conv_2": {ID: "conv_2", Title: "Sibling"},
	}}
	attach := &recordingAttachmentStore{memAttachmentStore: &memAttachmentStore{root: t.TempDir()}}
	acp := &cascadeFakeAcp{liveRuns: map[string][]*domain.AcpRun{
		"conv_1": {{TaskState: domain.TaskState[domain.AcpRunStatus]{ID: "run_a", Status: domain.AcpRunRunning}}},
		"conv_2": {{TaskState: domain.TaskState[domain.AcpRunStatus]{ID: "run_b", Status: domain.AcpRunRunning}}},
	}}
	app := &App{
		Conversations: convStore,
		Todos:         &fakeTodoPort{items: map[string][]domain.TodoItem{"conv_1": {{ID: "1", Content: "Task", Status: domain.TodoPending}}}},
		Attachments:   attach,
		Acp:           acp,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
	}

	if _, rpcErr := app.handleConversationsDelete(contracts.ConversationIDRequest{ID: "conv_1"}); rpcErr != nil {
		t.Fatalf("delete: %v", rpcErr)
	}
	if _, err := convStore.Get("conv_1"); err == nil {
		t.Error("expected conv_1 removed from store")
	}
	if attach.removed == nil || attach.removed[0] != "conv_1" {
		t.Errorf("Attachments.Remove not called for conv_1: %+v", attach.removed)
	}
	if len(acp.stops) != 1 || acp.stops[0] != "run_a" {
		t.Errorf("Acp.Stop must only target conv_1's run: got %+v", acp.stops)
	}
}

func TestHandleConversationsDeleteNilPortsStillSucceeds(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}
	app := &App{Conversations: convStore, Logs: &fakeLogStore{}, Bus: NewBus()}
	if _, rpcErr := app.handleConversationsDelete(contracts.ConversationIDRequest{ID: "conv_1"}); rpcErr != nil {
		t.Fatalf("delete: %v", rpcErr)
	}
	if _, err := convStore.Get("conv_1"); err == nil {
		t.Error("expected conv_1 removed from store")
	}
}

func TestPersistAcpRunSkipsWhenConversationGone(t *testing.T) {
	// Conversation is deleted before persistAcpRun fires. The store must
	// not be written to: a late OnDone must not recreate the
	// conversations/<id>.acp/ sidecar or leave a stale JSON document.
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{}}
	storage := &recordingAcpStorage{}
	app := &App{
		Conversations: convStore,
		AcpRunStorage: storage,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
	}
	run := &domain.AcpRun{
		TaskState:      domain.TaskState[domain.AcpRunStatus]{ID: "run_x", Status: domain.AcpRunCompleted},
		ConversationID: "conv_gone",
	}
	if path := app.persistAcpRun(run); path != "" {
		t.Errorf("persistAcpRun after delete must return empty path, got %q", path)
	}
	if len(storage.saved) != 0 {
		t.Errorf("AcpRunStorage.Save must be skipped, got %+v", storage.saved)
	}
}

func TestPersistAcpRunStillSavesWhenConversationExists(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_live": {ID: "conv_live", Title: "live"},
	}}
	storage := &recordingAcpStorage{}
	app := &App{
		Conversations: convStore,
		AcpRunStorage: storage,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
	}
	run := &domain.AcpRun{
		TaskState:      domain.TaskState[domain.AcpRunStatus]{ID: "run_y", Status: domain.AcpRunCompleted},
		ConversationID: "conv_live",
	}
	if path := app.persistAcpRun(run); path == "" {
		t.Errorf("persistAcpRun must produce a path when conversation exists, got empty")
	}
	if len(storage.saved) != 1 {
		t.Errorf("AcpRunStorage.Save expected once, got %+v", storage.saved)
	}
}

func TestHandleConversationsSetWorkspaceRejectsRelativePath(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}
	app := &App{
		Conversations:    convStore,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		DirectoryBrowser: fakeDirBrowser{},
	}

	_, rpcErr := app.handleConversationsSetWorkspace(contracts.ConversationSetWorkspaceRequest{
		ID:   "conv_1",
		Path: filepath.Join("rel", "workspace"),
	})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR for a relative workspace, got %+v", rpcErr)
	}
}

func TestHandleConversationsSetProviderPersistsPerConversation(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"codex-room": {ID: "codex-room", Title: "Codex", ProviderRoute: "old"},
		"other-room": {ID: "other-room", Title: "Other", ProviderRoute: "keep"},
	}}
	app := &App{Conversations: convStore, Logs: &fakeLogStore{}, Bus: NewBus()}

	resp, rpcErr := app.handleConversationsSetProvider(contracts.ConversationSetProviderRequest{
		ID: "codex-room", ProviderRoute: "account-plus",
	})
	if rpcErr != nil {
		t.Fatalf("set provider: %v", rpcErr)
	}
	got, ok := resp.(contracts.ConversationGetResult)
	if !ok || got.Conversation.ProviderRoute != "account-plus" {
		t.Fatalf("response = %+v, want account-plus", resp)
	}
	saved, err := convStore.Get("codex-room")
	if err != nil || saved.ProviderRoute != "account-plus" {
		t.Fatalf("saved route = %q, err=%v", saved.ProviderRoute, err)
	}
	other, err := convStore.Get("other-room")
	if err != nil || other.ProviderRoute != "keep" {
		t.Fatalf("other room route leaked: %q, err=%v", other.ProviderRoute, err)
	}

	if _, rpcErr := app.handleConversationsSetProvider(contracts.ConversationSetProviderRequest{ID: "codex-room"}); rpcErr != nil {
		t.Fatalf("clear provider: %v", rpcErr)
	}
	saved, _ = convStore.Get("codex-room")
	if saved.ProviderRoute != "" {
		t.Fatalf("cleared route = %q", saved.ProviderRoute)
	}
}

func TestHandleConversationsSetWorkspaceAcceptsAbsolutePath(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}
	workspace := t.TempDir()
	app := &App{
		Conversations:    convStore,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		DirectoryBrowser: fakeDirBrowser{},
	}

	resp, rpcErr := app.handleConversationsSetWorkspace(contracts.ConversationSetWorkspaceRequest{
		ID:   "conv_1",
		Path: workspace,
	})
	if rpcErr != nil {
		t.Fatalf("absolute workspace rejected: %v", rpcErr)
	}
	got, ok := resp.(contracts.ConversationGetResult)
	if !ok || got.Conversation.Workspace != workspace {
		t.Fatalf("workspace = %+v, want %q", resp, workspace)
	}
}

// TestHandleConversationsSetWorkspaceSerializesTurnSave reproduces the
// workspace-set/turn-completion race. The real JSON store returns clones;
// if the set handler saves a stale pre-validation clone after a turn saves
// the latest message, that completed turn disappears from the conversation
// file. EnsureDir stands in for the slow pre-lock I/O phase.
func TestHandleConversationsSetWorkspaceSerializesTurnSave(t *testing.T) {
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
	ensureStarted := make(chan struct{})
	releaseEnsure := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseEnsure)
		}
	}()
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		DirectoryBrowser: fakeDirBrowser{ensure: func(context.Context, string) error {
			close(ensureStarted)
			<-releaseEnsure
			return nil
		}},
	}

	setDone := make(chan *contracts.RPCError, 1)
	go func() {
		_, rpcErr := app.handleConversationsSetWorkspace(contracts.ConversationSetWorkspaceRequest{
			ID:   "conv_1",
			Path: workspace,
		})
		setDone <- rpcErr
	}()
	<-ensureStarted

	// Model the final persistence step of a concurrent turn. It is allowed to
	// complete while the path is validated; the handler must re-read the
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
		t.Fatal("turn save did not complete while the workspace set handler was pre-lock")
	}

	close(releaseEnsure)
	released = true
	if rpcErr := <-setDone; rpcErr != nil {
		t.Fatalf("set workspace: %v", rpcErr)
	}
	select {
	case <-turnSaveDone:
	case <-time.After(time.Second):
		t.Fatal("turn save did not complete after the ensure released")
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

// TestHandleConversationsSetWorkspaceEmptyRoomDoesNotInsertHydration pins
// that setting a workspace before the first user must not persist a
// checkpoint at index 0. The first turn's addTurnMessages parks it after
// the user so OpenAI/Claude see system → user → hydration.
func TestHandleConversationsSetWorkspaceEmptyRoomDoesNotInsertHydration(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Test"},
	}}
	workspace := t.TempDir()
	app := &App{
		Conversations:    convStore,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		Toolbox:          &recordingToolbox{},
		DirectoryBrowser: fakeDirBrowser{},
	}
	if _, rpcErr := app.handleConversationsSetWorkspace(contracts.ConversationSetWorkspaceRequest{
		ID:   "conv_1",
		Path: workspace,
	}); rpcErr != nil {
		t.Fatalf("set workspace: %v", rpcErr)
	}
	saved := convStore.convs["conv_1"]
	for _, m := range saved.Messages {
		if domain.IsHydrationMessage(m) {
			t.Fatalf("empty room persisted hydration before any user: %+v", m)
		}
	}
}

// TestHandleConversationsSetWorkspaceKeepsFormedHydration pins that a
// mid-conversation workspace change must not strip or rebuild formed
// hydration. The visible workspace_changed notice is queued for the next
// user turn.
func TestHandleConversationsSetWorkspaceKeepsFormedHydration(t *testing.T) {
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
		Conversations:    convStore,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		DirectoryBrowser: fakeDirBrowser{},
	}
	if _, rpcErr := app.handleConversationsSetWorkspace(contracts.ConversationSetWorkspaceRequest{
		ID:   "conv_1",
		Path: newWS,
	}); rpcErr != nil {
		t.Fatalf("set workspace: %v", rpcErr)
	}
	saved := convStore.convs["conv_1"]
	if saved.Workspace != newWS {
		t.Fatalf("workspace = %q, want %q", saved.Workspace, newWS)
	}
	if !saved.PendingWorkspaceAnnouncement {
		t.Fatal("mid-conversation workspace change must queue a visible notice; formed hydration stays")
	}
	foundOld := false
	for _, m := range saved.Messages {
		if m.ID == "m2" {
			foundOld = true
		}
		if m.ID != "m1" && m.ID != "m2" && m.ID != "m3" {
			t.Fatalf("set must not splice messages, got extra %+v", m)
		}
	}
	if !foundOld {
		t.Fatal("formed hydration message must remain (append-only)")
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

// --- from conversation_messaging_test.go ---

func TestConversationMessagingListAndSearch(t *testing.T) {
	now := time.Now()
	c1 := &domain.Conversation{ID: "conv_1", Title: "Backend Project", Summary: "Refactoring auth middleware", UpdatedAt: now.Add(-10 * time.Minute)}
	c2 := &domain.Conversation{ID: "conv_2", Title: "Frontend UI", Summary: "Building user profile page", UpdatedAt: now.Add(-5 * time.Minute)}
	c3 := &domain.Conversation{ID: "conv_3", Title: "Database Migration", Summary: "Postgres schema updates", UpdatedAt: now}
	hidden := &domain.Conversation{ID: "conv_pipe", Title: "[pipeline] step", Origin: domain.ConversationOriginPipeline, UpdatedAt: now}

	store := &fakeConvStore{
		convs: map[string]*domain.Conversation{
			"conv_1":    c1,
			"conv_2":    c2,
			"conv_3":    c3,
			"conv_pipe": hidden,
		},
	}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	// 1. List from conv_3 (self excluded, hidden excluded, sorted newest first)
	total, items, err := app.List("conv_3", 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].ID != "conv_2" || items[1].ID != "conv_1" {
		t.Fatalf("items order = [%s, %s], want [conv_2, conv_1] (newest first)", items[0].ID, items[1].ID)
	}

	// 2. List with pagination (limit 1, offset 0)
	total, items, err = app.List("conv_3", 1, 0)
	if err != nil {
		t.Fatalf("List pagination: %v", err)
	}
	if total != 2 || len(items) != 1 || items[0].ID != "conv_2" {
		t.Fatalf("page 1 items = %+v, want conv_2", items)
	}

	// 3. List with pagination (limit 1, offset 1)
	total, items, err = app.List("conv_3", 1, 1)
	if err != nil {
		t.Fatalf("List pagination offset: %v", err)
	}
	if total != 2 || len(items) != 1 || items[0].ID != "conv_1" {
		t.Fatalf("page 2 items = %+v, want conv_1", items)
	}

	// 4. Search by title
	total, items, err = app.Search("conv_3", "backend", 10, 0)
	if err != nil {
		t.Fatalf("Search title: %v", err)
	}
	if total != 1 || items[0].ID != "conv_1" {
		t.Fatalf("search 'backend' = %+v, want conv_1", items)
	}

	// 5. Search by summary
	total, items, err = app.Search("conv_3", "profile", 10, 0)
	if err != nil {
		t.Fatalf("Search summary: %v", err)
	}
	if total != 1 || items[0].ID != "conv_2" {
		t.Fatalf("search 'profile' = %+v, want conv_2", items)
	}
}

func TestConversationMessagingSend(t *testing.T) {
	c1 := &domain.Conversation{ID: "conv_1", Title: "Sender"}
	c2 := &domain.Conversation{ID: "conv_2", Title: "Receiver"}
	hidden := &domain.Conversation{ID: "conv_pipe", Title: "[pipeline] step", Origin: domain.ConversationOriginPipeline}

	store := &fakeConvStore{
		convs: map[string]*domain.Conversation{
			"conv_1":    c1,
			"conv_2":    c2,
			"conv_pipe": hidden,
		},
	}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	// 1. Send valid message from conv_1 to conv_2
	err := app.Send("conv_1", "conv_2", "Hey bro, whutsapp?")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	gotC2, _ := store.Get("conv_2")
	if len(gotC2.PendingAnnouncements) != 1 {
		t.Fatalf("conv_2 pending announcements = %d, want 1", len(gotC2.PendingAnnouncements))
	}
	ann := gotC2.PendingAnnouncements[0]
	if ann.Type != "peer_message" {
		t.Fatalf("announcement type = %q, want peer_message", ann.Type)
	}
	wantMsg := "You received message from conversation `conv_1`, use `conversation(op=\"send\", id=\"conv_1\", content=\"...\")` to reply:\n> Hey bro, whutsapp?"
	if ann.Message != wantMsg {
		t.Fatalf("announcement message =\n%q\nwant:\n%q", ann.Message, wantMsg)
	}

	// 2. Reject send to self
	if err := app.Send("conv_1", "conv_1", "hi"); err == nil {
		t.Fatal("expected error when sending to self")
	}

	// 3. Reject send to non-existent
	if err := app.Send("conv_1", "conv_999", "hi"); err == nil {
		t.Fatal("expected error when target does not exist")
	}

	// 4. Reject send to hidden room
	if err := app.Send("conv_1", "conv_pipe", "hi"); err == nil {
		t.Fatal("expected error when target is hidden")
	}
}

// --- from workspace_test.go ---

func TestEffectiveWorkspaceFallsBackToDefault(t *testing.T) {
	app := &App{defaultWorkspace: "/home/tuan"}
	if got := app.effectiveWorkspace(""); got != "/home/tuan" {
		t.Fatalf("empty workspace default = %q, want /home/tuan", got)
	}
	if got := app.effectiveWorkspace("   "); got != "/home/tuan" {
		t.Fatalf("blank workspace default = %q, want /home/tuan", got)
	}
	if got := app.effectiveWorkspace("/projects/x"); got != "/projects/x" {
		t.Fatalf("explicit workspace = %q, want /projects/x", got)
	}
	if app := (&App{}); app.effectiveWorkspace("") != "" {
		t.Fatal("without a configured default the effective workspace must stay empty")
	}
}

func TestHandleToolContractsAdvertisesMemoryProjectWithDefaultWorkspace(t *testing.T) {
	box := &factoryStubToolbox{tools: []ToolInfo{
		{Name: "memory_project", InputSchema: map[string]any{"type": "object"}},
	}}
	a := &App{Toolbox: box, defaultWorkspace: "/home/tuan"}

	result, rpcErr := a.handleToolContracts(contracts.ToolContractsRequest{Workspace: ""})
	if rpcErr != nil {
		t.Fatalf("handleToolContracts: %v", rpcErr)
	}
	got := result.(contracts.ToolContractsResult)
	if _, ok := findToolContract(got.Tools, "memory_project"); !ok {
		t.Fatal("memory_project must be advertised when the default workspace is active")
	}
}

// fakeDirBrowser is the in-memory DirectoryBrowser used by workspace tests.
// Both methods default to success so notice/validation tests only override
// what they care about.
type fakeDirBrowser struct {
	list   func(ctx context.Context, path string) (DirListing, error)
	ensure func(ctx context.Context, path string) error
}

func (f fakeDirBrowser) ListDirs(ctx context.Context, path string) (DirListing, error) {
	if f.list != nil {
		return f.list(ctx, path)
	}
	return DirListing{Entries: []contracts.WorkspaceDirEntry{}}, nil
}

func (f fakeDirBrowser) EnsureDir(ctx context.Context, path string) error {
	if f.ensure != nil {
		return f.ensure(ctx, path)
	}
	return nil
}

func TestHandleWorkspaceListDirsReturnsResolvedListing(t *testing.T) {
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		DirectoryBrowser: fakeDirBrowser{list: func(_ context.Context, path string) (DirListing, error) {
			return DirListing{
				Path:   "/home/tuan/projects",
				Parent: "/home/tuan",
				Entries: []contracts.WorkspaceDirEntry{
					{Name: "alpha", Path: "/home/tuan/projects/alpha"},
					{Name: "beta", Path: "/home/tuan/projects/beta"},
				},
				Truncated: false,
			}, nil
		}},
	}

	resp, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: ""})
	if rpcErr != nil {
		t.Fatalf("list dirs: %v", rpcErr)
	}
	got, ok := resp.(contracts.WorkspaceListDirsResult)
	if !ok {
		t.Fatalf("resp = %T, want WorkspaceListDirsResult", resp)
	}
	if got.Path != "/home/tuan/projects" || got.Parent != "/home/tuan" {
		t.Fatalf("path/parent = %q/%q", got.Path, got.Parent)
	}
	if len(got.Entries) != 2 || got.Entries[0].Name != "alpha" {
		t.Fatalf("entries = %+v", got.Entries)
	}
	if got.Truncated {
		t.Fatal("truncated must be false")
	}
}

func TestHandleWorkspaceListDirsRejectsRelativePath(t *testing.T) {
	app := &App{Logs: &fakeLogStore{}, Bus: NewBus(), DirectoryBrowser: fakeDirBrowser{}}

	_, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: "relative/dir"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR for relative path, got %+v", rpcErr)
	}
}

func TestHandleWorkspaceListDirsMapsNotExistToValidation(t *testing.T) {
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		DirectoryBrowser: fakeDirBrowser{list: func(context.Context, string) (DirListing, error) {
			return DirListing{}, fs.ErrNotExist
		}},
	}

	_, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: "/gone"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR for missing dir, got %+v", rpcErr)
	}
}

func TestHandleWorkspaceListDirsMapsPermissionToValidation(t *testing.T) {
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		DirectoryBrowser: fakeDirBrowser{list: func(context.Context, string) (DirListing, error) {
			return DirListing{}, fs.ErrPermission
		}},
	}

	_, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: "/root"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR for unreadable dir, got %+v", rpcErr)
	}
}

func TestHandleWorkspaceListDirsNilBrowserUnavailable(t *testing.T) {
	app := &App{Logs: &fakeLogStore{}, Bus: NewBus()}

	_, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: "/x"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR when browser unavailable, got %+v", rpcErr)
	}
}

func TestHandleWorkspaceListDirsCoercesNilEntriesToEmpty(t *testing.T) {
	path := t.TempDir()
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		DirectoryBrowser: fakeDirBrowser{list: func(context.Context, string) (DirListing, error) {
			return DirListing{Path: path}, nil
		}},
	}

	resp, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: path})
	if rpcErr != nil {
		t.Fatalf("list dirs: %v", rpcErr)
	}
	got := resp.(contracts.WorkspaceListDirsResult)
	if got.Entries == nil {
		t.Fatal("entries must marshal as [] not null")
	}
}

// --- from workspace_switch_test.go ---

// agentsMDToolbox returns a canned AGENTS.md body for file_read, and empty
// output for every other tool so hydration slots stay hidden in these tests.
type agentsMDToolbox struct {
	body string
	err  error
}

func (t *agentsMDToolbox) ListTools() []ToolInfo { return nil }

func (t *agentsMDToolbox) Execute(_ context.Context, name string, argsJSON []byte) (string, error) {
	if name != "file_read" {
		return "", nil
	}
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(argsJSON, &args)
	if !strings.HasSuffix(args.Path, "AGENTS.md") {
		return "", fmt.Errorf("missing: %s", args.Path)
	}
	if t.err != nil {
		return "", t.err
	}
	return t.body, nil
}

func workspaceSwitchFixture(oldWS string) *domain.Conversation {
	return &domain.Conversation{
		ID:        "conv_1",
		Title:     "Test",
		Workspace: oldWS,
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "hello", Status: domain.StatusDone},
			{ID: "m2", Role: domain.RoleAssistant, Content: "option C", Status: domain.StatusDone},
		},
	}
}

func setWorkspace(t *testing.T, app *App, id, path string) *domain.Conversation {
	t.Helper()
	if _, rpcErr := app.handleConversationsSetWorkspace(contracts.ConversationSetWorkspaceRequest{ID: id, Path: path}); rpcErr != nil {
		t.Fatalf("set workspace: %v", rpcErr)
	}
	saved, err := app.Conversations.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestHandleConversationsPickWorkspaceQueuesNoticeWithoutInserting(t *testing.T) {
	oldWS := "/media/jahrulnr/storage/workspace/NusaShell-mcp"
	conv := workspaceSwitchFixture(oldWS)
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	newWS := t.TempDir()
	app := &App{
		Conversations:    store,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		Toolbox:          &agentsMDToolbox{body: "---\nbytes: 12\n---\n\n# Rules\n"},
		DirectoryBrowser: fakeDirBrowser{},
	}

	saved := setWorkspace(t, app, "conv_1", newWS)
	if saved.Workspace != newWS {
		t.Fatalf("workspace = %q, want %q", saved.Workspace, newWS)
	}
	if !saved.PendingWorkspaceAnnouncement {
		t.Fatal("pick mid-conversation must queue a workspace-switch notice for the next user turn")
	}
	if saved.WorkspaceSwitchFrom != oldWS {
		t.Fatalf("WorkspaceSwitchFrom = %q, want %q", saved.WorkspaceSwitchFrom, oldWS)
	}
	for _, m := range saved.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				t.Fatalf("notice must wait for the next user message, got %+v", tc)
			}
			if tc.Name == "file_read" && !domain.IsHydrationCallID(tc.ID) {
				t.Fatalf("visible AGENTS.md file_read must wait for the next user message, got %+v", tc)
			}
		}
	}
}

func TestHandleConversationsPickWorkspaceEmptyRoomDoesNotQueueNotice(t *testing.T) {
	store := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Empty"},
	}}
	app := &App{
		Conversations:    store,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		DirectoryBrowser: fakeDirBrowser{},
	}
	saved := setWorkspace(t, app, "conv_1", t.TempDir())
	if saved.PendingWorkspaceAnnouncement {
		t.Fatal("empty room must not queue a workspace-switch notice")
	}
}

func TestHandleConversationsPickWorkspaceSamePathDoesNotQueueNotice(t *testing.T) {
	ws := t.TempDir()
	conv := workspaceSwitchFixture(ws)
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	app := &App{
		Conversations:    store,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		DirectoryBrowser: fakeDirBrowser{},
	}
	saved := setWorkspace(t, app, "conv_1", ws)
	if saved.PendingWorkspaceAnnouncement {
		t.Fatal("re-picking the same workspace must not queue a notice")
	}
}

// TestAddTurnMessagesInjectsWorkspaceSwitchNotice pins the user-reported
// flow: assistant → pick workspace → user sends → visible announcement +
// AGENTS.md file_read sit immediately after that user, then the assistant
// turn. Same injection point as restart announcements.
func TestAddTurnMessagesInjectsWorkspaceSwitchNotice(t *testing.T) {
	oldWS := "/media/jahrulnr/storage/workspace/NusaShell-mcp"
	newWS := t.TempDir()
	agentsPath := filepath.Join(newWS, "AGENTS.md")
	agentsBody := "---\nbytes: 18\n---\n\n# Host rules\nUse Go.\n"
	conv := workspaceSwitchFixture(oldWS)
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	app := &App{
		Conversations:    store,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		Toolbox:          &agentsMDToolbox{body: agentsBody},
		DirectoryBrowser: fakeDirBrowser{},
	}
	saved := setWorkspace(t, app, "conv_1", newWS)

	app.addTurnMessages(saved,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "Sip, gas implement C", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)

	if saved.PendingWorkspaceAnnouncement {
		t.Fatal("pending flag must be one-shot")
	}
	if saved.WorkspaceSwitchFrom != "" {
		t.Fatalf("WorkspaceSwitchFrom must clear after inject, got %q", saved.WorkspaceSwitchFrom)
	}

	// fixture 2 + user + notice + assistant
	if len(saved.Messages) < 4 {
		t.Fatalf("messages = %d, want at least 4", len(saved.Messages))
	}
	userIdx := -1
	for i, m := range saved.Messages {
		if m.ID == "m_user" {
			userIdx = i
			break
		}
	}
	if userIdx < 0 {
		t.Fatal("user message missing")
	}
	if saved.Messages[userIdx].Content != "Sip, gas implement C" {
		t.Fatalf("user content = %q", saved.Messages[userIdx].Content)
	}
	notice := saved.Messages[userIdx+1]
	if domain.IsHydrationMessage(notice) {
		t.Fatal("workspace-switch notice must be visible (not a hidden hydration checkpoint)")
	}
	if notice.Role != domain.RoleAssistant {
		t.Fatalf("notice role = %s, want assistant", notice.Role)
	}
	if dto := msgDTO(notice); dto.ID == "" || len(dto.ToolCalls) == 0 {
		t.Fatal("notice must survive msgDTO so the UI and JSON dump show it")
	}
	if saved.Messages[userIdx+2].ID != "m_asst" {
		t.Fatalf("assistant turn must follow the notice, got %+v", saved.Messages[userIdx+2])
	}

	var ann, agents *domain.ToolCall
	for i := range notice.ToolCalls {
		tc := &notice.ToolCalls[i]
		switch {
		case tc.Name == domain.AnnouncementToolName:
			ann = tc
		case tc.Name == "file_read":
			agents = tc
		}
	}
	if ann == nil {
		t.Fatal("missing announcement tool")
	}
	if !domain.IsAnnouncementCallID(ann.ID) {
		t.Fatalf("announcement id %q must use announce- prefix", ann.ID)
	}
	if ann.Status != domain.ToolOK {
		t.Fatalf("announcement status = %s", ann.Status)
	}
	var payload struct {
		Type string `json:"type"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal([]byte(ann.Args), &payload); err != nil {
		t.Fatalf("announcement args must be JSON: %v (args=%s)", err, ann.Args)
	}
	// Compare the parsed fields, not raw substrings: args is JSON-escaped,
	// so a Windows path (backslashes) in the raw text differs from the
	// plain path value (e.g. C:\\Users vs C:\Users).
	if payload.Type != "workspace_changed" || payload.From != oldWS || payload.To != newWS {
		t.Fatalf("announcement args = type %q from %q to %q, want type=workspace_changed from=%q to=%q", payload.Type, payload.From, payload.To, oldWS, newWS)
	}
	if !strings.Contains(ann.Output, oldWS) || !strings.Contains(ann.Output, newWS) {
		t.Fatalf("announcement output must name both paths: %q", ann.Output)
	}
	if agents == nil {
		t.Fatal("missing AGENTS.md file_read")
	}
	if domain.IsHydrationCallID(agents.ID) {
		t.Fatal("AGENTS.md file_read must not use the hidden hydrate- prefix")
	}
	var fa struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(agents.Args), &fa); err != nil || fa.Path != agentsPath {
		t.Fatalf("file_read args = %s, want path %q", agents.Args, agentsPath)
	}
	if agents.Output != agentsBody {
		t.Fatalf("file_read output = %q, want real AGENTS.md body", agents.Output)
	}

	app.addTurnMessages(saved,
		domain.Message{ID: "m_user2", Role: domain.RoleUser, Content: "lanjut", Status: domain.StatusDone},
		domain.Message{ID: "m_asst2", Role: domain.RoleAssistant},
	)
	for _, m := range saved.Messages[userIdx+3:] {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				var parsed struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal([]byte(tc.Args), &parsed)
				if parsed.Type == "workspace_changed" {
					t.Fatal("second turn must not repeat the workspace-switch notice")
				}
			}
		}
	}
}

func TestAddTurnMessagesWorkspaceSwitchOmitsMissingAgentsMD(t *testing.T) {
	conv := workspaceSwitchFixture("/old/ws")
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	ws := t.TempDir()
	app := &App{
		Conversations:    store,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		Toolbox:          &agentsMDToolbox{err: fmt.Errorf("open AGENTS.md: no such file")},
		DirectoryBrowser: fakeDirBrowser{},
	}
	saved := setWorkspace(t, app, "conv_1", ws)
	app.addTurnMessages(saved,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "go", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	var notice domain.Message
	for _, m := range saved.Messages {
		if m.ID == "m_user" {
			continue
		}
		if m.ID == "m_asst" {
			break
		}
		if len(m.ToolCalls) > 0 {
			notice = m
		}
	}
	if len(notice.ToolCalls) != 1 || notice.ToolCalls[0].Name != domain.AnnouncementToolName {
		t.Fatalf("missing AGENTS.md must leave announcement only, got %+v", notice.ToolCalls)
	}
}

func TestWorkspaceSwitchNoticeIncludesInstructionFiles(t *testing.T) {
	newWS := instructionFixture(t)
	conv := workspaceSwitchFixture("/old/ws")
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	app := &App{
		Conversations:    store,
		Logs:             &fakeLogStore{},
		Bus:              NewBus(),
		Toolbox:          &agentsMDToolbox{body: "---\nbytes: 8\n---\n\n# root\n"},
		DirectoryBrowser: fakeDirBrowser{},
	}
	saved := setWorkspace(t, app, "conv_1", newWS)
	app.addTurnMessages(saved,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "go", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	var args string
	for _, m := range saved.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				args = tc.Args
			}
		}
	}
	if args == "" {
		t.Fatal("missing workspace_changed announcement")
	}
	var payload struct {
		Type             string   `json:"type"`
		To               string   `json:"to"`
		InstructionFiles []string `json:"instruction_files"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		t.Fatalf("announcement args: %v (%s)", err, args)
	}
	if payload.Type != "workspace_changed" || payload.To != newWS {
		t.Fatalf("announcement args = %+v", payload)
	}
	joined := strings.Join(payload.InstructionFiles, ",")
	for _, want := range []string{"AGENTS.md", "application/AGENTS.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("instruction_files missing %s: %v", want, payload.InstructionFiles)
		}
	}
	for _, p := range payload.InstructionFiles {
		if strings.Contains(p, "node_modules") || strings.Contains(p, "vendor") || strings.Contains(p, ".experimental") {
			t.Fatalf("instruction_files leaked ignored path %q", p)
		}
	}
}
