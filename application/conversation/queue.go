package appconversation

import (
	"context"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// QueueDepth is how many jobs wait on the room's worker — a queued
// cross-thread message among them. A room with no worker waits for nothing.
func (app *ConversationApp) QueueDepth(convID string) int {
	app.mu.Lock()
	w := app.workers[convID]
	app.mu.Unlock()
	if w == nil {
		return 0
	}
	return w.Depth()
}

// OnQueueChanged hears that a room's queue moved: a message was accepted, or
// the queue drained. The number is read from the queue at emit time, so the
// listener sees the truth and not a counter kept beside it.
func (app *ConversationApp) OnQueueChanged(fn func(convID string)) {
	app.mu.Lock()
	app.onQueueChanged = fn
	app.mu.Unlock()
}

// Enqueue accepts a send for a room without waiting for its turn: the job
// joins the room's own FIFO behind whatever runs, so the room keeps one turn
// at a time and the message keeps its arrival order. It answers the position
// (the queue depth after the post) and never a promise about when the turn
// runs — a server that stops first loses the message, and its sender was told
// only that it was accepted.
func (app *ConversationApp) Enqueue(ctx context.Context, convID, providerID, modelID, text string, attachments []valueobject.Attachment, opts appjob.SendOptions) (int, error) {
	if opts.Origin.Kind != enum.TurnOriginCompletion {
		app.noteOwnerTurn(convID)
	}
	// The line is written before the sender is told the message waits: a queue
	// that lives only in memory must never be the only copy of a prompt another
	// room already handed over. The job uses this record instead of writing a
	// second one when it runs.
	if opts.Relay != nil && opts.AcceptedRecord == nil {
		record := app.persistRelayLine(ctx, convID, valueobject.UserInput{
			Text:        text,
			Attachments: attachments,
			Relay:       opts.Relay,
		})
		if record == nil {
			return 0, apperror.New(500, "could not record the accepted message", nil)
		}
		opts.AcceptedRecord = record
	}
	// The accepted message outlives the caller's turn: a sender that ends its
	// own turn must not cancel the message it just handed over.
	j := &appjob.Send{
		Deps:        app.depsSnapshot(),
		Ctx:         context.WithoutCancel(ctx),
		Receipt:     appjob.NewPersistenceReceipt(),
		ProviderID:  providerID,
		ModelID:     modelID,
		Message:     text,
		Attachments: attachments,
		Opts:        opts,
	}
	j.Result = make(chan error, 1)
	if err := app.post(convID, j); err != nil {
		return 0, err
	}
	app.notifyQueue(convID)
	position := app.QueueDepth(convID)
	if position == 0 {
		// The worker picked it up at once: it is a turn, not a queued message.
		return 0, nil
	}
	return position, nil
}

// notifyQueue announces that a room's queue moved, so its surface can name
// the count.
func (app *ConversationApp) notifyQueue(convID string) {
	app.mu.Lock()
	fn := app.onQueueChanged
	app.mu.Unlock()
	if fn != nil {
		fn(convID)
	}
}
