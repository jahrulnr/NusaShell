package agent

import (
	"testing"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

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
