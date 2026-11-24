package wsconversation

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// runResult is how an externally opened turn reports back to its caller.
type runResult struct {
	receipt appconversation.SendResult
	err     error
}

var externalSeq atomic.Int64

// Run opens a send turn that no socket asked for — a message from a chat
// channel — and blocks until it ends. The turn is an ordinary hub turn: it
// is journaled, announced to every socket, watchable, steerable, and
// cancellable from the web. observe sees each event after the web does;
// it runs outside the hub lock. An empty convID mints a new room.
func (h *Hub) Run(ctx context.Context, convID, providerID, modelID, content string, attachments []valueobject.Attachment, opts appjob.SendOptions, observe event.TurnEventHandler) (string, appconversation.SendResult, error) {
	h.mu.Lock()
	h.evictJournalsLocked(time.Now())
	if h.shutting {
		h.mu.Unlock()
		return "", appconversation.SendResult{}, apperror.New(503, "server is shutting down", nil)
	}
	active := 0
	for _, t := range h.turns {
		if !t.terminal {
			active++
		}
	}
	if active >= maxActiveTurns {
		h.mu.Unlock()
		return "", appconversation.SendResult{}, apperror.New(429, "too many active turns", nil)
	}
	if convID == "" {
		conv, err := aggregate.NewConversation("")
		if err != nil {
			h.mu.Unlock()
			return "", appconversation.SendResult{}, err
		}
		convID = conv.ID()
	}
	tid, err := valueobject.NewID("t", "")
	if err != nil {
		h.mu.Unlock()
		return "", appconversation.SendResult{}, err
	}
	opts.Steering = appjob.NewSteeringInput()
	t := &turn{
		id: tid.String(), kind: turnSend, conversationID: convID,
		requestID:  "ext-" + strconv.FormatInt(externalSeq.Add(1), 10),
		providerID: providerID, modelID: modelID, content: content, attachments: attachments, opts: opts,
		steering: opts.Steering, steerByInput: map[string]*steerRecord{},
		subscribed: map[*conn]struct{}{}, observe: observe, done: make(chan runResult, 1),
	}
	h.admitSeq++
	t.admitted = h.admitSeq
	t.turnCtx, t.turnCancel = context.WithCancel(h.ctx)
	t.startedAt = time.Now()
	t.live = newLiveDoc()
	stop := context.AfterFunc(ctx, t.turnCancel)
	defer stop()
	h.turns[t.id] = t
	h.wg.Add(1)
	h.emitLocked(t, "turn.opened", openedPayload{StartedAt: t.startedAt.UnixMilli()}, true)
	h.broadcastLocked(activityFrame(t, "opened"))
	h.mu.Unlock()

	go h.runTurn(t)
	res := <-t.done
	return convID, res.receipt, res.err
}

// Steer hands text to the room's running send turn, folded at its next
// safe point. It reports false when no such turn runs.
func (h *Hub) Steer(convID, text string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range h.turns {
		if t.terminal || t.conversationID != convID || t.kind != turnSend || t.steering == nil {
			continue
		}
		if len(t.steerRecords) >= maxSteerInputs {
			return false
		}
		inputID := "ext-" + strconv.FormatInt(externalSeq.Add(1), 10)
		rec := &steerRecord{
			inputID: inputID,
			content: text,
			payload: messageInputPayload{Content: text},
			// The input remembers the id it was submitted under, so the user
			// message the fold writes names the same submission the live
			// bubble does.
			receipt: t.steering.Submit(valueobject.UserInput{Text: text, InputID: inputID}),
		}
		t.steerRecords = append(t.steerRecords, rec)
		t.steerByInput[inputID] = rec
		h.emitLocked(t, "turn.steer_ack", steerInputPayload{InputID: inputID}, false)
		return true
	}
	return false
}

// Cancel stops every running turn of the room; false when none runs.
func (h *Hub) Cancel(convID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	stopped := false
	for _, t := range h.turns {
		if !t.terminal && t.conversationID == convID {
			t.turnCancel()
			stopped = true
		}
	}
	return stopped
}

// Busy reports whether a turn of the room is running.
func (h *Hub) Busy(convID string) bool {
	_, _, ok := h.ActiveTurn(convID)
	return ok
}

// OnFinished adds fn to run after every turn ends; kind is the command
// that opened it — "send", "edit", or "resume" — and external says the
// turn came through Run. Each call runs on its own goroutine. Register
// listeners before turns start.
func (h *Hub) OnFinished(fn func(convID, kind string, external bool, err error)) {
	h.mu.Lock()
	h.finished = append(h.finished, fn)
	h.mu.Unlock()
}

// OnEvent adds fn to see every turn event of every room, after the web
// does. It runs on the turn's goroutine outside the hub lock, so it must
// return quickly. Register listeners before turns start.
func (h *Hub) OnEvent(fn func(convID string, ev event.TurnEvent)) {
	h.mu.Lock()
	h.events = append(h.events, fn)
	h.mu.Unlock()
}

// observeEvent forwards one event to an external caller outside the lock.
func (t *turn) observeEvent(ev event.TurnEvent) {
	if t.observe != nil {
		t.observe(ev)
	}
}
