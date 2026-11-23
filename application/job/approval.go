package job

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Approvals holds the permission requests waiting on the owner. A request
// lives only while the turn that raised it waits: answering it resumes
// the agent, and the turn ending withdraws it.
type Approvals struct {
	mu      sync.Mutex
	pending map[string]*pendingApproval // by request id
}

type pendingApproval struct {
	convID string
	req    valueobject.ApprovalRequest
	answer chan string // buffered 1; the first answer wins
}

// NewApprovals returns an empty broker.
func NewApprovals() *Approvals {
	return &Approvals{pending: map[string]*pendingApproval{}}
}

// ask publishes req for the owner and waits for an answer. emit carries the
// request and its resolution to the turn's subscribers.
func (a *Approvals) ask(ctx context.Context, convID string, req valueobject.ApprovalRequest, emit event.TurnEventHandler) (string, error) {
	p := &pendingApproval{convID: convID, req: req, answer: make(chan string, 1)}
	a.mu.Lock()
	a.pending[req.ID()] = p
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, req.ID())
		a.mu.Unlock()
	}()
	emit(event.NewApprovalEvent(req))
	select {
	case optionID := <-p.answer:
		emit(event.NewApprovalResolvedEvent(req, optionID))
		return optionID, nil
	case <-ctx.Done():
		emit(event.NewApprovalResolvedEvent(req, ""))
		return "", ctx.Err()
	}
}

// Answer resolves a pending request with one of the options it offered.
// 404 when nothing in that conversation waits under requestID; 400 when
// the option was not offered.
func (a *Approvals) Answer(convID, requestID, optionID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.pending[requestID]
	if p == nil || p.convID != convID {
		return apperror.NewNotFoundError("approval request "+requestID, fmt.Errorf("no pending request %q in %s", requestID, convID))
	}
	if _, ok := p.req.Option(optionID); !ok {
		return apperror.New(400, "approval option not offered", fmt.Errorf("option %q", optionID))
	}
	select {
	case p.answer <- optionID:
	default: // already answered; the first answer stands
	}
	return nil
}

// Pending lists the requests waiting in one conversation, oldest id first.
func (a *Approvals) Pending(convID string) []valueobject.ApprovalRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []valueobject.ApprovalRequest
	for _, p := range a.pending {
		if p.convID == convID {
			out = append(out, p.req)
		}
	}
	slices.SortFunc(out, func(x, y valueobject.ApprovalRequest) int {
		switch {
		case x.ID() < y.ID():
			return -1
		case x.ID() > y.ID():
			return 1
		}
		return 0
	})
	return out
}
