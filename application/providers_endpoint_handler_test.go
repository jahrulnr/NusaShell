package application

import (
	"context"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// fakeRouteProvider implements both core.Provider and ModelEndpointsLister
// so the handler can exercise the type assertion path.
type fakeRouteProvider struct {
	routes []domain.ModelRoute
	err    error
	slug   string
	calls  int
}

func (f *fakeRouteProvider) Name() string { return "fake-route" }

func (f *fakeRouteProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return &core.Response{FinishReason: core.FinishReasonStop}, nil
}

func (f *fakeRouteProvider) Stream(context.Context, *core.Request) (core.Stream, error) {
	return &stubStream{}, nil
}

func (f *fakeRouteProvider) ListModelEndpoints(_ context.Context, canonicalSlug string) ([]domain.ModelRoute, error) {
	f.calls++
	f.slug = canonicalSlug
	if f.err != nil {
		return nil, f.err
	}
	return f.routes, nil
}

func newEndpointsTestApp(t *testing.T, factory ProviderFactory, creds CredentialStore, providers map[string]*domain.Provider) *App {
	t.Helper()
	if creds == nil {
		creds = &fakeVisionCredStore{creds: map[string]string{"p1": "key"}}
	}
	return &App{
		DataDir:     t.TempDir(),
		Providers:   &fakeProviderStore{items: providers},
		Credentials: creds,
		Factory:     factory,
	}
}

func TestHandleModelEndpoints(t *testing.T) {
	inputCost, outputCost := 1.5, 2.5
	routeProvider := &fakeRouteProvider{routes: []domain.ModelRoute{{Slug: "nebius/fp8", Name: "Nebius", InputCost: &inputCost, OutputCost: &outputCost}}}
	providers := map[string]*domain.Provider{
		"p1": {
			ID: "p1", Name: "OpenRouter", Enabled: true,
			Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
			Models: []domain.Model{{
				ID:            "meta-llama/llama-3.3-70b-instruct",
				CanonicalSlug: "meta-llama/llama-3.3-70b-instruct",
			}},
		},
		"p2": { // gateway tanpa canonical slug → home icon
			ID: "p2", Name: "Direct", Enabled: true,
			Driver: domain.ProviderDriverOpenAI, Kind: domain.ProviderResponses,
			Models: []domain.Model{{ID: "gpt-x"}},
		},
	}
	app := newEndpointsTestApp(t, func(_ context.Context, _ *domain.Provider, _ string) (core.Provider, error) {
		return routeProvider, nil
	}, nil, providers)

	// 1) Fetch miss → routes + cached=false; second call served from cache.
	res, rpcErr := app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "meta-llama/llama-3.3-70b-instruct"})
	if rpcErr != nil {
		t.Fatalf("handleModelEndpoints: %+v", rpcErr)
	}
	first := res.(contracts.ModelEndpointsResult)
	if first.Cached || len(first.Routes) != 1 || first.Routes[0].Slug != "nebius/fp8" {
		t.Fatalf("first result = %+v", first)
	}
	if first.Routes[0].InputCost == nil || *first.Routes[0].InputCost != inputCost || first.Routes[0].OutputCost == nil || *first.Routes[0].OutputCost != outputCost {
		t.Fatalf("first route pricing = %+v", first.Routes[0])
	}
	if routeProvider.calls != 1 || routeProvider.slug != "meta-llama/llama-3.3-70b-instruct" {
		t.Fatalf("lister calls = %d slug=%q", routeProvider.calls, routeProvider.slug)
	}

	res, rpcErr = app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "meta-llama/llama-3.3-70b-instruct"})
	if rpcErr != nil {
		t.Fatalf("cached handleModelEndpoints: %+v", rpcErr)
	}
	second := res.(contracts.ModelEndpointsResult)
	if !second.Cached || len(second.Routes) != 1 {
		t.Fatalf("cached result = %+v", second)
	}
	if routeProvider.calls != 1 {
		t.Fatalf("lister called %d times, want 1 (cache hit)", routeProvider.calls)
	}

	// 2) Model tanpa canonical slug → empty routes tanpa fetch.
	res, rpcErr = app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p2", ModelID: "gpt-x"})
	if rpcErr != nil {
		t.Fatalf("no-slug handleModelEndpoints: %+v", rpcErr)
	}
	if len(res.(contracts.ModelEndpointsResult).Routes) != 0 {
		t.Fatalf("no-slug result = %+v, want empty", res)
	}

	// 3) Validasi: id hilang / model tak dikenal.
	if _, rpcErr := app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "", ModelID: "x"}); rpcErr == nil {
		t.Fatal("missing provider_id must fail")
	}
	if _, rpcErr := app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "unknown"}); rpcErr == nil {
		t.Fatal("unknown model must fail")
	}
}

func TestHandleModelEndpointsFetchErrorSurfaces(t *testing.T) {
	routeProvider := &fakeRouteProvider{err: errNotFound}
	providers := map[string]*domain.Provider{
		"p1": {
			ID: "p1", Name: "OR", Enabled: true,
			Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
			Models: []domain.Model{{ID: "m", CanonicalSlug: "m"}},
		},
	}
	app := newEndpointsTestApp(t, func(_ context.Context, _ *domain.Provider, _ string) (core.Provider, error) {
		return routeProvider, nil
	}, nil, providers)
	_, rpcErr := app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "m"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeProvider {
		t.Fatalf("rpcErr = %+v, want CodeProvider", rpcErr)
	}
}
