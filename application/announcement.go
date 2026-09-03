package application

import (
	"encoding/json"
	"strings"
	"sync"

	"nusashell/domain"
	"nusashell/pkg/nonce"
	clock "nusashell/pkg/time"
)

// maxPendingAnnouncements caps the persisted pending queue so an
// arbitrarily long idle period cannot grow a drain into context bloat.
// The oldest entry is dropped (with a warn log) once the cap is hit;
// exact-duplicate dedup keeps identical bursts from reaching it at all.
const maxPendingAnnouncements = 64

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

// pendingAnnouncementsMessage rebuilds persisted pending announcements as
// ONE synthetic assistant message carrying a single pre-completed
// `announcement` tool call — the same shape as restartAnnouncement, so the
// model processes it as harness runtime state and the UI renders a normal
// tool card. A single notice keeps its original args and result text
// (backward compatible); multiple notices merge into one card whose args
// list every notice ({"items":[...]}) and whose result joins the texts
// with `---` separators, so an idle burst reads as one runtime-state block
// instead of N cards.
func (a *App) pendingAnnouncementsMessage(items []domain.PendingAnnouncement) domain.Message {
	return domain.Message{
		ID:        domain.NewID(domain.IDPrefixMsg),
		Role:      domain.RoleAssistant,
		CreatedAt: clock.NewTime().Time(),
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.AnnouncementToolCallPrefix + nonce.Random(),
			Name:   domain.AnnouncementToolName,
			Args:   mergedAnnouncementArgs(items),
			Status: domain.ToolOK,
			Output: mergedAnnouncementOutput(items),
		}},
	}
}

// mergedAnnouncementArgs builds the self-describing args payload for a
// drain: a single notice keeps its own args verbatim; multiple notices are
// wrapped as {"items":[...]} with each element the notice's own args
// payload, so every notice stays self-describing for the model and the UI.
// Non-JSON args degrade into a JSON string element instead of breaking the
// array shape.
func mergedAnnouncementArgs(items []domain.PendingAnnouncement) string {
	if len(items) == 1 {
		return items[0].Args
	}
	out := struct {
		Items []json.RawMessage `json:"items"`
	}{Items: make([]json.RawMessage, 0, len(items))}
	for _, pa := range items {
		if json.Valid([]byte(pa.Args)) {
			out.Items = append(out.Items, json.RawMessage(pa.Args))
			continue
		}
		raw, _ := json.Marshal(pa.Args)
		out.Items = append(out.Items, json.RawMessage(raw))
	}
	b, err := json.Marshal(out)
	if err != nil {
		return `{"items":[]}`
	}
	return string(b)
}

// mergedAnnouncementOutput joins the pending notice texts into the
// pre-filled announcement result. A single notice passes through verbatim;
// multiple notices are separated by `---` on their own lines.
func mergedAnnouncementOutput(items []domain.PendingAnnouncement) string {
	if len(items) == 1 {
		return items[0].Message
	}
	parts := make([]string, len(items))
	for i := range items {
		parts[i] = items[i].Message
	}
	return strings.Join(parts, "\n---\n")
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
	items := c.DrainPendingAnnouncements()
	if len(items) == 0 {
		return false, nil
	}
	// All queued notices merge into ONE announcement tool call: the model
	// reads a single runtime-state block (deduped bursts, distinct facts
	// kept) instead of N tool cards.
	c.AddMessage(a.pendingAnnouncementsMessage(items))
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
	// Bound the persisted queue: an idle conversation accruing distinct
	// notices must not grow the next drain into a context bloat. Drop the
	// oldest entry (best-effort order preservation) and log the loss.
	if n := len(c.PendingAnnouncements); n > maxPendingAnnouncements {
		dropped := c.PendingAnnouncements[0]
		c.PendingAnnouncements = append([]domain.PendingAnnouncement(nil), c.PendingAnnouncements[1:]...)
		a.log("warn", "agent", "announcement: dropped oldest pending %s for %s (queue cap %d)", dropped.Type, convID, maxPendingAnnouncements)
	}
	if err := repo.Save(); err != nil {
		a.log("warn", "agent", "announcement: failed to persist pending for %s: %v", convID, err)
	}
}

// publishAnnouncementToAll fans an announcement out to every visible
// conversation except skipConvID (the caller's own room — no
// self-announcement). Used by the background learning agent, whose memory and
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
