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
// back to its queue; a full mailbox is a 429 with nothing written.
//
// An agent room gets one extra step: the agent writes its own reply-side row
// from the text alone, so the provenance line is written before the text is
// handed over. Without it a relayed prompt reaching a busy agent room would
// arrive with no attribution at all.
func (app *ConversationApp) SteerInput(ctx context.Context, convID string, in valueobject.UserInput) (bool, error) {
	if strings.TrimSpace(in.Text) == "" && len(in.Attachments) == 0 {
		return false, apperror.New(400, "empty steer", fmt.Errorf("steer has no text or uploads"))
	}
	app.mu.Lock()
	w := app.workers[convID]
	app.mu.Unlock()
	if w == nil {
		return false, nil
	}
	// The line is written when the room accepts the prompt, whatever runtime it
	// runs: a stop between accept and the turn that would read it must be able to
	// delay delivery, never to lose it. The turn then reads this line instead of
	// writing its own.
	var before func() *entity.Message
	if in.Relay != nil {
		before = func() *entity.Message { return app.persistRelayLine(ctx, convID, in) }
	}
	found, accepted := w.SteerInput(in, before)
	if !found {
		return false, nil
	}
	if !accepted {
		return true, apperror.New(429, "that thread's steering mailbox is full — nothing was written; try again once its turn folds the input it holds", nil)
	}
	return true, nil
}

// persistRelayLine writes the provenance line for a relayed prompt an agent
// room takes mid-turn: one relay-typed user message, the same shape a relayed
// send writes. The text itself reaches the agent through the steer, so the line
// is the record, not a second copy of the prompt path.
func (app *ConversationApp) persistRelayLine(ctx context.Context, convID string, in valueobject.UserInput) *entity.Message {
	msg, err := entity.NewUserMessage(in)
	if err != nil {
		return nil
	}
	msg.SetUserType(enum.LLMUserTypeRelay)
	msg.SetRelay(*in.Relay)
	if _, err := app.depsSnapshot().Convs.AppendMessage(context.WithoutCancel(ctx), convID, msg); err != nil {
		return nil
	}
	return &msg
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
