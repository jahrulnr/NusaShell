package appchannel

import (
	"context"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// UseChatIndex sets where the channels keep their accounts' messages, so
// the owner can search them.
func (a *App) UseChatIndex(index drepo.ChatIndex) {
	a.mu.Lock()
	a.index = index
	a.mu.Unlock()
}

// Search pages through one channel's kept messages, newest first.
func (a *App) Search(ctx context.Context, f drepo.ChatSearch) (drepo.ChatSearchPage, error) {
	a.mu.Lock()
	index := a.index
	_, known := a.channels[f.Channel]
	a.mu.Unlock()
	if !known || index == nil {
		return drepo.ChatSearchPage{}, apperror.NewNotFoundError("messages of channel "+f.Channel, nil)
	}
	return index.Search(ctx, f)
}
