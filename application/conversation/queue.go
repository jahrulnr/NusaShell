package appconversation

import (
	"context"

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
