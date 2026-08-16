package application

import (
	"fmt"
	"sync"

	"nusashell/domain"
)

// AskQuestionService holds in-flight ask_question tool calls until the UI
// answers them via RPC, or the turn is cancelled / ended.
//
// The service is safe for concurrent use. Each pending ask is keyed by
// (runID, toolCallID) so multiple asks from the same turn are tracked
// independently.
type AskQuestionService struct {
	mu      sync.Mutex
	pending map[string]*pendingAsk
	onAsk   func(runID, callID, conversationID string, req domain.AskQuestionRequest)
}

type pendingAsk struct {
	runID          string
	callID         string
	conversationID string
	req            domain.AskQuestionRequest
	resolve        func(domain.AskQuestionResult)
	reject         func(error)
}

// pendingKey builds the map key for a (runID, callID) pair.
func pendingKey(runID, callID string) string {
	return runID + ":" + callID
}

// NewAskQuestionService creates a ready-to-use service.
func NewAskQuestionService() *AskQuestionService {
	return &AskQuestionService{pending: make(map[string]*pendingAsk)}
}

// SetOnAsk registers a callback fired when a new ask is registered. The
// application layer uses this to emit an EventAskPending over the bus.
func (s *AskQuestionService) SetOnAsk(fn func(runID, callID, conversationID string, req domain.AskQuestionRequest)) {
	s.mu.Lock()
	s.onAsk = fn
	s.mu.Unlock()
}

// Ask registers a pending ask and blocks (via the returned channel) until the
// UI answers or the turn is cancelled. The caller (tool handler) reads from
// the channel to get the result. Returns an error if an ask is already
// pending for the same (runID, callID).
func (s *AskQuestionService) Ask(runID, callID, conversationID string, req domain.AskQuestionRequest) (<-chan domain.AskQuestionResult, error) {
	key := pendingKey(runID, callID)
	s.mu.Lock()
	if _, exists := s.pending[key]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("ask question already pending for call %s", callID)
	}
	ch := make(chan domain.AskQuestionResult, 1)
	s.pending[key] = &pendingAsk{
		runID:          runID,
		callID:         callID,
		conversationID: conversationID,
		req:            req,
		resolve:        func(r domain.AskQuestionResult) { ch <- r },
		reject:         func(err error) { ch <- domain.AskQuestionResult{OK: false, Answer: err.Error()} },
	}
	callback := s.onAsk
	s.mu.Unlock()
	if callback != nil {
		callback(runID, callID, conversationID, req)
	}
	return ch, nil
}

// Answer resolves a pending ask with the user's answer. Returns the tool
// result and an error if the answer is invalid or no pending ask exists.
func (s *AskQuestionService) Answer(runID, callID string, answer domain.AskQuestionAnswer) (domain.AskQuestionResult, error) {
	key := pendingKey(runID, callID)
	s.mu.Lock()
	p, ok := s.pending[key]
	if !ok {
		s.mu.Unlock()
		return domain.AskQuestionResult{}, fmt.Errorf("no pending ask question for call %s", callID)
	}
	delete(s.pending, key)
	s.mu.Unlock()

	result, err := domain.BuildAskQuestionResult(p.req, answer)
	if err != nil {
		p.reject(err)
		return domain.AskQuestionResult{}, err
	}
	p.resolve(result)
	return result, nil
}

// Cancel rejects a single pending ask (e.g. user clicked Cancel on the card).
func (s *AskQuestionService) Cancel(runID, callID string, reason string) {
	key := pendingKey(runID, callID)
	s.mu.Lock()
	p, ok := s.pending[key]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.pending, key)
	s.mu.Unlock()
	if reason == "" {
		reason = "Ask question cancelled by the user"
	}
	p.reject(fmt.Errorf("%s", reason))
}

// RejectRun rejects all pending asks for a run (e.g. turn cancelled, stopped,
// or ended without an answer). This is called from the turn lifecycle cleanup.
func (s *AskQuestionService) RejectRun(runID string, reason string) {
	s.mu.Lock()
	toReject := make([]*pendingAsk, 0)
	for key, p := range s.pending {
		if p.runID == runID {
			toReject = append(toReject, p)
			delete(s.pending, key)
		}
	}
	s.mu.Unlock()
	if reason == "" {
		reason = "Agent turn ended before the ask question was answered"
	}
	for _, p := range toReject {
		p.reject(fmt.Errorf("%s", reason))
	}
}

// HasPending reports whether a pending ask exists for the given (runID, callID).
func (s *AskQuestionService) HasPending(runID, callID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pending[pendingKey(runID, callID)]
	return ok
}

// HasPendingForRun reports whether any pending ask exists for the given run.
func (s *AskQuestionService) HasPendingForRun(runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.pending {
		if p.runID == runID {
			return true
		}
	}
	return false
}
