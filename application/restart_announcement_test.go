package application

import (
	"testing"
	"time"

	"nusashell/domain"
)

func TestShouldAnnounceRestart(t *testing.T) {
	startedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	before := startedAt.Add(-time.Hour)
	after := startedAt.Add(time.Hour)

	persisted := &domain.Conversation{
		ID: "conv_old", UpdatedAt: before,
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hi"}},
	}
	if !shouldAnnounceRestart(persisted, startedAt) {
		t.Error("conversation used before restart must announce")
	}

	fresh := &domain.Conversation{
		ID: "conv_new", UpdatedAt: after,
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hi"}},
	}
	if shouldAnnounceRestart(fresh, startedAt) {
		t.Error("conversation created after restart must not announce")
	}

	empty := &domain.Conversation{ID: "conv_empty", UpdatedAt: before}
	if shouldAnnounceRestart(empty, startedAt) {
		t.Error("empty conversation (no history) must not announce")
	}

	touched := &domain.Conversation{
		ID: "conv_announced", UpdatedAt: after,
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hi"}},
	}
	if shouldAnnounceRestart(touched, startedAt) {
		t.Error("conversation already active in this process must not re-announce")
	}

	if shouldAnnounceRestart(persisted, time.Time{}) {
		t.Error("zero startedAt must disable announcements")
	}
}

func TestRestartAnnouncementShape(t *testing.T) {
	app := &App{Bus: NewBus()}
	msg := app.restartAnnouncement()
	if msg.Role != domain.RoleAssistant || msg.Status != domain.StatusDone {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != domain.AnnouncementToolName {
		t.Fatalf("tool name = %q", tc.Name)
	}
	if !domain.IsAnnouncementCallID(tc.ID) {
		t.Fatalf("call id %q must use the announce- prefix", tc.ID)
	}
	if tc.Status != domain.ToolOK || tc.Output != domain.AnnouncementMessage {
		t.Fatalf("tool call must be pre-completed: %+v", tc)
	}
}

// TestAddTurnMessagesInjectsRestartAnnouncement verifies the injection
// point (addTurnMessages): a conversation whose history predates the
// process gets the announcement appended between the user message and
// the assistant turn, and only once per restart.
func TestAddTurnMessagesInjectsRestartAnnouncement(t *testing.T) {
	startedAt := time.Now().UTC()
	oldUpdated := startedAt.Add(-2 * time.Hour)
	conv := &domain.Conversation{
		ID:        "conv_old",
		CreatedAt: oldUpdated,
		UpdatedAt: oldUpdated,
		Messages: []domain.Message{
			{ID: "m_old", Role: domain.RoleUser, Content: "earlier message", Status: domain.StatusDone},
			{ID: "m_old2", Role: domain.RoleAssistant, Content: "earlier reply", Status: domain.StatusDone},
		},
	}
	app := &App{startedAt: startedAt}

	app.addTurnMessages(conv,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "hello again", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)

	if len(conv.Messages) != 5 {
		t.Fatalf("messages = %d, want 5 (old 2 + user + announcement + assistant)", len(conv.Messages))
	}
	if conv.Messages[2].ID != "m_user" {
		t.Fatalf("message[2] must be the user message: %+v", conv.Messages[2])
	}
	announcement := conv.Messages[3]
	if announcement.Role != domain.RoleAssistant || len(announcement.ToolCalls) != 1 {
		t.Fatalf("message[3] must be the announcement: %+v", announcement)
	}
	tc := announcement.ToolCalls[0]
	if tc.Name != domain.AnnouncementToolName || tc.Output != domain.AnnouncementMessage {
		t.Fatalf("unexpected announcement tool call: %+v", tc)
	}
	if conv.Messages[4].ID != "m_asst" {
		t.Fatalf("message[4] must be the assistant turn message: %+v", conv.Messages[4])
	}

	// One-shot per restart: addTurnMessages bumped UpdatedAt past
	// startedAt, so the next turn in the same process gets nothing.
	app.addTurnMessages(conv,
		domain.Message{ID: "m_user2", Role: domain.RoleUser, Content: "again", Status: domain.StatusDone},
		domain.Message{ID: "m_asst2", Role: domain.RoleAssistant},
	)
	if len(conv.Messages) != 7 {
		t.Fatalf("messages after second turn = %d, want 7 (no repeat announcement)", len(conv.Messages))
	}
	for _, m := range conv.Messages[5:] {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				t.Fatalf("second turn must not repeat the announcement: %+v", tc)
			}
		}
	}
}

// TestAddTurnMessagesSkipsFreshOrEmpty verifies that conversations
// created after startup or without history never get the announcement.
func TestAddTurnMessagesSkipsFreshOrEmpty(t *testing.T) {
	startedAt := time.Now().UTC()
	app := &App{startedAt: startedAt}

	fresh := &domain.Conversation{
		ID: "conv_fresh", CreatedAt: startedAt.Add(time.Minute), UpdatedAt: startedAt.Add(time.Minute),
		Messages: []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hi", Status: domain.StatusDone}},
	}
	app.addTurnMessages(fresh,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "first message", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	for _, m := range fresh.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				t.Fatalf("fresh conversation must not get an announcement: %+v", tc)
			}
		}
	}

	empty := &domain.Conversation{ID: "conv_empty", UpdatedAt: startedAt.Add(-time.Hour)}
	app.addTurnMessages(empty,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "hi", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	if len(empty.Messages) != 2 {
		t.Fatalf("empty conversation must only get user + assistant, got %d messages", len(empty.Messages))
	}
}
