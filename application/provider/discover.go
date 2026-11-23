package appprovider

import (
	"context"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// DiscoverUpstreams lists the upstream backends a provider can route the
// given model to — the choices an aggregator surface exposes to the
// picker's upstream layer. Direct providers answer an empty list.
func (app *ProviderApp) DiscoverUpstreams(ctx context.Context, id, modelID string) ([]dservice.Upstream, error) {
	if app.catalog == nil {
		return nil, apperror.NewInternalError("provider discovery unavailable", nil)
	}
	prov, err := app.providers.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	return app.catalog.ListUpstreams(ctx, prov, modelID)
}
