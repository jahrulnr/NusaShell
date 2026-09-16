package agent

import (
	"fmt"
	"strings"

	"nusashell/domain"
)

// DeliverPeerMessage queues a message from another conversation and wakes the
// target when it is idle. The queue write is acknowledged to the caller; the
// target provider may still be unavailable, in which case the message stays
// durable for the next normal turn.
func (a *Service) DeliverPeerMessage(targetID, fromID, content string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return fmt.Errorf("target conversation id is required")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("message content is required")
	}
	if err := a.queueAnnouncement(targetID, Announcement{
		Type:    "peer_message",
		Args:    domain.AnnouncementPeerMessageArgs(fromID),
		Message: domain.AnnouncementPeerMessageMessage(fromID, content),
	}); err != nil {
		return err
	}
	a.WakePendingPeerMessage(targetID)
	return nil
}
