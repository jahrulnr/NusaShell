package application

import (
	"sync"

	"nusashell/domain"
	"nusashell/pkg/nonce"
	clock "nusashell/pkg/time"
)

// Announcement is a harness notice queued for one conversation: the type
// (coalescing key), the self-describing tool args, and the tool result text.
// It is deliberately a plain value — no routing keys, no delivery metadata.
// The persisted pending queue is the queue; publish is Send, the round
// boundary drain is Receive.
type Announcement struct {
	Type    string
	Args    string
	Message string
}

// pendingAnnouncementMessage rebuilds a persisted pending announcement as a
// synthetic assistant message carrying the `announcement` tool call with its
// result pre-filled — the same shape as restartAnnouncement, so the model
// processes it as harness runtime state and the UI renders a normal tool card.
func (a *App) pendingAnnouncementMessage(pa domain.PendingAnnouncement) domain.Message {
	return domain.Message{
		ID:        domain.NewID(domain.IDPrefixMsg),
		Role:      domain.RoleAssistant,
		CreatedAt: clock.NewTime().Time(),
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.AnnouncementToolCallPrefix + nonce.Random(),
			Name:   domain.AnnouncementToolName,
			Args:   pa.Args,
			Status: domain.ToolOK,
			Output: pa.Message,
		}},
	}
}

// drainAnnouncements injects pending harness announcements at a safe round
// boundary (after tool-call). Returns true when at least one announcement
// was injected, forcing the round loop to continue so the model sees it.
//
// The persisted pending queue is the queue: publish appends (coalesced by
// type), this drains everything and clears. The turn lock guarantees a
// single consumer per conversation, and the per-conversation announcement
// lock serializes this against concurrent publishers, so entries are never
// lost or double-injected.
func (a *App) drainAnnouncements(run *TurnRun) (bool, error) {
	if run == nil {
		return false, nil
	}
	lock := a.announcementLock(run.ConversationID)
	lock.Lock()
	defer lock.Unlock()
	repo, err := a.loadRepo(run.ConversationID)
	if err != nil {
		return false, err
	}
	c := repo.Conversation()
	if len(c.PendingAnnouncements) == 0 {
		return false, nil
	}
	for _, pa := range c.PendingAnnouncements {
		c.AddMessage(a.pendingAnnouncementMessage(pa))
	}
	c.PendingAnnouncements = nil
	if err := repo.Save(); err != nil {
		return false, err
	}
	return true, nil
}

// publishAnnouncement queues a harness announcement for one conversation
// (queue.Send). The pending queue coalesces by type, so a burst of changes
// collapses into one announcement. Fail-soft: a missing conversation only
// logs — the change is self-healing at the next hydration epoch.
func (a *App) publishAnnouncement(convID string, ev Announcement) {
	if convID == "" || ev.Type == "" {
		return
	}
	lock := a.announcementLock(convID)
	lock.Lock()
	defer lock.Unlock()
	repo, err := a.loadRepo(convID)
	if err != nil {
		a.log("warn", "agent", "announcement: conversation %s not found: %v", convID, err)
		return
	}
	c := repo.Conversation()
	c.QueueAnnouncement(domain.PendingAnnouncement{
		ID:        domain.AnnouncementToolCallPrefix + nonce.Random(),
		Type:      ev.Type,
		Args:      ev.Args,
		Message:   ev.Message,
		CreatedAt: clock.NewTime().Time(),
	})
	if err := repo.Save(); err != nil {
		a.log("warn", "agent", "announcement: failed to persist pending for %s: %v", convID, err)
	}
}

// publishAnnouncementToAll fans an announcement out to every visible
// conversation except skipConvID (the caller's own room — no
// self-announcement). Used by the background review agent, whose memory and
// skill writes are external to every active agent.
func (a *App) publishAnnouncementToAll(ev Announcement, skipConvID string) {
	if a.Conversations == nil {
		return
	}
	for _, c := range a.Conversations.List() {
		if c == nil || c.HiddenFromRoomList() {
			continue
		}
		if skipConvID != "" && c.ID == skipConvID {
			continue
		}
		a.publishAnnouncement(c.ID, ev)
	}
}

// newAnnouncement builds a queue item for a harness notice.
func newAnnouncement(typ, args, message string) Announcement {
	return Announcement{Type: typ, Args: args, Message: message}
}

// announcementLock returns the per-conversation mutex serializing pending
// announcement load-modify-save (publish vs round-boundary drain).
func (a *App) announcementLock(convID string) *sync.Mutex {
	a.announcementLocksMu.Lock()
	defer a.announcementLocksMu.Unlock()
	if a.announcementLocks == nil {
		a.announcementLocks = map[string]*sync.Mutex{}
	}
	m, ok := a.announcementLocks[convID]
	if !ok {
		m = &sync.Mutex{}
		a.announcementLocks[convID] = m
	}
	return m
}
