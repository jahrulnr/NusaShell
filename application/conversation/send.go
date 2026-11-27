package appconversation

import (
	"context"
	"fmt"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// SendResult identifies the conversation that received the submission and
// reports whether this submission's user message was persisted, even when
// Send returns an error.
type SendResult struct {
	ConversationID       string
	UserMessagePersisted bool
}

// Send delivers one user turn and returns the conversation id it landed
// in. The room is upserted: an existing convID appends, a new FE-minted
// convID creates it, and "" mints a fresh c_<base32> — the returned id
// always tells the caller which room received the message, so streamed
// events can be attributed. providerID/modelID "" reuse the stored binding;
// on a brand-new conversation both must be given.
//
// Concurrency contract: sends on one conversation queue FIFO on its
// worker, and the queue wait is cancellable through ctx — a send canceled
// while queued returns 408 having touched nothing. Once running, the send
// is reachable through Cancel: the user turn may already be persisted by
// then (it survives a canceled send — history stays honest), but the
// provider call aborts and returns 408, never an upstream error. The
// per-send persistence receipt keeps that distinction accurate if
// cancellation races with AppendMessage. A failure after the user append
// leaves that line in history without an assistant turn; earlier routing
// or validation failures do not persist it. The room keeps its binding
// after a persisted failed turn so a retry can reuse providerID/modelID "".
//
// An empty submission — no text, attachments or annotations — is a 400: it would
// mint an empty room and burn a turn on nothing.
func (app *ConversationApp) Send(
	ctx context.Context,
	convID string,
	providerID string,
	modelID string,
	message string,
	attachments []valueobject.Attachment,
	opts appjob.SendOptions,
	onEvent event.TurnEventHandler,
) (SendResult, error) {
	if opts.Origin.Kind != enum.TurnOriginCompletion {
		// An owner, relay, channel, or automation turn intervenes: the wake
		// budget fills again.
		app.noteOwnerTurn(convID)
	}
	in := valueobject.UserInput{
		Text:        message,
		Attachments: attachments,
		References:  opts.References,
		Annotations: opts.Annotations,
		Recording:   opts.Recording,
	}
	if in.Empty() {
		return SendResult{}, apperror.New(400, "empty message", fmt.Errorf("send has no text, attachments, annotations, or recording"))
	}
	if err := valueobject.CheckReferences(message, opts.References); err != nil {
		return SendResult{}, apperror.New(400, "invalid references", err)
	}
	if convID == "" {
		c, err := aggregate.NewConversation("") // mints c_<base32>
		if err != nil {
			return SendResult{}, err
		}
		convID = c.ID()
	}
	res := make(chan error, 1)
	receipt := appjob.NewPersistenceReceipt()
	// The app owns the turn's mailbox. It exists before the job is posted, so a
	// steer naming this turn lands on it while the job is still queued, and the
	// job reads the same mailbox once it runs. The transport only names the
	// turn — it keeps no mailbox of its own.
	if opts.Steering == nil {
		opts.Steering = appjob.NewSteeringInput()
	}
	defer app.bindSteering(convID, opts.LiveTurn, opts.Steering)()
	j := &appjob.Send{
		Ctx:     ctx,
		Receipt: receipt,

		ProviderID:  providerID,
		ModelID:     modelID,
		Message:     message,
		Attachments: attachments,
		Opts:        opts,
		OnEvent:     onEvent,
	}
	j.Result = res
	app.post(convID, j)
	result := SendResult{ConversationID: convID}
	select {
	case err := <-res:
		result.UserMessagePersisted = receipt.UserMessagePersisted()
		return result, err
	case <-ctx.Done():
		// A queued send has not appended its user line — return now so
		// the caller is not stuck behind the turn in front of it. A
		// send that already committed the user line is inside the turn;
		// wait for the job so a partial assistant reply is on disk
		// before the terminal frame is emitted.
		result.UserMessagePersisted = receipt.UserMessagePersisted()
		if !result.UserMessagePersisted {
			return result, apperror.New(408, "send canceled", ctx.Err())
		}
		err := <-res
		result.UserMessagePersisted = receipt.UserMessagePersisted()
		if err != nil {
			return result, err
		}
		return result, apperror.New(408, "send canceled", ctx.Err())
	}
}
