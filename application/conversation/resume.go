package appconversation

import (
	"context"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Resume continues a room from its current history — unanswered tool
// calls finish, then the model is called again. History is never
// rewritten or truncated: a retry keeps the tool results and partial
// replies the failed turn already produced. A live turn is interrupted
// first (preempt), then resume cuts ahead of queued sends. providerID/
// modelID/opts are the same per-turn overrides Send takes — "" keeps
// the stored binding, and a successful resume re-binds them.
func (app *ConversationApp) Resume(
	ctx context.Context,
	convID string,
	providerID string,
	modelID string,
	opts appjob.SendOptions,
	onEvent event.TurnEventHandler,
) error {
	res := make(chan error, 1)
	j := &appjob.Resume{
		Ctx:        ctx,
		ProviderID: providerID,
		ModelID:    modelID,
		Opts:       opts,
		OnEvent:    onEvent,
	}
	j.Result = res
	app.post(convID, j)
	select {
	case err := <-res:
		return err
	case <-ctx.Done():
		return apperror.New(408, "resume canceled", ctx.Err())
	}
}
