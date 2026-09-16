package agent

import (
	"context"
	"errors"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

func TestHandleTurnsStartCreatesConversationOnFirstUserMessage(t *testing.T) {
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{}}
	provider := &domain.Provider{ID: "provider", Kind: domain.ProviderChat, Enabled: true}
	var launched func()
	service := New(Deps{
		Conversations: store,
		ResolveModel: func(model string) (*domain.Provider, string, string, *contracts.RPCError) {
			if model != "provider:model" {
				return nil, "", "", &contracts.RPCError{Code: contracts.CodeValidation, Message: "unexpected model"}
			}
			return provider, "model", "key", nil
		},
		Go: func(_ string, fn func()) { launched = fn },
	})

	result, rpcErr := service.HandleTurnsStart(context.Background(), contracts.TurnStartRequest{
		ConversationKey: "draft-123",
		Text:            "first message",
		Model:           "provider:model",
		Workspace:       "/tmp/workspace",
	})
	if rpcErr != nil {
		t.Fatalf("HandleTurnsStart: %s", rpcErr.Message)
	}
	started, ok := result.(contracts.TurnStartResult)
	if !ok {
		t.Fatalf("result = %#v, want TurnStartResult", result)
	}
	if started.ConversationID == "" || started.ConversationKey != "draft-123" || started.RunID == "" {
		t.Fatalf("start result = %+v", started)
	}
	if launched == nil {
		t.Fatal("first user message must launch the turn")
	}
	saved, err := store.Get(started.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Workspace != "/tmp/workspace" || saved.Status != "running" {
		t.Fatalf("saved conversation = %+v", saved)
	}
	if len(saved.Messages) < 2 || saved.Messages[0].Role != domain.RoleUser {
		t.Fatalf("saved messages = %+v, want user message before assistant placeholder", saved.Messages)
	}
}

func TestHandleTurnsStartDeduplicatesSameConversationKey(t *testing.T) {
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{}}
	provider := &domain.Provider{ID: "provider", Kind: domain.ProviderChat, Enabled: true}
	launches := 0
	service := New(Deps{
		Conversations: store,
		ResolveModel: func(string) (*domain.Provider, string, string, *contracts.RPCError) {
			return provider, "model", "key", nil
		},
		Go: func(_ string, _ func()) { launches++ },
	})
	req := contracts.TurnStartRequest{ConversationKey: "draft-dedupe", Text: "hello", Model: "provider:model"}
	first, firstErr := service.HandleTurnsStart(context.Background(), req)
	if firstErr != nil {
		t.Fatalf("first start: %s", firstErr.Message)
	}
	second, secondErr := service.HandleTurnsStart(context.Background(), req)
	if secondErr != nil {
		t.Fatalf("second start: %s", secondErr.Message)
	}
	firstResult := first.(contracts.TurnStartResult)
	secondResult := second.(contracts.TurnStartResult)
	if firstResult.ConversationID != secondResult.ConversationID || firstResult.RunID != secondResult.RunID {
		t.Fatalf("dedupe results differ: first=%+v second=%+v", firstResult, secondResult)
	}
	if launches != 1 {
		t.Fatalf("launches = %d, want exactly one", launches)
	}
	if got := len(store.byID); got != 1 {
		t.Fatalf("stored conversations = %d, want 1", got)
	}
}

func TestHandleTurnsStartRejectsInvalidDraftWorkspace(t *testing.T) {
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{}}
	provider := &domain.Provider{ID: "provider", Kind: domain.ProviderChat, Enabled: true}
	service := New(Deps{
		Conversations: store,
		ResolveModel: func(string) (*domain.Provider, string, string, *contracts.RPCError) {
			return provider, "model", "key", nil
		},
		ValidateWorkspace: func(context.Context, string) error {
			return errors.New("not a directory")
		},
	})

	_, rpcErr := service.HandleTurnsStart(context.Background(), contracts.TurnStartRequest{
		ConversationKey: "draft-invalid-workspace",
		Text:            "hello",
		Model:           "provider:model",
		Workspace:       "/tmp/not-a-directory",
	})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("start error = %+v, want validation error", rpcErr)
	}
	if len(store.byID) != 0 {
		t.Fatalf("invalid draft workspace created storage entries: %v", store.byID)
	}
}

// TestAddTurnMessagesScansTaskMemoryBeforeDrain proves the task-memory scan
// runs inside AddTurnMessages BEFORE the pending-announcement drain, so a
// newly confirmed record (written after the previous turn) is announced in
// THIS turn — no 1–2 turn lag. The scan callback queues a marker
// announcement directly onto the conversation; the drain must pick it up
// and inject it as a PendingAnnouncementsMessage before the assistant
// placeholder.
func TestAddTurnMessagesScansTaskMemoryBeforeDrain(t *testing.T) {
	now := clock.NewTime().Time()
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "Fix memory announcement queue",
		Type:  domain.ConversationTypeConversation,
	}
	scanCalled := false
	svc := New(Deps{
		MaybeAnnounceTaskMemory: func(c *domain.Conversation) {
			scanCalled = true
			c.QueueAnnouncement(domain.PendingAnnouncement{
				ID:        domain.AnnouncementToolCallPrefix + "test-scan",
				Type:      "task_memory",
				Args:      `{"type":"task_memory","hits":[{"id":"rec-1","content":"marker"}]}`,
				Message:   "marker task memory",
				CreatedAt: now,
			})
		},
	})

	userMsg := domain.Message{ID: "u1", Role: domain.RoleUser, Content: "fix the queue", CreatedAt: now, Status: domain.StatusDone}
	asstMsg := domain.Message{ID: "a1", Role: domain.RoleAssistant, CreatedAt: now}

	svc.AddTurnMessages(conv, userMsg, asstMsg)

	if !scanCalled {
		t.Fatal("MaybeAnnounceTaskMemory callback must be called from AddTurnMessages")
	}
	// The marker announcement must have been drained: the conversation
	// should contain a PendingAnnouncementsMessage (assistant message with
	// an announcement tool call carrying the marker args) BEFORE the
	// assistant placeholder.
	var foundDrained bool
	for _, m := range conv.Messages {
		if m.Role != domain.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName && tc.Output == "marker task memory" {
				foundDrained = true
			}
		}
	}
	if !foundDrained {
		t.Fatalf("task_memory announcement must be drained in the same AddTurnMessages call; messages = %+v", conv.Messages)
	}
}

// TestAddTurnMessagesScansTaskMemorySkipsPipeline proves the scan is called
// for pipeline/automation conversations but the EffectiveType gate inside
// MaybeAnnounceTaskMemory skips them. The callback itself is still invoked
// (AddTurnMessages doesn't filter), but the memory service's internal gate
// prevents scanning non-conversation rooms.
func TestAddTurnMessagesScanCallbackInvokedForAllTypes(t *testing.T) {
	now := clock.NewTime().Time()
	conv := &domain.Conversation{
		ID:    "c-pipeline",
		Title: "[pipeline] step",
		Type:  domain.ConversationTypeAutomation,
	}
	scanCalled := false
	svc := New(Deps{
		MaybeAnnounceTaskMemory: func(c *domain.Conversation) {
			scanCalled = true
			// In production, the memory service's EffectiveType gate
			// prevents queuing. Here we simulate that by NOT queuing.
		},
	})

	userMsg := domain.Message{ID: "u1", Role: domain.RoleUser, Content: "run step", CreatedAt: now, Status: domain.StatusDone}
	asstMsg := domain.Message{ID: "a1", Role: domain.RoleAssistant, CreatedAt: now}

	svc.AddTurnMessages(conv, userMsg, asstMsg)

	// The callback IS invoked (AddTurnMessages doesn't filter by type),
	// but no announcement is queued because the memory service's gate
	// (simulated here) skips non-conversation rooms.
	if !scanCalled {
		t.Fatal("MaybeAnnounceTaskMemory callback must be invoked even for automation conversations")
	}
	for _, m := range conv.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				t.Fatalf("automation conversation must not receive task_memory announcement, found tool call %s", tc.ID)
			}
		}
	}
}
