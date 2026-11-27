package wsconversation

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// parseTurnKind reads the wire word a job names its turn with.
func parseTurnKind(kind string) (turnKind, bool) {
	switch kind {
	case "send":
		return turnSend, true
	case "edit":
		return turnEdit, true
	case "resume":
		return turnResume, true
	}
	return 0, false
}

// Open implements the job-side live-turn surface: a room job that reached
// execution without an event handler gets one here, so its turn is journaled and
// watchable exactly like a turn a socket admitted.
//
// It fills a surface that does not exist yet — it never replaces or duplicates
// one. A job that already carries a handler keeps it and does not ask; a turn a
// socket admitted already has its journal, so opening here would give that one
// turn two journals, two opened frames, and two terminals.
//
// A refusal — an unknown kind, a full pool, a stopping server — answers nil, and
// the turn runs unjournaled exactly as it did before: the pool is observability,
// never a gate on work.
func (h *Hub) Open(convID, kind string, origin valueobject.TurnOrigin) (event.TurnEventHandler, func(persisted bool, err error)) {
	k, ok := parseTurnKind(kind)
	if !ok {
		slog.Warn("live turn refused: unknown kind", "conversation", convID, "kind", kind)
		return nil, nil
	}
	h.mu.Lock()
	h.evictJournalsLocked(time.Now())
	active := 0
	for _, t := range h.turns {
		if !t.terminal {
			active++
		}
	}
	if h.shutting || active >= maxActiveTurns {
		// One line per room per exhaustion episode, counted: a room that keeps
		// running turns while the pool is full must not become a log storm.
		h.journalRefusals[convID]++
		if h.journalRefusals[convID] == 1 {
			slog.Warn("journal pool full", "conversation", convID, "active", active, "kind", kind)
		}
		h.mu.Unlock()
		return nil, nil
	}
	delete(h.journalRefusals, convID)
	tid, err := valueobject.NewID("t", "")
	if err != nil {
		h.mu.Unlock()
		slog.Warn("live turn refused: could not mint turn id", "conversation", convID, "error", err)
		return nil, nil
	}
	t := &turn{
		id: tid.String(), kind: k, origin: origin, conversationID: convID,
		subscribed: map[*conn]struct{}{}, startedAt: time.Now(), live: newLiveDoc(),
		// The room's worker owns the execution: cancel and steer for this
		// turn are forwarded there, never to the bookkeeping ctx below.
		jobOwned: true,
	}
	// The job owns the turn's cancellation; the journal only borrows the
	// server's context for its own bookkeeping, so there is never a second
	// owner of "stop this turn".
	t.turnCtx, t.turnCancel = context.WithCancelCause(h.ctx)
	// Deliberately NOT registered on h.wg: that group is the hub's own lifetime,
	// and a room job's is already accounted by its worker and the shutdown
	// barrier. Registering here would make Shutdown wait on the same job twice,
	// and a wedged job would hold the hub's Wait as well.
	h.admitSeq++
	t.admitted = h.admitSeq
	h.turns[t.id] = t
	h.emitLocked(t, "turn.opened", h.openedBody(t), true)
	h.broadcastLocked(activityFrame(t, "opened"))
	h.mu.Unlock()

	handler := func(ev event.TurnEvent) {
		h.onEvent(t, ev)
		t.observeEvent(ev)
		h.mu.Lock()
		listeners := h.events
		h.mu.Unlock()
		for _, fn := range listeners {
			fn(convID, ev)
		}
	}
	var once sync.Once
	// seal runs on every exit of the turn, including a failed one: a journal
	// left open would hold a pool slot until the server restarts, and its
	// watchers would wait for a terminal that never comes.
	seal := func(persisted bool, err error) {
		once.Do(func() { h.sealTurn(t, persisted, err) })
	}
	return handler, seal
}
