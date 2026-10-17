package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/application/provider"
	"nusashell/contracts"
	"nusashell/domain"
)

type compactionEventBus struct {
	mu     sync.Mutex
	events []string
}

func (b *compactionEventBus) Emit(typ string, _ any) {
	b.mu.Lock()
	b.events = append(b.events, typ)
	b.mu.Unlock()
}

func (b *compactionEventBus) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.events))
	copy(out, b.events)
	return out
}

func manualCompactProvider() *domain.Provider {
	return &domain.Provider{
		ID:      "prov",
		Kind:    domain.ProviderChat,
		Enabled: true,
		Models:  []domain.Model{{ID: "model", Context: 4000}},
	}
}

func manualCompactMessages(n int) []domain.Message {
	body := strings.Repeat("abcdefghij", 40)
	msgs := make([]domain.Message, 0, n)
	for i := 0; i < n; i++ {
		role := domain.RoleUser
		if i%2 == 1 {
			role = domain.RoleAssistant
		}
		msgs = append(msgs, domain.Message{
			ID:      fmt.Sprintf("m%d", i),
			Role:    role,
			Content: body,
			Status:  domain.StatusDone,
		})
	}
	return msgs
}

func TestHandleConversationsCompactSummarizesAndKeepsRecentTail(t *testing.T) {
	const convID = "conv-manual"
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{
		convID: {
			ID:       convID,
			Status:   "idle",
			Model:    "prov:model",
			Messages: manualCompactMessages(40),
		},
	}}
	adapter := &compactionChatAdapter{toolCallSummaries: []string{compactionTestSummary}}
	bus := &compactionEventBus{}
	providerMeta := manualCompactProvider()
	svc := New(Deps{
		Conversations: store,
		Settings:      compactionSettings{s: domain.DefaultSettings()},
		Bus:           bus,
		ResolveModel: func(model string) (*domain.Provider, string, string, *contracts.RPCError) {
			if model != "prov:model" {
				return nil, "", "", &contracts.RPCError{Code: contracts.CodeValidation, Message: "unexpected model"}
			}
			return providerMeta, "model", "key", nil
		},
		Factory: func(context.Context, *domain.Provider, string) (provider.AIProvider, error) {
			return adapter, nil
		},
		Go: func(_ string, fn func()) { fn() },
	})

	result, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: convID})
	if rpcErr != nil {
		t.Fatalf("HandleConversationsCompact: %s", rpcErr.Message)
	}
	got, ok := result.(contracts.ConversationCompactResult)
	if !ok || got.Status != contracts.CompactStatusStarted || got.ConversationID != convID {
		t.Fatalf("result = %#v", result)
	}
	if adapter.calls == 0 {
		t.Fatal("manual compaction did not call the summary model")
	}
	saved, err := store.Get(convID)
	if err != nil || saved == nil {
		t.Fatalf("saved conversation: %v", err)
	}
	if len(saved.Messages) >= 40 {
		t.Fatalf("message count = %d, want the older prefix archived", len(saved.Messages))
	}
	var found bool
	for _, msg := range saved.Messages {
		if strings.Contains(msg.Content, "handoff checkpoint") {
			found = true
		}
	}
	if !found {
		t.Fatal("retained transcript has no compaction summary")
	}
	events := bus.snapshot()
	if len(events) < 2 || events[0] != contracts.EventCompacting || events[1] != contracts.EventCompacted {
		t.Fatalf("events = %v", events)
	}
	if svc.manualCompactionActive(convID) {
		t.Fatal("compaction flag still set after the pass finished")
	}
}

func TestHandleConversationsCompactRefusesBusyAndEmptyRooms(t *testing.T) {
	const convID = "conv-manual-refuse"
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{
		convID:       {ID: convID, Status: "idle", Model: "prov:model", Messages: manualCompactMessages(4)},
		"conv-short": {ID: "conv-short", Status: "idle", Messages: manualCompactMessages(1)},
		"conv-server": {
			ID: "conv-server", Status: "idle", Model: "prov:gpt-5",
			Messages: manualCompactMessages(4),
		},
	}}
	adapter := &compactionChatAdapter{}
	var launched func()
	svc := New(Deps{
		Conversations: store,
		Settings:      compactionSettings{s: domain.DefaultSettings()},
		Bus:           noopEmitter{},
		ResolveModel: func(model string) (*domain.Provider, string, string, *contracts.RPCError) {
			bare := strings.TrimPrefix(model, "prov:")
			kind := domain.ProviderChat
			return &domain.Provider{ID: "prov", Kind: kind, Models: []domain.Model{{ID: bare, Context: 4000}}}, bare, "key", nil
		},
		Factory: func(context.Context, *domain.Provider, string) (provider.AIProvider, error) {
			return adapter, nil
		},
		Go: func(_ string, fn func()) { launched = fn },
	})

	if _, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{}); rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("missing id = %+v", rpcErr)
	}
	if _, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: "missing"}); rpcErr == nil || rpcErr.Code != contracts.CodeNotFound {
		t.Fatalf("missing conversation = %+v", rpcErr)
	}

	short, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: "conv-short"})
	if rpcErr != nil {
		t.Fatalf("short room: %s", rpcErr.Message)
	}
	if short.(contracts.ConversationCompactResult).Status != contracts.CompactStatusNothingToCompact {
		t.Fatalf("short room = %#v", short)
	}

	server, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: "conv-server"})
	if rpcErr != nil {
		t.Fatalf("server-managed: %s", rpcErr.Message)
	}
	if server.(contracts.ConversationCompactResult).Status != contracts.CompactStatusServerManaged {
		t.Fatalf("server-managed = %#v", server)
	}
	if adapter.calls != 0 {
		t.Fatalf("refused rooms called the model %d times", adapter.calls)
	}

	svc.runsMu.Lock()
	svc.runs["run-busy"] = &TurnRun{ID: "run-busy", ConversationID: convID, Ctx: context.Background()}
	svc.runsMu.Unlock()
	if _, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: convID}); rpcErr == nil || rpcErr.Code != contracts.CodeConflict {
		t.Fatalf("busy turn = %+v", rpcErr)
	}
	delete(svc.runs, "run-busy")

	first, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: convID})
	if rpcErr != nil {
		t.Fatalf("start: %s", rpcErr.Message)
	}
	if first.(contracts.ConversationCompactResult).Status != contracts.CompactStatusStarted {
		t.Fatalf("start = %#v", first)
	}
	if _, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: convID}); rpcErr == nil || rpcErr.Code != contracts.CodeConflict {
		t.Fatalf("second compact = %+v", rpcErr)
	}
	if _, rpcErr := svc.HandleTurnsStart(context.Background(), contracts.TurnStartRequest{
		ConversationID: convID,
		Text:           "hello",
		Model:          "prov:model",
	}); rpcErr == nil || rpcErr.Code != contracts.CodeConflict {
		t.Fatalf("turn during compact = %+v", rpcErr)
	}
	if launched != nil {
		launched()
	}
}

// deletingConvStore removes the room on Delete. Save still inserts whatever
// it is given, which is what jsonstore does after a row is gone.
type deletingConvStore struct {
	lifecycleConvStore
}

func (s *deletingConvStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
	return nil
}

func (s *deletingConvStore) setTitle(id, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.byID[id]; c != nil {
		c.Title = title
	}
}

func waitManualCompaction(t *testing.T, svc *Service, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for svc.manualCompactionActive(id) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if svc.manualCompactionActive(id) {
		t.Fatal("compaction flag still set")
	}
}

func TestManualCompactionSaveResurrectsADeletedRoom(t *testing.T) {
	const convID = "conv-delete-during-compact"
	store := &deletingConvStore{lifecycleConvStore: lifecycleConvStore{byID: map[string]*domain.Conversation{
		convID: {
			ID: convID, Title: "Original", Status: "idle", Model: "prov:model",
			Messages: manualCompactMessages(24),
		},
	}}}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	adapter := &compactionChatAdapter{before: func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	}}
	svc := New(Deps{
		Conversations: store,
		Settings:      compactionSettings{s: domain.DefaultSettings()},
		Bus:           &compactionEventBus{},
		ResolveModel: func(string) (*domain.Provider, string, string, *contracts.RPCError) {
			return manualCompactProvider(), "model", "key", nil
		},
		Factory: func(context.Context, *domain.Provider, string) (provider.AIProvider, error) {
			return adapter, nil
		},
		Go: func(_ string, fn func()) { go fn() },
	})

	result, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: convID})
	if rpcErr != nil {
		t.Fatalf("start: %s", rpcErr.Message)
	}
	if result.(contracts.ConversationCompactResult).Status != contracts.CompactStatusStarted {
		t.Fatalf("result = %#v", result)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("summary call did not start")
	}
	if err := store.Delete(convID); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitManualCompaction(t, svc, convID)

	saved, err := store.Get(convID)
	if err != nil {
		t.Fatal(err)
	}
	if saved != nil {
		t.Fatalf("deleted room was written back with %d messages", len(saved.Messages))
	}
}

func TestManualCompactionSaveDropsARenameDuringTheModelCall(t *testing.T) {
	const convID = "conv-rename-during-compact"
	store := &deletingConvStore{lifecycleConvStore: lifecycleConvStore{byID: map[string]*domain.Conversation{
		convID: {
			ID: convID, Title: "Original", Status: "idle", Model: "prov:model",
			Messages: manualCompactMessages(24),
		},
	}}}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	adapter := &compactionChatAdapter{before: func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	}}
	svc := New(Deps{
		Conversations: store,
		Settings:      compactionSettings{s: domain.DefaultSettings()},
		Bus:           &compactionEventBus{},
		ResolveModel: func(string) (*domain.Provider, string, string, *contracts.RPCError) {
			return manualCompactProvider(), "model", "key", nil
		},
		Factory: func(context.Context, *domain.Provider, string) (provider.AIProvider, error) {
			return adapter, nil
		},
		Go: func(_ string, fn func()) { go fn() },
	})

	if _, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: convID}); rpcErr != nil {
		t.Fatalf("start: %s", rpcErr.Message)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("summary call did not start")
	}
	store.setTitle(convID, "Renamed during compact")
	close(release)
	waitManualCompaction(t, svc, convID)

	saved, err := store.Get(convID)
	if err != nil || saved == nil {
		t.Fatalf("saved conversation: %v", err)
	}
	if saved.Title != "Renamed during compact" {
		t.Fatalf("title = %q, want the rename that landed during the model call", saved.Title)
	}
}

func TestManualCompactionPanicEmitsNoTerminalEvent(t *testing.T) {
	const convID = "conv-panic-during-compact"
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{
		convID: {
			ID: convID, Status: "idle", Model: "prov:model",
			Messages: manualCompactMessages(24),
		},
	}}
	adapter := &compactionChatAdapter{before: func() { panic("summary adapter failed") }}
	bus := &compactionEventBus{}
	done := make(chan struct{})
	svc := New(Deps{
		Conversations: store,
		Settings:      compactionSettings{s: domain.DefaultSettings()},
		Bus:           bus,
		ResolveModel: func(string) (*domain.Provider, string, string, *contracts.RPCError) {
			return manualCompactProvider(), "model", "key", nil
		},
		Factory: func(context.Context, *domain.Provider, string) (provider.AIProvider, error) {
			return adapter, nil
		},
		Go: func(_ string, fn func()) {
			go func() {
				defer close(done)
				defer func() { _ = recover() }()
				fn()
			}()
		},
	})

	if _, rpcErr := svc.HandleConversationsCompact(context.Background(), contracts.ConversationIDRequest{ID: convID}); rpcErr != nil {
		t.Fatalf("start: %s", rpcErr.Message)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("compaction goroutine did not finish")
	}
	if svc.manualCompactionActive(convID) {
		t.Fatal("server flag still set after the recovered panic")
	}
	events := bus.snapshot()
	for _, event := range events {
		if event == contracts.EventCompactionFailed || event == contracts.EventCompacted {
			return
		}
	}
	t.Fatalf("events = %v, want compaction.failed so the page can leave the compacting state", events)
}
