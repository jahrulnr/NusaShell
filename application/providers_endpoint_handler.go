package application

import (
	"context"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// handleModelEndpoints returns the upstream providers that can serve a
// model on its gateway. Cache-first (TTL 24h, keyed per provider+model);
// on a miss it fetches from the gateway. Gateways without a route
// concept (non-OpenRouter) return an empty list immediately so the
// frontend shows the non-interactive home icon. A fetch error is
// surfaced so the UI can hint at account-level blocking.
func (a *App) handleModelEndpoints(req contracts.ModelEndpointsRequest) (any, *contracts.RPCError) {
	providerID := strings.TrimSpace(req.ProviderID)
	modelID := strings.TrimSpace(req.ModelID)
	if providerID == "" || modelID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "provider_id and model_id are required"}
	}
	p, err := a.Providers.Get(providerID)
	if err != nil || p == nil || !p.Enabled {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "provider is not available or not enabled"}
	}
	m := p.FindModel(modelID)
	if m == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "model is not available on provider"}
	}

	cache := a.endpoints()
	if routes, ok := cache.get(providerID, modelID); ok {
		return toEndpointsResult(routes, true, time.Now()), nil
	}
	if m.CanonicalSlug == "" {
		// No canonical slug: this gateway has no route concept for the
		// model (or the slug was never captured). Empty list = home icon.
		return toEndpointsResult(nil, false, time.Now()), nil
	}
	key, _, _ := a.Credentials.Get(providerID)
	adapter, err := a.Factory(context.Background(), p, key)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	lister, ok := adapter.(ModelEndpointsLister)
	if !ok {
		// Factory returned a provider without route enumeration; the
		// picker degrades to the home icon.
		return toEndpointsResult(nil, false, time.Now()), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	routes, err := lister.ListModelEndpoints(ctx, m.EndpointsSlug())
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	cache.set(providerID, modelID, routes)
	return toEndpointsResult(routes, false, time.Now()), nil
}

func toEndpointsResult(routes []domain.ModelRoute, cached bool, fetchedAt time.Time) contracts.ModelEndpointsResult {
	out := make([]contracts.ModelEndpointDTO, 0, len(routes))
	for _, r := range routes {
		out = append(out, contracts.ModelEndpointDTO{
			Slug:         r.Slug,
			Name:         r.Name,
			Quantization: r.Quantization,
			Status:       r.Status,
			Latency:      r.Latency,
			Throughput:   r.Throughput,
			InputCost:    r.InputCost,
			OutputCost:   r.OutputCost,
		})
	}
	return contracts.ModelEndpointsResult{
		Routes:    out,
		Cached:    cached,
		FetchedAt: fetchedAt.UTC().Format(time.RFC3339),
	}
}
