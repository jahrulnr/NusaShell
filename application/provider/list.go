package appprovider

import (
	"context"
)

// List returns every configured provider, sorted by id — backs the model
// picker and the provider management view. Unlike the repository's
// picker-facing List, InspectList surfaces broken records too: an
// unresolvable key or corrupt file appears marked Broken rather than
// vanishing, since this is the view an operator fixes things from.
func (app *ProviderApp) List(ctx context.Context) ([]ProviderInfo, error) {
	snaps, err := app.providers.InspectList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderInfo, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, app.toInfo(snap))
	}
	return out, nil
}
