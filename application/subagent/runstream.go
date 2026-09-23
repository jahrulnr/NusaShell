package subagent

import (
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
	subscribers map[int]*runStreamSubscriber
}

type runStreamEntry struct {
	frame         contracts.AcpRunStreamFrame
	updated       time.Time
	sourceUpdated time.Time
	expires       time.Time
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
	if previous, ok := stream.runs[run.ID]; ok &&
		(previous.frame.Type == contracts.EventAcpRunDone && event != contracts.EventAcpRunDone ||
			sourceUpdated.Before(previous.sourceUpdated) ||
			sourceUpdated.Equal(previous.sourceUpdated) && runStreamEventPriority(event) <= runStreamEventPriority(previous.frame.Type)) {
		return
	}
	stream.seq++
	frame := contracts.AcpRunStreamFrame{Type: event, Seq: stream.seq, Run: &runCopy}
	entry := runStreamEntry{frame: frame, updated: now, sourceUpdated: sourceUpdated}
	if event == contracts.EventAcpRunDone || !isLiveRunStatus(run.Status) {
		entry.expires = now.Add(runStreamRecentTTL)
	}
	stream.runs[run.ID] = entry
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
	r.pruneLocked(now)
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
		if len(stream.runs) == 0 && len(stream.subscribers) == 0 {
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
