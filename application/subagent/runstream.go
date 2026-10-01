package subagent

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"nusashell/contracts"
	clock "nusashell/pkg/time"
)

const (
	runStreamSubscriberBuffer = 4
	runStreamMaxSubscribers   = 32
	runStreamRecentTTL        = 90 * time.Second
	runStreamMaxSnapshots     = 64
	// runStreamCoalesceInterval is the minimum spacing between emitted
	// EventAcpRunUpdated frames for one run. Streaming deltas each carry a
	// full transcript snapshot; publishing per delta is O(transcript) work
	// per token, so intermediate snapshots coalesce to the latest payload.
	runStreamCoalesceInterval = 100 * time.Millisecond
)

type RunStreamRegistry struct {
	mu               sync.Mutex
	conversations    map[string]*runStreamConversation
	nextSubscriberID int
	subscriberCount  int
}

type runStreamConversation struct {
	seq         int64
	runs        map[string]runStreamEntry
	pending     map[string]*runStreamPending
	subscribers map[int]*runStreamSubscriber
}

type runStreamEntry struct {
	frame         contracts.AcpRunStreamFrame
	updated       time.Time
	sourceUpdated time.Time
	expires       time.Time
}

// runStreamPending is the latest coalesced update waiting out the emit
// interval. A pending frame is a snapshot: a newer arrival replaces it
// wholesale, so only the newest payload is ever published.
type runStreamPending struct {
	run           contracts.AcpRunDTO
	sourceUpdated time.Time
	timer         *time.Timer
}

type runStreamSubscriber struct {
	frames chan contracts.AcpRunStreamFrame
	done   chan struct{}
	closed bool
}

type RunStreamSub struct {
	registry       *RunStreamRegistry
	conversationID string
	id             int
	subscriber     *runStreamSubscriber
	snapshot       contracts.AcpRunStreamFrame
}

func NewRunStreamRegistry() *RunStreamRegistry {
	return &RunStreamRegistry{conversations: map[string]*runStreamConversation{}}
}

func (r *RunStreamRegistry) Publish(event string, run contracts.AcpRunDTO) {
	r.publish(event, run, clock.NewTime().Time())
}

func (r *RunStreamRegistry) publish(event string, run contracts.AcpRunDTO, sourceUpdated time.Time) {
	if r == nil || run.ID == "" || strings.TrimSpace(run.ConversationID) == "" || !isRunStreamEvent(event) {
		return
	}
	now := clock.NewTime().Time()
	if sourceUpdated.IsZero() {
		sourceUpdated = now
	}
	runCopy := cloneAcpRunDTO(run)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	stream := r.conversationLocked(run.ConversationID)
	if !runStreamAccepted(stream, run.ID, event, runCopy, sourceUpdated) {
		return
	}
	// Terminal state, lifecycle boundaries, and permission prompts must not
	// wait out the coalescing interval. They also drain any pending update
	// first so emit order follows producer order.
	immediate := event != contracts.EventAcpRunUpdated ||
		!isLiveRunStatus(run.Status) || run.PendingPermission != nil
	if pending := stream.pending[run.ID]; pending != nil {
		if !immediate {
			// Latest snapshot wins; an older source stamp must not displace it.
			if !sourceUpdated.Before(pending.sourceUpdated) {
				pending.run = runCopy
				pending.sourceUpdated = sourceUpdated
			}
			return
		}
		r.emitPendingLocked(stream, run.ID, now)
	}
	if !immediate {
		if entry, ok := stream.runs[run.ID]; ok {
			if remaining := runStreamCoalesceInterval - now.Sub(entry.updated); remaining > 0 {
				stream.pending[run.ID] = &runStreamPending{
					run:           runCopy,
					sourceUpdated: sourceUpdated,
					timer: time.AfterFunc(remaining, func() {
						r.flushPending(run.ConversationID, run.ID)
					}),
				}
				return
			}
		}
	}
	r.emitLocked(stream, event, runCopy, sourceUpdated, now)
	r.pruneLocked(now)
}

// runStreamAccepted applies the per-run ordering rules against the last
// EMITTED frame: done regression, stale source stamps, lower-priority same
// stamps, and identical retransmissions.
func runStreamAccepted(stream *runStreamConversation, runID, event string, runCopy contracts.AcpRunDTO, sourceUpdated time.Time) bool {
	previous, ok := stream.runs[runID]
	if !ok {
		return true
	}
	switch {
	case previous.frame.Type == contracts.EventAcpRunDone && event != contracts.EventAcpRunDone,
		sourceUpdated.Before(previous.sourceUpdated):
		return false
	case sourceUpdated.Equal(previous.sourceUpdated) &&
		runStreamEventPriority(event) <= runStreamEventPriority(previous.frame.Type):
		// Same source stamp, strictly lower priority: stale ordering.
		if runStreamEventPriority(event) < runStreamEventPriority(previous.frame.Type) {
			return false
		}
		// Same stamp, same priority, identical payload: a retransmission.
		if previous.frame.Run != nil && reflect.DeepEqual(*previous.frame.Run, runCopy) {
			return false
		}
		// Same stamp but a different payload means the producer's clock
		// collapsed two distinct versions onto one value (coarse timer
		// granularity, e.g. Windows). Arrival order is authoritative —
		// accept it; the assigned seq still orders it after the previous.
	}
	return true
}

// emitLocked stores and broadcasts one frame. Caller holds r.mu.
func (r *RunStreamRegistry) emitLocked(stream *runStreamConversation, event string, runCopy contracts.AcpRunDTO, sourceUpdated, now time.Time) {
	stream.seq++
	frame := contracts.AcpRunStreamFrame{Type: event, Seq: stream.seq, Run: &runCopy}
	entry := runStreamEntry{frame: frame, updated: now, sourceUpdated: sourceUpdated}
	if event == contracts.EventAcpRunDone || !isLiveRunStatus(runCopy.Status) {
		entry.expires = now.Add(runStreamRecentTTL)
	}
	stream.runs[runCopy.ID] = entry
	for id, sub := range stream.subscribers {
		select {
		case sub.frames <- cloneAcpRunStreamFrame(frame):
		default:
			delete(stream.subscribers, id)
			r.subscriberCount--
			sub.closed = true
			close(sub.done)
		}
	}
}

// flushPending emits a coalesced update when its interval elapses. This is
// the timer callback: the pending map lookup makes a fire-after-drop a no-op,
// so a timer can never resurrect a pruned conversation or run.
func (r *RunStreamRegistry) flushPending(conversationID, runID string) {
	now := clock.NewTime().Time()
	r.mu.Lock()
	defer r.mu.Unlock()
	stream := r.conversations[conversationID]
	if stream == nil {
		return
	}
	r.emitPendingLocked(stream, runID, now)
	r.pruneLocked(now)
}

// emitPendingLocked drains a coalesced update, if one is armed. Caller holds
// r.mu.
func (r *RunStreamRegistry) emitPendingLocked(stream *runStreamConversation, runID string, now time.Time) {
	pending := stream.pending[runID]
	if pending == nil {
		return
	}
	delete(stream.pending, runID)
	if pending.timer != nil {
		pending.timer.Stop()
	}
	// No frame for this run can emit while a pending snapshot sits armed —
	// immediate events drain it first — so the ordering checks it passed on
	// arrival still hold. Re-check only the cheap staleness guard against
	// eviction/time pathology.
	if previous, ok := stream.runs[runID]; ok {
		if previous.frame.Type == contracts.EventAcpRunDone || pending.sourceUpdated.Before(previous.sourceUpdated) {
			return
		}
	}
	r.emitLocked(stream, contracts.EventAcpRunUpdated, pending.run, pending.sourceUpdated, now)
}

func (r *RunStreamRegistry) Subscribe(conversationID string) *RunStreamSub {
	if r == nil || strings.TrimSpace(conversationID) == "" {
		return nil
	}
	now := clock.NewTime().Time()
	r.mu.Lock()
	r.pruneLocked(now)
	if r.subscriberCount >= runStreamMaxSubscribers {
		r.mu.Unlock()
		return nil
	}
	stream := r.conversationLocked(conversationID)
	r.nextSubscriberID++
	id := r.nextSubscriberID
	sub := &runStreamSubscriber{
		frames: make(chan contracts.AcpRunStreamFrame, runStreamSubscriberBuffer),
		done:   make(chan struct{}),
	}
	stream.subscribers[id] = sub
	r.subscriberCount++
	snapshot := r.snapshotLocked(conversationID, stream)
	r.mu.Unlock()
	return &RunStreamSub{registry: r, conversationID: conversationID, id: id, subscriber: sub, snapshot: snapshot}
}

func (s *RunStreamSub) Snapshot() contracts.AcpRunStreamFrame {
	if s == nil {
		return contracts.AcpRunStreamFrame{}
	}
	return cloneAcpRunStreamFrame(s.snapshot)
}

func (s *RunStreamSub) Frames() <-chan contracts.AcpRunStreamFrame {
	if s == nil || s.subscriber == nil {
		return nil
	}
	return s.subscriber.frames
}

func (s *RunStreamSub) Done() <-chan struct{} {
	if s == nil || s.subscriber == nil {
		return nil
	}
	return s.subscriber.done
}

func (s *RunStreamSub) Close() {
	if s == nil || s.registry == nil || s.subscriber == nil {
		return
	}
	s.registry.unsubscribe(s.conversationID, s.id, s.subscriber)
}

func (r *RunStreamRegistry) conversationLocked(conversationID string) *runStreamConversation {
	stream := r.conversations[conversationID]
	if stream != nil {
		return stream
	}
	stream = &runStreamConversation{
		runs:        map[string]runStreamEntry{},
		pending:     map[string]*runStreamPending{},
		subscribers: map[int]*runStreamSubscriber{},
	}
	r.conversations[conversationID] = stream
	return stream
}

func (r *RunStreamRegistry) snapshotLocked(conversationID string, stream *runStreamConversation) contracts.AcpRunStreamFrame {
	entries := make([]runStreamEntry, 0, len(stream.runs))
	for _, entry := range stream.runs {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].frame.Seq < entries[j].frame.Seq })
	snapshot := contracts.AcpRunStreamFrame{
		Type:           contracts.EventAcpRunSnapshot,
		ConversationID: conversationID,
		Seq:            stream.seq,
		Runs:           make([]contracts.AcpRunDTO, 0, len(entries)),
	}
	for _, entry := range entries {
		if entry.frame.Run != nil {
			snapshot.Runs = append(snapshot.Runs, cloneAcpRunDTO(*entry.frame.Run))
		}
	}
	return snapshot
}

func (r *RunStreamRegistry) unsubscribe(conversationID string, id int, sub *runStreamSubscriber) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if stream := r.conversations[conversationID]; stream != nil && stream.subscribers[id] == sub {
		delete(stream.subscribers, id)
		r.subscriberCount--
	}
	if !sub.closed {
		sub.closed = true
		close(sub.done)
	}
	r.pruneLocked(clock.NewTime().Time())
}

func (r *RunStreamRegistry) pruneLocked(now time.Time) {
	total := 0
	for _, stream := range r.conversations {
		for id, entry := range stream.runs {
			if !entry.expires.IsZero() && !now.Before(entry.expires) {
				delete(stream.runs, id)
				continue
			}
			total++
		}
	}
	for total > runStreamMaxSnapshots {
		oldestConversation, oldestRun := "", ""
		var oldest time.Time
		for conversationID, stream := range r.conversations {
			for runID, entry := range stream.runs {
				if oldest.IsZero() || entry.updated.Before(oldest) {
					oldestConversation, oldestRun, oldest = conversationID, runID, entry.updated
				}
			}
		}
		if oldestRun == "" {
			break
		}
		delete(r.conversations[oldestConversation].runs, oldestRun)
		total--
	}
	for conversationID, stream := range r.conversations {
		// A pending coalesced update keeps the conversation alive until its
		// flush timer fires, so flushPending always finds its stream.
		if len(stream.runs) == 0 && len(stream.subscribers) == 0 && len(stream.pending) == 0 {
			delete(r.conversations, conversationID)
		}
	}
}

func isRunStreamEvent(event string) bool {
	switch event {
	case contracts.EventAcpRunStarted, contracts.EventAcpRunUpdated, contracts.EventAcpRunDone:
		return true
	default:
		return false
	}
}

func runStreamEventPriority(event string) int {
	switch event {
	case contracts.EventAcpRunStarted:
		return 1
	case contracts.EventAcpRunUpdated:
		return 2
	case contracts.EventAcpRunDone:
		return 3
	default:
		return 0
	}
}

func isLiveRunStatus(status string) bool {
	return status == "starting" || status == "running"
}

func cloneAcpRunDTO(run contracts.AcpRunDTO) contracts.AcpRunDTO {
	run.AvailableModes = append([]contracts.AcpModeDTO(nil), run.AvailableModes...)
	run.Transcript = append([]contracts.AcpTranscriptChunkDTO(nil), run.Transcript...)
	if run.PendingPermission != nil {
		permission := *run.PendingPermission
		permission.Paths = append([]string(nil), permission.Paths...)
		permission.Options = append([]contracts.AcpPermissionOptionDTO(nil), permission.Options...)
		run.PendingPermission = &permission
	}
	return run
}

func cloneAcpRunStreamFrame(frame contracts.AcpRunStreamFrame) contracts.AcpRunStreamFrame {
	if frame.Run != nil {
		run := cloneAcpRunDTO(*frame.Run)
		frame.Run = &run
	}
	if frame.Runs != nil {
		runs := make([]contracts.AcpRunDTO, len(frame.Runs))
		for i := range frame.Runs {
			runs[i] = cloneAcpRunDTO(frame.Runs[i])
		}
		frame.Runs = runs
	}
	return frame
}
