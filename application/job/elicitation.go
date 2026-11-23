package job

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Elicitations holds agents' requests for input waiting on the owner, like
// Approvals: a request lives while the turn that raised it waits.
type Elicitations struct {
	mu      sync.Mutex
	pending map[string]*pendingElicitation
}

type pendingElicitation struct {
	convID string
	req    valueobject.Elicitation
	answer chan valueobject.ElicitationAnswer
}

// NewElicitations returns an empty broker.
func NewElicitations() *Elicitations {
	return &Elicitations{pending: map[string]*pendingElicitation{}}
}

func (b *Elicitations) ask(ctx context.Context, convID string, req valueobject.Elicitation, emit event.TurnEventHandler) (valueobject.ElicitationAnswer, error) {
	p := &pendingElicitation{convID: convID, req: req, answer: make(chan valueobject.ElicitationAnswer, 1)}
	b.mu.Lock()
	b.pending[req.ID()] = p
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, req.ID())
		b.mu.Unlock()
	}()
	emit(event.NewElicitationEvent(req))
	select {
	case a := <-p.answer:
		emit(event.NewElicitationResolvedEvent(req, a.Action))
		return a, nil
	case <-ctx.Done():
		emit(event.NewElicitationResolvedEvent(req, ""))
		return valueobject.ElicitationAnswer{Action: "cancel"}, ctx.Err()
	}
}

// Answer resolves a pending request. 404 when nothing waits under id in
// that conversation; 400 for an unknown action.
func (b *Elicitations) Answer(convID, id string, a valueobject.ElicitationAnswer) error {
	if !a.Valid() {
		return apperror.New(400, "action must be accept, decline, or cancel", nil)
	}
	if a.Action != "accept" {
		a.Content = nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.pending[id]
	if p == nil || p.convID != convID {
		return apperror.NewNotFoundError("elicitation "+id, fmt.Errorf("no pending elicitation %q in %s", id, convID))
	}
	if a.Action == "accept" {
		if err := p.req.Check(a.Content); err != nil {
			return apperror.New(400, err.Error(), err)
		}
	}
	select {
	case p.answer <- a:
	default:
	}
	return nil
}

// Pending lists the requests waiting in one conversation, by id.
func (b *Elicitations) Pending(convID string) []valueobject.Elicitation {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []valueobject.Elicitation
	for _, p := range b.pending {
		if p.convID == convID {
			out = append(out, p.req)
		}
	}
	slices.SortFunc(out, func(x, y valueobject.Elicitation) int { return strings.Compare(x.ID(), y.ID()) })
	return out
}
