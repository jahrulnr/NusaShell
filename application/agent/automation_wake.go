package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/domain"
)

// AutomationWakeFunc delivers a message to a conversation room and nudges an
// idle room into a turn. It is the automation-facing twin of
// DeliverPeerMessage: same queue+wake machinery, different announcement type.
type AutomationWakeFunc func(targetID, source, content string) error

// DeliverAutomationWake queues an automation_event announcement for one
// conversation and wakes the room when it is idle. The queue write is
// acknowledged to the caller; a busy room still drains the announcement at
// the next round boundary, so workflow messages are never lost.
func (a *Service) DeliverAutomationWake(targetID, source, content string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return fmt.Errorf("target conversation id is required")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("message content is required")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "automation"
	}
	if err := a.queueAnnouncement(targetID, Announcement{
		Type:    "automation_event",
		Args:    domain.AnnouncementAutomationEventArgs(source),
		Message: domain.AnnouncementAutomationEventMessage(source, content),
	}); err != nil {
		return err
	}
	a.startIdleConversationTurn(targetID, false)
	return nil
}

// ConversationWakeCapability builds the builtin `conversation.wake`
// capability consumed by automation `uses:` steps. Input:
// {"conversation":"<id>","message":"<text>","source":"<optional label>"}.
// The wake func is injected at composition time so the registry stays free
// of an agent-service dependency.
func ConversationWakeCapability(wake AutomationWakeFunc) BuiltinCapability {
	return BuiltinCapability{
		Name:        "conversation.wake",
		Kind:        domain.CapabilityAction,
		Description: "Queue a message for a user-visible conversation room and wake it when it is idle",
		Execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			var req struct {
				Conversation string `json:"conversation"`
				Message      string `json:"message"`
				Source       string `json:"source"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			if strings.TrimSpace(req.Conversation) == "" {
				return nil, fmt.Errorf("conversation is required")
			}
			if wake == nil {
				return nil, fmt.Errorf("conversation wake is not configured")
			}
			if err := wake(req.Conversation, req.Source, req.Message); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"ok": true, "conversation": req.Conversation})
		},
	}
}
