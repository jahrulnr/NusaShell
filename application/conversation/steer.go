package appconversation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/worker"
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
	taken, record, _, err := app.deliverSteer(ctx, convID, "", in)
	return taken, record, err
}

// bindSteering binds a send's mailbox to the transport turn it runs under, and
// returns the release. With no turn id there is nothing to bind: the job's own
// mailbox is the room's running one.
func (app *ConversationApp) bindSteering(convID, turnID string, box *appjob.SteeringInput) func() {
	if turnID == "" || box == nil {
		return func() {}
	}
	app.mu.Lock()
	rooms := app.steering[convID]
	if rooms == nil {
		rooms = map[string]*appjob.SteeringInput{}
		app.steering[convID] = rooms
	}
	rooms[turnID] = box
	app.mu.Unlock()
	return func() {
		app.mu.Lock()
		if rooms := app.steering[convID]; rooms != nil {
			if rooms[turnID] == box {
				delete(rooms, turnID)
			}
			if len(rooms) == 0 {
				delete(app.steering, convID)
			}
		}
		app.mu.Unlock()
	}
}

// steeringFor resolves the mailbox a steer names: the exact turn the transport
// opened when it bound one, else the job the room's worker is running. The
// second case is a turn the job opened for itself, where the running job is
// the only turn there is.
func (app *ConversationApp) steeringFor(convID, turnID string) (*appjob.SteeringInput, *worker.Worker) {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.steering[convID][turnID], app.workers[convID]
}

// AcceptSteer is the journal's view of SteerInput: the receipt is the one the
// running job will claim and persist, so a hub record that keeps it sees the
// same state the fold does. turnID names the exact live turn when the transport
// has one. ok is false when nothing was buffered.
func (app *ConversationApp) AcceptSteer(ctx context.Context, convID, turnID string, in valueobject.UserInput) (*appjob.SteeringReceipt, bool) {
	_, _, receipt, err := app.deliverSteer(ctx, convID, turnID, in)
	if err != nil || receipt == nil {
		return nil, false
	}
	return receipt, true
}

// RecallInput withdraws one pending steer by the id it was submitted under.
func (app *ConversationApp) RecallInput(convID, turnID, inputID string) bool {
	if strings.TrimSpace(inputID) == "" {
		return false
	}
	box, w := app.steeringFor(convID, turnID)
	if box != nil {
		return box.RecallID(inputID)
	}
	if w == nil {
		return false
	}
	return w.RecallInput(inputID)
}

func (app *ConversationApp) deliverSteer(ctx context.Context, convID, turnID string, in valueobject.UserInput) (bool, *entity.Message, *appjob.SteeringReceipt, error) {
	if strings.TrimSpace(in.Text) == "" && len(in.Attachments) == 0 {
		return false, nil, nil, apperror.New(400, "empty steer", fmt.Errorf("steer has no text or uploads"))
	}
	box, w := app.steeringFor(convID, turnID)
	if box == nil && w == nil {
		return false, nil, nil, nil
	}
	if in.Relay != nil {
		found, available := false, false
		if box != nil {
			found, available = true, box.Available(app.cfg.MailboxCap)
		} else {
			found, available = w.SteerInputAvailable()
		}
		if !found {
			return false, nil, nil, nil
		}
		if !available {
			return true, nil, nil, apperror.New(429, "that thread's steering mailbox is full — nothing was written; try again once its turn folds the input it holds", nil)
		}
	}
	var record *entity.Message
	if in.Relay != nil {
		var err error
		record, err = app.persistRelayLine(ctx, convID, in)
		if err != nil {
			return false, nil, nil, err
		}
	}
	if box != nil {
		// The turn's own mailbox: the job reads it whether it is queued or
		// already running, so the input lands on the turn that was named.
		receipt, err := box.SubmitBefore(in, app.cfg.MailboxCap, record)
		if errors.Is(err, appjob.ErrMailboxFull) {
			if in.Relay != nil {
				return false, record, nil, nil
			}
			return true, nil, nil, apperror.New(429, "that thread's steering mailbox is full — nothing was written; try again once its turn folds the input it holds", nil)
		}
		if err != nil {
			return false, nil, nil, apperror.New(500, "could not buffer the steer", err)
		}
		return true, record, receipt, nil
	}
	found, raw, accepted := w.SteerInput(in, record)
	receipt, _ := raw.(*appjob.SteeringReceipt)
	if !found {
		return false, record, nil, nil
	}
	if !accepted {
		if in.Relay != nil {
			// The preflight lost a race. The line already exists, so this is no
			// longer a clean 429: the queue can accept the same record instead.
			return false, record, nil, nil
		}
		return true, nil, nil, apperror.New(429, "that thread's steering mailbox is full — nothing was written; try again once its turn folds the input it holds", nil)
	}
	return true, record, receipt, nil
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

// NextTurn queues text a turn accepted but never reached. The room queue is the
// only waiting room, so the input becomes an ordinary send behind whatever runs
// — never a draft, and never a second mailbox.
func (app *ConversationApp) NextTurn(convID string, in valueobject.UserInput, origin valueobject.TurnOrigin) error {
	_, err := app.Enqueue(context.Background(), convID, "", "", in.Text, in.Attachments, appjob.SendOptions{
		References:  in.References,
		Annotations: in.Annotations,
		Origin:      origin,
	})
	return err
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
