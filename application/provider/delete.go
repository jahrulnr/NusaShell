package appprovider

import (
	"context"
)

// Delete removes the provider and its vaulted credential — 404 when
// absent. Conversations bound to the provider are not touched: their
// stored reference dangles, and the next send surfaces the 404 the app
// layer already routes on, letting the caller rebind.
func (app *ProviderApp) Delete(ctx context.Context, id string) error {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.providers.Delete(ctx, id)
}
