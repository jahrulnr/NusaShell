package appconversation

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// Environment answers where a room's workspace and commands run. A cross-thread
// sender reads it to decide the form an attachment takes: a local target can
// resolve a path, a remote one needs the bytes. ConversationInfo does not carry
// the environment, so this loads the aggregate.
func (app *ConversationApp) Environment(ctx context.Context, convID string) (enum.Environment, error) {
	conv, err := app.depsSnapshot().Convs.Load(ctx, convID)
	if err != nil {
		return "", err
	}
	return conv.Environment(), nil
}
