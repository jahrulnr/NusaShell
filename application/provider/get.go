package appprovider

import (
	"context"
)

// Get returns one provider as a sanitized projection — 404 when absent.
// It reads through Inspect, which never resolves the credential, so a
// record whose key ref is broken still renders (marked Broken) instead
// of erroring — this is the view that must show what's wrong.
func (app *ProviderApp) Get(ctx context.Context, id string) (ProviderInfo, error) {
	snap, err := app.providers.Inspect(ctx, id)
	if err != nil {
		return ProviderInfo{}, err
	}
	return app.toInfo(snap), nil
}
