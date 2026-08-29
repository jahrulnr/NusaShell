package application

// RoundStreamRegistry stages live agent round content in memory so SSE
// consumers can attach mid-round, replay from a cursor (after=<seq>), and
// never read torn state from the conversation store.
//
// Contract (see contracts/roster.go):
//   - The conversation store only ever contains round content AFTER the
//     round is sealed (persistTurnRound at the round boundary). During a
//     live round the store may hold at most the empty "running" placeholder;
//     the authoritative live content for round (runID, messageID) lives in
//     the matching RoundStream in this registry.
//   - Every published delta gets a per-stream monotonic Seq. A consumer that
//     missed frames (WS-less reconnect, slow reader) re-opens the stream with
//     after=lastSeq and the registry replays the buffered tail; a gap at the
//     head of the replay is detectable by the first replayed Seq jumping.
//   - Live fan-out is best-effort per subscriber (non-blocking send). Dropped
//     live frames are recoverable via the same replay path, so a slow reader
//     can never deadlock the turn goroutine.
//   - Streams are bounded: sealed streams survive for roundStreamSealedTTL so
//     late joiners get the terminal round.done frame, then are pruned. Live
//     streams idle out after roundStreamIdleTTL.

import (
	"context"
	"errors"
	"sync"
	"time"

	"nusashell/contracts"
)

const (
	// roundStreamFrameCap bounds the replay buffer per stream. Token streams
	// produce single-digit frames per second, so 8192 frames covers very long
	// rounds; older frames are trimmed from the head (Seq keeps increasing, so
	// a consumer whose cursor falls off the head sees a Seq jump and falls
	// back to a snapshot refresh).
	roundStreamFrameCap = 8192
	// roundStreamSubBuf is the per-subscriber live queue. Publishers never
	// block on it; overflow drops and is recovered by replay.
	roundStreamSubBuf = 512
	// roundStreamSealedTTL keeps sealed streams available for late joiners
	// (new tab, room switch) before pruning.
	roundStreamSealedTTL = 90 * time.Second
	// roundStreamIdleTTL prunes live streams that have had no publish for a
	// while (turn crashed without a seal; registry is process-local so the
	// conversation store's healOrphanedRunningConversation handles recovery).
	roundStreamIdleTTL = 10 * time.Minute
	// roundStreamWaitTTL is how long a reader waits for a stream that has not
	// published anything yet (opened between turn.started and the first delta,
	// or for a round the server has not reached).
	roundStreamWaitTTL = 10 * time.Second
)

// ErrRoundStreamNotFound is returned when a stream never appears within the
// wait window (round already GC'd, run unknown, or backend restarted).
var ErrRoundStreamNotFound = errors.New("round stream not found")

// RoundStream is one staged round: (runID, messageID).
type RoundStream struct {
	key       string
	runID     string
	messageID string
	round     int

	mu         sync.Mutex
	seq        int64
	deltas     []contracts.RoundDeltaFrame
	subs       map[int]*roundSubscriber
	nextSubID  int
	sealed     bool
	doneFrame  contracts.RoundDoneFrame
	createdAt  time.Time
	lastActive time.Time
}

type roundSubscriber struct {
	id   int
	ch   chan contracts.RoundDeltaFrame
	done chan struct{}
}

// publish appends one delta and fans it out best-effort. Returns false when
// the stream is already sealed (the delta is dropped; the terminal round.done
// frame is authoritative).
func (s *RoundStream) publish(frame contracts.RoundDeltaFrame) bool {
	s.mu.Lock()
	if s.sealed {
		s.mu.Unlock()
		return false
	}
	s.seq++
	frame.Seq = s.seq
	s.deltas = append(s.deltas, frame)
	if len(s.deltas) > roundStreamFrameCap {
		s.deltas = append([]contracts.RoundDeltaFrame(nil), s.deltas[len(s.deltas)-roundStreamFrameCap:]...)
	}
	s.lastActive = time.Now()
	subs := make([]*roundSubscriber, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub.ch <- frame:
		default: // slow reader: drop; replay after= covers it
		}
	}
	return true
}

// seal marks the stream terminal and releases every subscriber.
func (s *RoundStream) seal(done contracts.RoundDoneFrame) {
	s.mu.Lock()
	if s.sealed {
		s.mu.Unlock()
		return
	}
	s.sealed = true
	s.doneFrame = done
	subs := make([]*roundSubscriber, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	s.subs = map[int]*roundSubscriber{}
	s.mu.Unlock()
	for _, sub := range subs {
		close(sub.done)
	}
}

// subscribe attaches a consumer at a cursor. Buffered deltas newer than
// after are replayed synchronously (blocking into the subscriber's queue —
// the transport reads immediately after subscribe, so this drains); once the
// stream is sealed, Done is closed and DoneFrame carries the terminal state.
func (s *RoundStream) subscribe(after int64) *RoundStreamSub {
	sub := &roundSubscriber{
		ch:   make(chan contracts.RoundDeltaFrame, roundStreamSubBuf),
		done: make(chan struct{}),
	}
	s.mu.Lock()
	sub.id = s.nextSubID
	s.nextSubID++
	s.lastActive = time.Now()
	sealed := s.sealed
	var replay []contracts.RoundDeltaFrame
	for _, f := range s.deltas {
		if f.Seq > after {
			replay = append(replay, f)
		}
	}
	if !sealed {
		s.subs[sub.id] = sub
	}
	s.mu.Unlock()
	for _, f := range replay {
		sub.ch <- f
	}
	if sealed {
		close(sub.done)
	}
	return &RoundStreamSub{stream: s, sub: sub}
}

// unsubscribe detaches a live subscriber (transport closed the stream early).
func (s *RoundStream) unsubscribe(sub *roundSubscriber) {
	s.mu.Lock()
	if _, ok := s.subs[sub.id]; ok {
		delete(s.subs, sub.id)
	}
	s.mu.Unlock()
}

// RoundStreamSub is one consumer attachment.
type RoundStreamSub struct {
	stream *RoundStream
	sub    *roundSubscriber
}

// Frames delivers live/replayed deltas in Seq order.
func (s *RoundStreamSub) Frames() <-chan contracts.RoundDeltaFrame { return s.sub.ch }

// Done is closed when the round is sealed.
func (s *RoundStreamSub) Done() <-chan struct{} { return s.sub.done }

// DoneFrame returns the terminal frame (valid after Done closes).
func (s *RoundStreamSub) DoneFrame() contracts.RoundDoneFrame {
	s.stream.mu.Lock()
	defer s.stream.mu.Unlock()
	return s.stream.doneFrame
}

// Close detaches the subscriber.
func (s *RoundStreamSub) Close() {
	s.stream.unsubscribe(s.sub)
}

// sealRound seals the round stream for the given message with the terminal
// frame, chaining to next when the agent continues with another round.
func (a *App) sealRound(run *TurnRun, messageID string, round int, state string, next *contracts.RoundRef, usage *contracts.UsageDTO, errMsg string) {
	if a.RoundStreams == nil {
		return
	}
	a.RoundStreams.Seal(run.ID, messageID, round, contracts.RoundDoneFrame{
		State: state, RunID: run.ID, MessageID: messageID, Round: round, Usage: usage, Next: next, Error: errMsg,
	})
}

// RoundStreamRegistry owns all live and recently sealed round streams.
type RoundStreamRegistry struct {
	mu               sync.Mutex
	streams          map[string]*RoundStream
	waiters          map[string][]chan struct{}
	publishesSinceGC int
}

// NewRoundStreamRegistry builds an empty registry.
func NewRoundStreamRegistry() *RoundStreamRegistry {
	return &RoundStreamRegistry{
		streams: map[string]*RoundStream{},
		waiters: map[string][]chan struct{}{},
	}
}

// key is the registry key for (runID, messageID).
func keyFor(runID, messageID string) string { return runID + "|" + messageID }

// Reset clears a live stream's buffered frames and seq so a provider retry
// restarts the round cleanly (the failed attempt's deltas are discarded; the
// frontend re-opens with after=0). Consumers attached to the old stream are
// detached — they must re-subscribe after the reset.
func (r *RoundStreamRegistry) Reset(runID, messageID string) {
	r.mu.Lock()
	st := r.streams[keyFor(runID, messageID)]
	r.publishesSinceGC++
	r.mu.Unlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	st.deltas = nil
	st.seq = 0
	old := st.subs
	st.subs = map[int]*roundSubscriber{}
	st.lastActive = time.Now()
	st.mu.Unlock()
	for _, sub := range old {
		close(sub.done)
	}
}

// Exists reports whether a stream for the round is currently registered.
func (r *RoundStreamRegistry) Exists(runID, messageID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.streams[keyFor(runID, messageID)]
	return ok
}

// Publish appends a delta to the round stream, creating the stream on first
// use (rounds that produce no deltas are created by Seal instead).
func (r *RoundStreamRegistry) Publish(runID, messageID string, round int, kind, toolCallID, name, text string) {
	st := r.streamFor(runID, messageID, round)
	st.publish(contracts.RoundDeltaFrame{Kind: kind, ToolCallID: toolCallID, Name: name, Text: text})
	r.maybeGC()
}

func (r *RoundStreamRegistry) streamFor(runID, messageID string, round int) *RoundStream {
	key := keyFor(runID, messageID)
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.streams[key]
	if st != nil {
		return st
	}
	st = &RoundStream{
		key:        key,
		runID:      runID,
		messageID:  messageID,
		round:      round,
		subs:       map[int]*roundSubscriber{},
		createdAt:  time.Now(),
		lastActive: time.Now(),
	}
	r.streams[key] = st
	for _, ch := range r.waiters[key] {
		close(ch)
	}
	delete(r.waiters, key)
	return st
}

// Seal closes the stream for (runID, messageID) with the terminal frame.
// Creating a sealed-only stream when none exists lets consumers of
// content-free rounds (e.g. a tool-only round with no text) receive the
// terminal frame immediately.
func (r *RoundStreamRegistry) Seal(runID, messageID string, round int, done contracts.RoundDoneFrame) {
	st := r.streamFor(runID, messageID, round)
	st.seal(done)
	r.maybeGC()
}

// SealError closes the round with a terminal error state.
func (r *RoundStreamRegistry) SealError(runID, messageID string, round int, errMsg string) {
	r.Seal(runID, messageID, round, contracts.RoundDoneFrame{
		State: contracts.RoundStateError, RunID: runID, MessageID: messageID, Round: round, Error: errMsg,
	})
}

// SealInterrupted closes the round because the user stopped the turn.
func (r *RoundStreamRegistry) SealInterrupted(runID, messageID string, round int) {
	r.Seal(runID, messageID, round, contracts.RoundDoneFrame{
		State: contracts.RoundStateInterrupted, RunID: runID, MessageID: messageID, Round: round,
	})
}

// Subscribe attaches to the stream for (runID, messageID), waiting up to
// roundStreamWaitTTL for it to appear (a round that has been signaled by
// agent.turn.started but has not published a delta yet). Returns
// ErrRoundStreamNotFound when the stream never appears (unknown round, or
// already GC'd).
func (r *RoundStreamRegistry) Subscribe(ctx context.Context, runID, messageID string, after int64) (*RoundStreamSub, error) {
	key := keyFor(runID, messageID)
	deadline := time.NewTimer(roundStreamWaitTTL)
	defer deadline.Stop()
	r.mu.Lock()
	if st := r.streams[key]; st != nil {
		r.mu.Unlock()
		return st.subscribe(after), nil
	}
	waiter := make(chan struct{})
	r.waiters[key] = append(r.waiters[key], waiter)
	r.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			r.removeWaiter(key, waiter)
			return nil, ctx.Err()
		case <-deadline.C:
			r.removeWaiter(key, waiter)
			return nil, ErrRoundStreamNotFound
		case <-waiter:
			r.mu.Lock()
			st := r.streams[key]
			r.mu.Unlock()
			if st != nil {
				return st.subscribe(after), nil
			}
			// Re-register and keep waiting (stream was created and pruned
			// between the notify and our lookup).
			r.mu.Lock()
			newWaiter := make(chan struct{})
			r.waiters[key] = append(r.waiters[key], newWaiter)
			r.mu.Unlock()
			waiter = newWaiter
		}
	}
}

func (r *RoundStreamRegistry) removeWaiter(key string, ch chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	waiters := r.waiters[key]
	for i, w := range waiters {
		if w == ch {
			r.waiters[key] = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(r.waiters[key]) == 0 {
		delete(r.waiters, key)
	}
}

// maybeGC prunes sealed streams past their TTL and dead live streams. Called
// opportunistically from Publish/Seal; the cost is O(streams) once every
// gcInterval publishes.
func (r *RoundStreamRegistry) maybeGC() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publishesSinceGC++
	if r.publishesSinceGC < 256 {
		return
	}
	r.publishesSinceGC = 0
	now := time.Now()
	for key, st := range r.streams {
		st.mu.Lock()
		sealed := st.sealed
		lastActive := st.lastActive
		st.mu.Unlock()
		if sealed && now.Sub(lastActive) > roundStreamSealedTTL {
			delete(r.streams, key)
			continue
		}
		if !sealed && now.Sub(lastActive) > roundStreamIdleTTL {
			delete(r.streams, key)
		}
	}
}
