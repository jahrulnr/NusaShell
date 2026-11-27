package appconversation

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Steer injects user input into the conversation's running turn. The
// text lands at the next inference safe point — after tool results
// persist, before the following provider call — as an ordinary user
// message in history. A turn that ends without reaching a safe point
// never consumes the input; the caller decides what it becomes (the TUI
// queues it as a new message so the line is never dropped).
//
// 404 — no running turn, or the running job does not take input.
func (app *ConversationApp) Steer(ctx context.Context, convID string, message string) error {
	if strings.TrimSpace(message) == "" {
		return apperror.New(400, "empty steer", fmt.Errorf("steer has no text"))
	}
	app.mu.Lock()
	w := app.workers[convID]
	app.mu.Unlock()
	if w == nil || !w.Steer(message) {
		return apperror.NewNotFoundError("in-flight turn "+convID, fmt.Errorf("no steerable turn running"))
	}
	return nil
}

// SteerInput carries a full input — text with its selections, uploads, marks,
// and the relay record when another thread sent it — into the room's running
// turn. found=false means no live turn takes input here, so the caller falls
// back to its queue. The returned record is the acceptance line this call
// wrote: when a race keeps the live turn from taking the input, the fallback
// queue carries the same line instead of writing another.
//
// A relayed prompt is recorded before it is handed over, whatever runtime the
// room runs. If the uncontended mailbox is already full the refusal happens
// before that write; a capacity race after the write is not a refusal — the
// caller falls back to the queue with the record.
func (app *ConversationApp) SteerInput(ctx context.Context, convID string, in valueobject.UserInput) (bool, *entity.Message, error) {
	if strings.TrimSpace(in.Text) == "" && len(in.Attachments) == 0 {
		return false, nil, apperror.New(400, "empty steer", fmt.Errorf("steer has no text or uploads"))
	}
	app.mu.Lock()
	w := app.workers[convID]
	app.mu.Unlock()
	if w == nil {
		return false, nil, nil
	}
	if in.Relay != nil {
		found, available := w.SteerInputAvailable()
		if !found {
			return false, nil, nil
		}
		if !available {
			return true, nil, apperror.New(429, "that thread's steering mailbox is full — nothing was written; try again once its turn folds the input it holds", nil)
		}
	}
	var record *entity.Message
	if in.Relay != nil {
		var err error
		record, err = app.persistRelayLine(ctx, convID, in)
		if err != nil {
			return false, nil, err
		}
	}
	found, accepted := w.SteerInput(in, record)
	if !found {
		return false, record, nil
	}
	if !accepted {
		if in.Relay != nil {
			// The preflight lost a race. The line already exists, so this is no
			// longer a clean 429: the queue can accept the same record instead.
			return false, record, nil
		}
		return true, nil, apperror.New(429, "that thread's steering mailbox is full — nothing was written; try again once its turn folds the input it holds", nil)
	}
	return true, record, nil
}

// persistRelayLine is the room's one writer of a relayed prompt's provenance:
// one relay-typed user message carrying relay{id,label,title}. Delivery paths
// decide when the prompt runs; they never decide whether this line exists.
func (app *ConversationApp) persistRelayLine(ctx context.Context, convID string, in valueobject.UserInput) (*entity.Message, error) {
	msg, err := entity.NewUserMessage(in)
	if err != nil {
		return nil, apperror.New(400, "invalid relayed message", err)
	}
	msg.SetUserType(enum.LLMUserTypeRelay)
	msg.SetRelay(*in.Relay)
	// The line's own id is the identity the live steer event names: a frontend
	// pairs them and draws one bubble, never the row plus a second live copy.
	msg.SetInputID(msg.ID())
	if _, err := app.depsSnapshot().Convs.AppendMessage(context.WithoutCancel(ctx), convID, msg); err != nil {
		return nil, apperror.New(500, "could not record the accepted message", err)
	}
	return &msg, nil
}

// RecallSteer withdraws the newest unconsumed steering input so the
// caller can hand the text back — the composer restore path on Esc.
// ok=false — nothing pending, already folded into history, or no
// running turn.
func (app *ConversationApp) RecallSteer(convID string) (string, bool) {
	app.mu.Lock()
	w := app.workers[convID]
	app.mu.Unlock()
	if w == nil {
		return "", false
	}
	return w.RecallSteer()
}
