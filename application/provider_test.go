package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/modelcatalog"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- from providers_import_embedding_test.go ---

// fakeEmbeddingImportAdapter simulates an OpenAI-compatible /v1/models:
// every installed model (chat and embedding alike) is returned untagged.
type fakeEmbeddingImportAdapter struct {
	fakeVisionAdapter
}

func (f *fakeEmbeddingImportAdapter) ListModels(ctx context.Context, apiKey string) ([]domain.Model, error) {
	return []domain.Model{
		{ID: "nomic-embed-text:latest"},
		{ID: "gemma4:e2b"},
	}, nil
}

// fakeEmbLister stands in for the embedding model lister wired in
// production (infrastructure cannot be imported here without a dependency
// cycle).
type fakeEmbLister struct{ ids []string }

func (f fakeEmbLister) ListEmbeddingModels(ctx context.Context, apiKey string) ([]string, error) {
	return f.ids, nil
}

func TestHandleProvidersImportTagsEmbeddingsFromLister(t *testing.T) {
	app := &App{
		Logs:        &fakeLogStore{},
		Bus:         NewBus(),
		Providers:   &fakeProviderStore{items: map[string]*domain.Provider{}},
		Credentials: &fakeVisionCredStore{creds: map[string]string{}},
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
			return &fakeEmbeddingImportAdapter{}, nil
		},
		// Embedding model lister reports the embedding-capable ids.
		EmbeddingModelListerFactory: func(p *domain.Provider) EmbeddingModelLister {
			return fakeEmbLister{ids: []string{"nomic-embed-text:latest"}}
		},
	}
	app.Providers.Save(&domain.Provider{
		ID: "emb", Kind: domain.ProviderChat, Name: "Gateway",
		BaseURL: "https://gateway.example.com/v1", Enabled: true,
	})

	res, rpcErr := app.handleProvidersImport(contracts.ProviderIDRequest{ID: "emb"})
	if rpcErr != nil {
		t.Fatalf("handleProvidersImport: %v", rpcErr.Message)
	}
	kinds := map[string]string{}
	for _, m := range res.(contracts.ImportModelsResult).Models {
		kinds[m.ID] = m.Kind
	}
	if kinds["nomic-embed-text:latest"] != string(domain.ModelKindEmbedding) {
		t.Errorf("nomic-embed-text:latest kind = %q, want %q", kinds["nomic-embed-text:latest"], domain.ModelKindEmbedding)
	}
	if kinds["gemma4:e2b"] != "" && kinds["gemma4:e2b"] == string(domain.ModelKindEmbedding) {
		t.Errorf("chat model gemma4:e2b misclassified as embedding")
	}

	// The provider must have persisted the tagged models too.
	p, _ := app.Providers.Get("emb")
	if !p.HasModel("nomic-embed-text:latest") {
		t.Fatal("saved provider lost the embedding model")
	}
	for _, m := range p.Models {
		if m.ID == "nomic-embed-text:latest" && m.Kind != domain.ModelKindEmbedding {
			t.Errorf("persisted kind = %q, want embedding", m.Kind)
		}
	}
}

func TestHandleModelsListSurfacesLegacyEmbeddingModel(t *testing.T) {
	// A model imported before embedding detection existed is stored with an
	// empty Kind — the read path must still surface it in the picker.
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"emb": {ID: "emb", Kind: domain.ProviderChat, Name: "Gateway", Enabled: true,
				Models: []domain.Model{{ID: "nomic-embed-text:latest"}, {ID: "gemma4:e2b"}}},
		}},
	}
	res, rpcErr := app.handleModelsList()
	if rpcErr != nil {
		t.Fatalf("handleModelsList: %v", rpcErr.Message)
	}
	kinds := map[string]string{}
	for _, m := range res.(contracts.ModelsListResult).Models {
		kinds[m.ID] = m.Kind
	}
	if kinds["nomic-embed-text:latest"] != string(domain.ModelKindEmbedding) {
		t.Errorf("legacy nomic-embed-text:latest kind = %q, want %q", kinds["nomic-embed-text:latest"], domain.ModelKindEmbedding)
	}
	if kinds["gemma4:e2b"] == string(domain.ModelKindEmbedding) {
		t.Errorf("chat model gemma4:e2b must not appear as embedding")
	}
}

// --- from providers_endpoint_handler_test.go ---

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
	app := newEndpointsTestApp(t, func(_ context.Context, _ *domain.Provider, _ string) (AIProvider, error) {
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

func TestHandleModelEndpointsPreservesFreeVariant(t *testing.T) {
	routeProvider := &fakeRouteProvider{routes: []domain.ModelRoute{{Slug: "decart/fp4", Name: "Decart"}}}
	providers := map[string]*domain.Provider{
		"p1": {
			ID: "p1", Name: "OpenRouter", Enabled: true,
			Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
			Models: []domain.Model{{
				ID:            "z-ai/glm-5.2:free",
				CanonicalSlug: "z-ai/glm-5.2-20260616",
			}},
		},
	}
	app := newEndpointsTestApp(t, func(_ context.Context, _ *domain.Provider, _ string) (AIProvider, error) {
		return routeProvider, nil
	}, nil, providers)

	if _, rpcErr := app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "z-ai/glm-5.2:free"}); rpcErr != nil {
		t.Fatalf("handleModelEndpoints: %+v", rpcErr)
	}
	if routeProvider.slug != "z-ai/glm-5.2-20260616:free" {
		t.Fatalf("lister slug = %q, want canonical plus :free variant", routeProvider.slug)
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
	app := newEndpointsTestApp(t, func(_ context.Context, _ *domain.Provider, _ string) (AIProvider, error) {
		return routeProvider, nil
	}, nil, providers)
	_, rpcErr := app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "m"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeProvider {
		t.Fatalf("rpcErr = %+v, want CodeProvider", rpcErr)
	}
}

func TestHandleModelEndpointsSkipsHTTPClientError(t *testing.T) {
	routeProvider := &fakeRouteProvider{err: &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 404,
		Err:        fmt.Errorf("provider returned HTTP 404: <!DOCTYPE html><title>Not Found | opencode</title>"),
	}}
	providers := map[string]*domain.Provider{
		"p1": {
			ID: "p1", Name: "OpenCode", Enabled: true,
			Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
			Models: []domain.Model{{ID: "big-pickle", CanonicalSlug: "big-pickle"}},
		},
	}
	app := newEndpointsTestApp(t, func(_ context.Context, _ *domain.Provider, _ string) (AIProvider, error) {
		return routeProvider, nil
	}, nil, providers)

	res, rpcErr := app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "big-pickle"})
	if rpcErr != nil {
		t.Fatalf("4xx handleModelEndpoints: %+v", rpcErr)
	}
	first := res.(contracts.ModelEndpointsResult)
	if len(first.Routes) != 0 {
		t.Fatalf("4xx result = %+v, want empty routes", first)
	}
	if first.Cached {
		t.Fatalf("first 4xx result should not report cache hit")
	}

	res, rpcErr = app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "big-pickle"})
	if rpcErr != nil {
		t.Fatalf("cached 4xx handleModelEndpoints: %+v", rpcErr)
	}
	if !res.(contracts.ModelEndpointsResult).Cached {
		t.Fatal("second 4xx call should be served from cache")
	}
	if routeProvider.calls != 1 {
		t.Fatalf("lister called %d times, want 1 (4xx cached as empty)", routeProvider.calls)
	}
}

func TestHandleModelEndpointsSkipsHTTPServerErrorWithoutCache(t *testing.T) {
	routeProvider := &fakeRouteProvider{err: &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 503,
		Err:        fmt.Errorf("provider returned HTTP 503: <html>unavailable</html>"),
	}}
	providers := map[string]*domain.Provider{
		"p1": {
			ID: "p1", Name: "OR", Enabled: true,
			Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
			Models: []domain.Model{{ID: "m", CanonicalSlug: "m"}},
		},
	}
	app := newEndpointsTestApp(t, func(_ context.Context, _ *domain.Provider, _ string) (AIProvider, error) {
		return routeProvider, nil
	}, nil, providers)

	res, rpcErr := app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "m"})
	if rpcErr != nil {
		t.Fatalf("5xx handleModelEndpoints: %+v", rpcErr)
	}
	if len(res.(contracts.ModelEndpointsResult).Routes) != 0 {
		t.Fatalf("5xx result = %+v, want empty", res)
	}

	_, rpcErr = app.handleModelEndpoints(contracts.ModelEndpointsRequest{ProviderID: "p1", ModelID: "m"})
	if rpcErr != nil {
		t.Fatalf("second 5xx handleModelEndpoints: %+v", rpcErr)
	}
	if routeProvider.calls != 2 {
		t.Fatalf("lister called %d times, want 2 (5xx not cached)", routeProvider.calls)
	}
}

// --- from providers_cache_ttl_test.go ---

func strPtr(s string) *string { return &s }

func TestHandleProvidersSavePersistsCacheTTL(t *testing.T) {
	app, providers, _ := newSeedTestApp()

	res, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Kind:     "messages",
		Name:     "Anthropic",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
		CacheTTL: strPtr("1h"),
	})
	if rpcErr != nil {
		t.Fatalf("save: %+v", rpcErr)
	}
	out, ok := res.(contracts.ProvidersListResult)
	if !ok || len(out.Providers) != 1 {
		t.Fatalf("result = %#v", res)
	}
	if out.Providers[0].CacheTTL != "1h" {
		t.Errorf("dto cache_ttl = %q, want 1h", out.Providers[0].CacheTTL)
	}
	stored, err := providers.Get(out.Providers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheTTL != "1h" {
		t.Errorf("stored cache_ttl = %q, want 1h", stored.CacheTTL)
	}
}

func TestHandleProvidersSavePersistsCodexReasoningSummary(t *testing.T) {
	app, providers, _ := newSeedTestApp()
	res, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Driver: "codex", Kind: "codex", Name: "Codex",
		BaseURL: "https://chatgpt.com/backend-api/codex", Enabled: true,
		ReasoningSummary: strPtr("detailed"),
	})
	if rpcErr != nil {
		t.Fatalf("save: %+v", rpcErr)
	}
	out := res.(contracts.ProvidersListResult)
	if len(out.Providers) != 1 || out.Providers[0].ReasoningSummary != "detailed" {
		t.Fatalf("provider DTO = %#v", out.Providers)
	}
	if got := out.Providers[0].ReasoningSummaries; len(got) != 4 || got[0] != "auto" || got[2] != "detailed" {
		t.Fatalf("reasoning summaries = %v", got)
	}
	stored, err := providers.Get(out.Providers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReasoningSummary != "detailed" {
		t.Fatalf("stored reasoning summary = %q, want detailed", stored.ReasoningSummary)
	}
}

func TestHandleProvidersSaveRejectsInvalidCodexReasoningSummary(t *testing.T) {
	app, _, _ := newSeedTestApp()
	_, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Driver: "codex", Kind: "codex", Name: "Codex",
		BaseURL: "https://chatgpt.com/backend-api/codex", Enabled: true,
		ReasoningSummary: strPtr("verbose"),
	})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("invalid reasoning summary error = %#v", rpcErr)
	}
}

func TestHandleProvidersSaveAcceptsCodexDriver(t *testing.T) {
	app, providers, credentials := newSeedTestApp()

	res, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Driver:  "codex",
		Kind:    "codex",
		Name:    "Codex",
		BaseURL: "https://chatgpt.com/backend-api/codex",
		APIKey:  "oauth-access-token",
		Enabled: true,
	})
	if rpcErr != nil {
		t.Fatalf("save Codex: %+v", rpcErr)
	}
	out := res.(contracts.ProvidersListResult)
	if len(out.Providers) != 1 || out.Providers[0].Kind != "codex" || !out.Providers[0].Configured {
		t.Fatalf("Codex result = %#v, want configured codex provider", out)
	}
	stored, err := providers.Get(out.Providers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Driver != domain.ProviderDriverCodex || stored.Kind != domain.ProviderCodex {
		t.Fatalf("stored Codex provider = %+v", stored)
	}
	if got, _, err := credentials.Get(stored.ID); err != nil || got != "oauth-access-token" {
		t.Fatalf("stored Codex credential = %q, err=%v", got, err)
	}
}

func TestHandleProvidersSaveRejectsInvalidCacheTTL(t *testing.T) {
	app, _, _ := newSeedTestApp()
	_, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Kind:     "messages",
		Name:     "Anthropic",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
		CacheTTL: strPtr("30m"),
	})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("30m on messages must be VALIDATION_ERROR, got %+v", rpcErr)
	}
}

func TestHandleProvidersSavePreservesCacheTTLWhenOmitted(t *testing.T) {
	app, providers, _ := newSeedTestApp()
	created, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Kind:     "messages",
		Name:     "Anthropic",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
		CacheTTL: strPtr("1h"),
	})
	if rpcErr != nil {
		t.Fatalf("create: %+v", rpcErr)
	}
	id := created.(contracts.ProvidersListResult).Providers[0].ID

	_, rpcErr = app.handleProvidersSave(contracts.ProviderSaveRequest{
		ID:      id,
		Kind:    "messages",
		Name:    "Anthropic",
		BaseURL: "https://api.anthropic.com",
		Enabled: true,
	})
	if rpcErr != nil {
		t.Fatalf("update: %+v", rpcErr)
	}
	stored, err := providers.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheTTL != "1h" {
		t.Errorf("omitted cache_ttl wiped stored value: %q", stored.CacheTTL)
	}
}

func TestHandleProvidersSaveClearsInvalidTTLOnKindChange(t *testing.T) {
	app, providers, _ := newSeedTestApp()
	created, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Driver:   "openrouter",
		Kind:     "chat",
		Name:     "Gateway",
		BaseURL:  "https://openrouter.ai/api/v1",
		Enabled:  true,
		CacheTTL: strPtr("1h"),
	})
	if rpcErr != nil {
		t.Fatalf("create: %+v", rpcErr)
	}
	id := created.(contracts.ProvidersListResult).Providers[0].ID

	_, rpcErr = app.handleProvidersSave(contracts.ProviderSaveRequest{
		ID:      id,
		Driver:  "openrouter",
		Kind:    "responses",
		Name:    "Gateway",
		BaseURL: "https://openrouter.ai/api/v1",
		Enabled: true,
	})
	if rpcErr != nil {
		t.Fatalf("kind change: %+v", rpcErr)
	}
	stored, err := providers.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheTTL != "" {
		t.Errorf("1h must be cleared when kind becomes responses, got %q", stored.CacheTTL)
	}
	dto := app.providerDTO(stored)
	if dto.CacheTTL != "30m" {
		t.Errorf("effective dto cache_ttl = %q, want 30m", dto.CacheTTL)
	}
}

func TestProviderDTOAdvertisesSendableCacheTTLs(t *testing.T) {
	app, _, _ := newSeedTestApp()
	or := &domain.Provider{
		ID: "openrouter", Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
		Name: "OpenRouter", Enabled: true, BaseURL: "https://openrouter.ai/api/v1",
	}
	dto := app.providerDTO(or)
	if got := dto.CacheTTLs; len(got) != 3 || got[0] != "5m" || got[1] != "1h" || got[2] != domain.CacheTTLOff {
		t.Errorf("openrouter chat cache_ttls = %v, want [5m 1h off]", dto.CacheTTLs)
	}
	if dto.CacheTTL != "5m" {
		t.Errorf("openrouter default cache_ttl = %q, want 5m", dto.CacheTTL)
	}

	oa := &domain.Provider{
		ID: "openai", Driver: domain.ProviderDriverOpenAI, Kind: domain.ProviderResponses,
		Name: "OpenAI", Enabled: true,
	}
	dto = app.providerDTO(oa)
	if got := dto.CacheTTLs; len(got) != 2 || got[0] != "30m" || got[1] != domain.CacheTTLOff {
		t.Errorf("openai cache_ttls = %v, want [30m off]", dto.CacheTTLs)
	}
	if dto.CacheTTL != "30m" {
		t.Errorf("openai default cache_ttl = %q, want 30m", dto.CacheTTL)
	}

	oc := &domain.Provider{
		ID: "prov_oc", Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
		Name: "OpenCode", Enabled: true, BaseURL: "https://opencode.ai/zen/go/v1", CacheTTL: "1h",
	}
	dto = app.providerDTO(oc)
	if got := dto.CacheTTLs; len(got) != 3 || got[0] != "5m" || got[1] != "1h" || got[2] != domain.CacheTTLOff {
		t.Errorf("opencode cache_ttls = %v, want [5m 1h off]", dto.CacheTTLs)
	}
	if dto.CacheTTL != "1h" {
		t.Errorf("opencode effective cache_ttl = %q, want 1h", dto.CacheTTL)
	}
}

func TestHandleProvidersSavePersistsCacheTTLOff(t *testing.T) {
	app, providers, _ := newSeedTestApp()
	res, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Kind:     "messages",
		Name:     "Anthropic",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
		CacheTTL: strPtr(domain.CacheTTLOff),
	})
	if rpcErr != nil {
		t.Fatalf("save: %+v", rpcErr)
	}
	out := res.(contracts.ProvidersListResult)
	if out.Providers[0].CacheTTL != domain.CacheTTLOff {
		t.Errorf("dto cache_ttl = %q, want off", out.Providers[0].CacheTTL)
	}
	stored, err := providers.Get(out.Providers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheTTL != domain.CacheTTLOff {
		t.Errorf("stored cache_ttl = %q, want off", stored.CacheTTL)
	}
}

// --- from rate_limit_test.go ---

// The observed OpenAI TPM rejection body (delivered either as an in-stream
// SSE error event on the Responses API or as an HTTP 429 on Chat
// Completions). Sanitized org id.
const tpmOverflowBody = "openai: stream error: Request too large for gpt-5.6-luna in organization org-test on tokens per min (TPM): Limit 200000, Requested 333331. The input or output tokens must be reduced in order to run successfully. Visit https://platform.openai.com/account/rate-limits to learn more."

func TestMarkProviderRateLimitedAndWait(t *testing.T) {
	a := &App{}
	if w := a.ProviderRateLimitWait("tok"); w != 0 {
		t.Fatalf("fresh provider wait = %v, want 0", w)
	}
	a.MarkProviderRateLimited("tok", time.Now().Add(30*time.Second))
	w := a.ProviderRateLimitWait("tok")
	if w <= 0 || w > 31*time.Second {
		t.Fatalf("wait = %v, want ~30s", w)
	}
	// Expired window clears.
	a.MarkProviderRateLimited("tok", time.Now().Add(-time.Second))
	if w := a.ProviderRateLimitWait("tok"); w != 0 {
		t.Fatalf("expired wait = %v, want 0", w)
	}
}

func TestMarkProviderRateLimitedDefaultsToOneMinute(t *testing.T) {
	a := &App{}
	a.MarkProviderRateLimited("tok", time.Time{})
	w := a.ProviderRateLimitWait("tok")
	if w <= 55*time.Second || w > 61*time.Second {
		t.Fatalf("default wait = %v, want ~1min", w)
	}
}

func TestDecorateRateLimitError(t *testing.T) {
	a := &App{}
	up := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, Err: errors.New("rate limit")}
	err := a.decorateRateLimitError("tok", up)
	if err == nil {
		t.Fatal("nil error")
	}
	if !strings.Contains(err.Error(), "rate-limited") || !strings.Contains(err.Error(), "try again") {
		t.Fatalf("friendly message missing: %q", err.Error())
	}
	if w := a.ProviderRateLimitWait("tok"); w <= 0 {
		t.Fatalf("rate-limit window not recorded, wait=%v", w)
	}
}

func TestDecorateRateLimitErrorNon429Untouched(t *testing.T) {
	a := &App{}
	inner := errors.New("boom")
	up := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 500, Err: inner}
	err := a.decorateRateLimitError("tok", up)
	if !errors.Is(err, inner) {
		t.Fatalf("non-429 error must pass through, got %v", err)
	}
	if w := a.ProviderRateLimitWait("tok"); w != 0 {
		t.Fatalf("non-429 must not record window, wait=%v", w)
	}
}

// TestDecorateRateLimitErrorTPMMessage verifies that a tokens-per-minute 429
// gets a token-accurate friendly message instead of the requests-per-minute
// one. Telling the user "max ~5 requests/min, wait and retry" is wrong for
// TPM: the request itself is too large and waiting changes nothing.
func TestDecorateRateLimitErrorTPMMessage(t *testing.T) {
	a := &App{}
	up := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, Err: errors.New(tpmOverflowBody)}
	err := a.decorateRateLimitError("tok", up)
	if err == nil {
		t.Fatal("nil error")
	}
	msg := err.Error()
	if strings.Contains(msg, "requests/min") {
		t.Fatalf("TPM 429 must not render the RPM message: %q", msg)
	}
	if !strings.Contains(msg, "tokens") || !strings.Contains(msg, "200000") || !strings.Contains(msg, "333331") {
		t.Fatalf("TPM message must name the token numbers: %q", msg)
	}
	if w := a.ProviderRateLimitWait("tok"); w <= 0 {
		t.Fatalf("TPM 429 must still record the window, wait=%v", w)
	}
}

// --- from slowdown_test.go ---

// mutexSettingsStore is a thread-safe in-memory SettingsStore for tests that
// mutate settings while a wait is in flight (the race detector would flag
// unsynchronized reads/writes otherwise).
type mutexSettingsStore struct {
	mu sync.Mutex
	s  domain.Settings
}

func (m *mutexSettingsStore) Get() domain.Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.s
}

func (m *mutexSettingsStore) Set(s domain.Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s = s
	return nil
}

func TestWaitSlowDownDisabledReturnsImmediately(t *testing.T) {
	app := &App{Settings: &mutexSettingsStore{s: domain.DefaultSettings()}} // slow_down = 0
	start := time.Now()
	app.waitSlowDown(context.Background())
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("wait with slow_down=0 took %v, want immediate return", elapsed)
	}
}

func TestWaitSlowDownAppliesDelay(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.SlowDown = 1
	app := &App{Settings: &mutexSettingsStore{s: settings}}
	start := time.Now()
	app.waitSlowDown(context.Background())
	if elapsed := time.Since(start); elapsed < 850*time.Millisecond {
		t.Fatalf("wait with slow_down=1 took %v, want ~1s", elapsed)
	}
}

// TestWaitSlowDownAbortsOnCancel: user stop / conversation switch / server
// shutdown must cut the wait short instead of blocking the turn.
func TestWaitSlowDownAbortsOnCancel(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.SlowDown = 5
	app := &App{Settings: &mutexSettingsStore{s: settings}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(120 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	app.waitSlowDown(ctx)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ctx cancel did not cut the wait short: took %v, want < 2s", elapsed)
	}
}

// TestWaitSlowDownClearedMidWait is the live-update contract: saving 0 (or a
// lower value) from the Settings UI while a conversation is mid-delay must
// take effect immediately — no stop, no idle turn, no restart.
func TestWaitSlowDownClearedMidWait(t *testing.T) {
	store := &mutexSettingsStore{s: domain.DefaultSettings()}
	store.s.SlowDown = 5
	app := &App{Settings: store}
	go func() {
		time.Sleep(120 * time.Millisecond)
		s := store.Get()
		s.SlowDown = 0
		_ = store.Set(s)
	}()
	start := time.Now()
	app.waitSlowDown(context.Background())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("clearing slow_down mid-wait did not cut it short: took %v, want < 2s", elapsed)
	}
}

// TestWaitSlowDownLoweredMidWait: a reduced value shrinks the remaining wait
// to the newly configured duration (5s → 1s must end after ~1s total, not 5s).
func TestWaitSlowDownLoweredMidWait(t *testing.T) {
	store := &mutexSettingsStore{s: domain.DefaultSettings()}
	store.s.SlowDown = 5
	app := &App{Settings: store}
	go func() {
		time.Sleep(120 * time.Millisecond)
		s := store.Get()
		s.SlowDown = 1
		_ = store.Set(s)
	}()
	start := time.Now()
	app.waitSlowDown(context.Background())
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("lowering slow_down mid-wait did not shrink it: took %v, want < 3s", elapsed)
	}
}

// TestEngineBeforeRoundAppliesSlowDownBetweenRounds: two rounds with
// slow_down=1 must take ~2s wall-clock with both streams still firing — the
// delay slots into the round boundary, never blocks or skips the provider
// call itself.
func TestEngineBeforeRoundAppliesSlowDownBetweenRounds(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.SlowDown = 1
	app := &App{Settings: &mutexSettingsStore{s: settings}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var streams []time.Time
	rules := AgentRules{
		BeforeRound: func(st *RoundState) error {
			app.waitSlowDown(ctx)
			return nil
		},
		Stream: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			streams = append(streams, time.Now())
			return ChatResponse{Content: "round"}, nil
		},
		Terminal: func(st *RoundState, resp ChatResponse) bool { return st.Round >= 1 },
		Execute: func(st *RoundState, resp ChatResponse, calls []domain.ToolCall) ([]ToolOutcome, error) {
			return nil, nil
		},
	}
	start := time.Now()
	if _, err := (&AgentEngine{}).Run(ctx, rules, 0); err != nil {
		t.Fatalf("engine run: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2 (delay must not swallow rounds)", len(streams))
	}
	if elapsed := time.Since(start); elapsed < 1850*time.Millisecond {
		t.Fatalf("two rounds with slow_down=1 took %v, want >= ~2s", elapsed)
	}
}

// --- from env_providers_test.go ---

func newSeedTestApp() (*App, *fakeProviderStore, *memCreds) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{}}
	creds := &memCreds{m: map[string]string{}}
	app := &App{
		Providers:   providers,
		Credentials: creds,
		Logs:        &fakeLogStore{},
		Bus:         NewBus(),
	}
	return app, providers, creds
}

func TestSeedProvidersFromEnvCreatesProvider(t *testing.T) {
	app, providers, creds := newSeedTestApp()

	actions := app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-or-test"}))

	if len(actions) != 1 {
		t.Fatalf("expected 1 action line, got %v", actions)
	}

	p, err := providers.Get("openrouter")
	if err != nil {
		t.Fatalf("expected seeded provider, got error: %v", err)
	}
	if p.Kind != domain.ProviderChat {
		t.Errorf("kind = %q, want chat", p.Kind)
	}
	if p.Name != "OpenRouter" {
		t.Errorf("name = %q, want OpenRouter", p.Name)
	}
	if p.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("base url = %q", p.BaseURL)
	}
	if !p.Enabled {
		t.Error("seeded provider should be enabled")
	}
	if !p.HasAPIKey {
		t.Error("seeded provider should report HasAPIKey")
	}
	key, has, _ := creds.Get("openrouter")
	if !has || key != "sk-or-test" {
		t.Errorf("credential = %q (has=%v), want sk-or-test", key, has)
	}
}

func TestSeedProvidersFromEnvSkipsWhenUnset(t *testing.T) {
	app, providers, _ := newSeedTestApp()

	app.SeedProvidersFromEnv(mapEnv(map[string]string{}))

	if len(providers.List()) != 0 {
		t.Fatalf("no provider should be seeded without env, got %d", len(providers.List()))
	}
}

func TestSeedProvidersFromEnvSkipsBlankValue(t *testing.T) {
	app, providers, _ := newSeedTestApp()

	app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "   "}))

	if len(providers.List()) != 0 {
		t.Fatalf("blank env value must not seed a provider, got %d", len(providers.List()))
	}
}

func TestSeedProvidersFromEnvIsIdempotent(t *testing.T) {
	app, providers, creds := newSeedTestApp()
	env := mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-or-test"})

	app.SeedProvidersFromEnv(env)
	second := app.SeedProvidersFromEnv(env)

	if len(second) != 0 {
		t.Fatalf("second run with an unchanged key must report no action, got %v", second)
	}
	if n := len(providers.List()); n != 1 {
		t.Fatalf("running twice must not duplicate providers, got %d", n)
	}
	key, _, _ := creds.Get("openrouter")
	if key != "sk-or-test" {
		t.Errorf("credential = %q, want sk-or-test", key)
	}
}

func TestSeedProvidersFromEnvRefreshesRotatedKey(t *testing.T) {
	app, providers, creds := newSeedTestApp()
	app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-old"}))

	actions := app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-new"}))

	if len(actions) != 1 {
		t.Fatalf("rotation should report one refresh action, got %v", actions)
	}
	key, _, _ := creds.Get("openrouter")
	if key != "sk-new" {
		t.Errorf("credential = %q, want sk-new (rotated)", key)
	}
	if n := len(providers.List()); n != 1 {
		t.Fatalf("rotation must not duplicate providers, got %d", n)
	}
}

func TestSeedProvidersFromEnvPreservesUserEdits(t *testing.T) {
	app, providers, creds := newSeedTestApp()
	// A user who renamed the provider, pointed it at a custom gateway, and
	// disabled it — with the same key the env would supply.
	providers.Save(&domain.Provider{
		ID:      "openrouter",
		Kind:    domain.ProviderChat,
		Name:    "My Router",
		BaseURL: "https://gateway.internal/v1",
		Enabled: false,
	})
	creds.Set("openrouter", "sk-or-test")

	app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-or-test"}))

	p, _ := providers.Get("openrouter")
	if p.Name != "My Router" || p.BaseURL != "https://gateway.internal/v1" || p.Enabled {
		t.Fatalf("seeder overwrote user edits: %+v", p)
	}
}

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// --- from resolve_model_test.go ---

type fakeCreds struct {
	keys map[string]string
}

func (c *fakeCreds) Get(id string) (string, bool, error) {
	k, ok := c.keys[id]
	return k, ok, nil
}
func (c *fakeCreds) Set(id, key string) error                { return nil }
func (c *fakeCreds) Delete(id string) error                  { return nil }
func (c *fakeCreds) ListByPrefix(p string) ([]string, error) { return nil, nil }

func TestSplitQualifiedModel(t *testing.T) {
	tests := []struct {
		input        string
		wantProvider string
		wantModel    string
		wantOk       bool
	}{
		{"tokenrouter:deepseek-v4-flash", "tokenrouter", "deepseek-v4-flash", true},
		{"openrouter:gpt-4o", "openrouter", "gpt-4o", true},
		{"gw:nomic-embed-text:latest", "gw", "nomic-embed-text:latest", true},
		{"deepseek-v4-flash", "", "", false},
		{"", "", "", false},
		{":model-only", "", "", false}, // empty provider is invalid
	}
	for _, tt := range tests {
		gotProvider, gotModel, gotOk := domain.SplitQualifiedModel(tt.input)
		if gotProvider != tt.wantProvider || gotModel != tt.wantModel || gotOk != tt.wantOk {
			t.Errorf("splitQualifiedModel(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, gotProvider, gotModel, gotOk, tt.wantProvider, tt.wantModel, tt.wantOk)
		}
	}
}

func TestResolveModelQualified(t *testing.T) {
	deepseekModel := domain.Model{ID: "deepseek-v4-flash", MaxOutput: 1048576}
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"openrouter":  {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1", Models: []domain.Model{deepseekModel}},
		"tokenrouter": {ID: "tokenrouter", Enabled: true, Kind: domain.ProviderResponses, BaseURL: "https://api.tokenrouter.io/v1", Models: []domain.Model{deepseekModel}},
	}}
	creds := &fakeCreds{keys: map[string]string{
		"openrouter":  "or-key",
		"tokenrouter": "tr-key",
	}}
	app := &App{Providers: providers, Credentials: creds}

	// Qualified: should pick tokenrouter, not openrouter
	p, _, key, err := app.resolveModelWithMeta("tokenrouter:deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID != "tokenrouter" {
		t.Errorf("qualified model should resolve to tokenrouter, got %q", p.ID)
	}
	if key != "tr-key" {
		t.Errorf("should use tokenrouter API key, got %q", key)
	}

	// Qualified: should pick openrouter
	p, _, key, err = app.resolveModelWithMeta("openrouter:deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID != "openrouter" {
		t.Errorf("qualified model should resolve to openrouter, got %q", p.ID)
	}
	if key != "or-key" {
		t.Errorf("should use openrouter API key, got %q", key)
	}

	// Unqualified: backward compat — first match
	p, _, _, err = app.resolveModelWithMeta("deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID == "" {
		t.Error("unqualified model should still resolve via first-match")
	}
}

func TestResolveModelAllowsMissingAPIKey(t *testing.T) {
	model := domain.Model{ID: "local-model"}
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"opencode": {
			ID: "opencode", Name: "OpenCode", Enabled: true,
			Kind: domain.ProviderMessages, Driver: domain.ProviderDriverOpenRouter,
			BaseURL: "http://127.0.0.1:4096", Models: []domain.Model{model},
		},
		"local-responses": {
			ID: "local-responses", Name: "Local Responses", Enabled: true,
			Kind: domain.ProviderResponses, Driver: domain.ProviderDriverOpenRouter,
			BaseURL: "http://127.0.0.1:8080/v1", Models: []domain.Model{model},
		},
	}}
	app := &App{Providers: providers, Credentials: &fakeCreds{keys: map[string]string{}}}

	for _, id := range []string{"opencode:local-model", "local-responses:local-model"} {
		p, _, key, err := app.resolveModelWithMeta(id)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", id, err)
		}
		if p == nil {
			t.Fatalf("%s: expected provider", id)
		}
		if key != "" {
			t.Errorf("%s: key = %q, want empty", id, key)
		}
	}
}

func TestResolveModelQualifiedNotFound(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{{ID: "gpt-4o"}}},
	}}
	creds := &fakeCreds{keys: map[string]string{"openrouter": "or-key"}}
	app := &App{Providers: providers, Credentials: creds}

	// Provider exists but doesn't have the model
	_, _, _, err := app.resolveModelWithMeta("openrouter:nonexistent")
	if err == nil {
		t.Error("expected error for model not on provider")
	}

	// Provider doesn't exist
	_, _, _, err = app.resolveModelWithMeta("ghost:deepseek-v4-flash")
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}

func TestResolveModelWithMetaAppliesLearnedOverrides(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"tokenrouter": {ID: "tokenrouter", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{{
			ID:      "qwen/qwen3.8-max-free",
			Context: 1_000_000,
			Vision:  true,
		}}},
	}}
	creds := &fakeCreds{keys: map[string]string{"tokenrouter": "tr-key"}}
	store := &fakeLearnedParamStore{}
	cache := learnedparams.New(store)
	cache.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`Requested token count exceeds the model's maximum context length of 262144 tokens.`)
	cache.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part`)

	app := &App{Providers: providers, Credentials: creds, learnedParams: cache}
	_, m, _, err := app.resolveModelWithMeta("tokenrouter:qwen/qwen3.8-max-free")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected model metadata")
	}
	if m.Context != 262144 {
		t.Errorf("learned cap should set Context to 262144, got %d", m.Context)
	}
	if m.Vision {
		t.Error("learned text-only should set Vision=false")
	}
}

// --- from model_overrides_test.go ---

// fakeModelOverrideStore is an in-memory ModelOverrideStore for testing.
type fakeModelOverrideStore struct {
	registry *domain.ModelOverrideRegistry
	saves    int
}

func (f *fakeModelOverrideStore) Load() *domain.ModelOverrideRegistry {
	if f.registry == nil {
		return domain.NewModelOverrideRegistry()
	}
	return f.registry
}

func (f *fakeModelOverrideStore) Save(r *domain.ModelOverrideRegistry) error {
	f.saves++
	f.registry = r
	return nil
}

func boolP(b bool) *bool { return &b }
func intP(i int) *int    { return &i }

// TestResolveModelWithMetaManualOverrideWins proves that a manual override
// applied at resolve time beats both the catalog value and a learned 400
// adaptation for the same field.
func TestResolveModelWithMetaManualOverrideWins(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"tokenrouter": {ID: "tokenrouter", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{{
			ID:      "qwen/qwen3.8-max-free",
			Context: 1_000_000,
			Vision:  true,
		}}},
	}}
	creds := &fakeCreds{keys: map[string]string{"tokenrouter": "tr-key"}}

	// Learned: cap context to 262144 and disable vision (text-only).
	learned := learnedparams.New(&fakeLearnedParamStore{})
	learned.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`Requested token count exceeds the model's maximum context length of 262144 tokens.`)
	learned.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part`)

	// Manual: assert context=1000000 and vision=true — must win.
	manual := modeloverrides.New(&fakeModelOverrideStore{})
	if err := manual.Set(&domain.ModelOverride{
		Provider: "tokenrouter", Model: "qwen/qwen3.8-max-free",
		Context: intP(1000000), Vision: boolP(true),
	}); err != nil {
		t.Fatalf("manual Set: %v", err)
	}

	app := &App{Providers: providers, Credentials: creds, learnedParams: learned, modelOverrides: manual}
	_, m, _, err := app.resolveModelWithMeta("tokenrouter:qwen/qwen3.8-max-free")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected model metadata")
	}
	if m.Context != 1000000 {
		t.Errorf("manual override must win: Context = %d, want 1000000", m.Context)
	}
	if !m.Vision {
		t.Error("manual override must win: Vision should be true")
	}
}

// TestResolveModelWithMetaManualOverrideOnly proves a manual override applies
// cleanly when there is no learned adaptation.
func TestResolveModelWithMetaManualOverrideOnly(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{{
			ID:      "deepseek/deepseek-v4-flash",
			Context: 200000,
			Vision:  true,
		}}},
	}}
	creds := &fakeCreds{keys: map[string]string{"openrouter": "or-key"}}

	manual := modeloverrides.New(&fakeModelOverrideStore{})
	if err := manual.Set(&domain.ModelOverride{
		Provider: "openrouter", Model: "deepseek/deepseek-v4-flash",
		Vision: boolP(false),
	}); err != nil {
		t.Fatalf("manual Set: %v", err)
	}

	app := &App{Providers: providers, Credentials: creds, modelOverrides: manual}
	_, m, _, err := app.resolveModelWithMeta("openrouter:deepseek/deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Vision {
		t.Error("manual Vision=false should disable vision")
	}
	if m.Context != 200000 {
		t.Errorf("untouched Context changed: %d", m.Context)
	}
}

// TestModelCapabilitiesManualOverrideWins proves the capabilities path also
// honors manual overrides over learned disabled modalities.
func TestModelCapabilitiesManualOverrideWins(t *testing.T) {
	learned := learnedparams.New(&fakeLearnedParamStore{})
	learned.LearnFrom400("openrouter", "qwen3.8-max-free", `text-only`)

	manual := modeloverrides.New(&fakeModelOverrideStore{})
	if err := manual.Set(&domain.ModelOverride{
		Provider: "openrouter", Model: "qwen3.8-max-free",
		Vision: boolP(true),
	}); err != nil {
		t.Fatalf("manual Set: %v", err)
	}

	provider := &domain.Provider{ID: "openrouter", Models: nil}
	caps := modelCapabilitiesWithLearned(provider, "qwen3.8-max-free", learned, manual)
	if !caps.Vision {
		t.Error("manual Vision=true must win over learned text-only")
	}
}

// TestResolveContextWindowManualOverrideWins proves the context-window path
// honors a manual context override over the learned cap.
func TestResolveContextWindowManualOverrideWins(t *testing.T) {
	learned := learnedparams.New(&fakeLearnedParamStore{})
	learned.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`maximum context length of 262144 tokens.`)

	manual := modeloverrides.New(&fakeModelOverrideStore{})
	if err := manual.Set(&domain.ModelOverride{
		Provider: "tokenrouter", Model: "qwen/qwen3.8-max-free",
		Context: intP(1000000),
	}); err != nil {
		t.Fatalf("manual Set: %v", err)
	}

	provider := &domain.Provider{ID: "tokenrouter", Models: []domain.Model{{
		ID: "qwen/qwen3.8-max-free", Context: 1000000,
	}}}
	app := &App{learnedParams: learned, modelOverrides: manual}
	settings := domain.DefaultSettings()
	if got := app.resolveContextWindow(provider, "qwen/qwen3.8-max-free", settings); got != 1000000 {
		t.Errorf("manual context override must win: got %d, want 1000000", got)
	}
}

// --- from catalog_invariant_test.go ---

func TestCatalogHintIsNotVendorHardcoded(t *testing.T) {
	// Any chat provider (TokenRouter, OpenRouter, a future gateway) must
	// NOT be special-cased in code: the hint comes from the model ID
	// prefix, which is output of the /models API. This is the regression
	// test for the "every new provider needs a code edit" bug.
	if got := catalogHintFromModelID("deepseek/deepseek-v4-flash"); got != "deepseek" {
		t.Fatalf("dynamic hint = %q, want deepseek", got)
	}
}

func TestCatalogHintFromModelIDIsDynamic(t *testing.T) {
	cases := []struct {
		id, want string
	}{
		{"deepseek/deepseek-v4-flash", "deepseek"},
		{"qwen/qwen3.8-max", "qwen"},
		{"openai/gpt-5.5", "openai"},
		{"anthropic/claude-sonnet-5", "anthropic"},
		{"MiniMax-M3", ""},
		{"grok-4.6", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := catalogHintFromModelID(c.id); got != c.want {
			t.Fatalf("catalogHintFromModelID(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}

// TestCatalogNeverWritesModels proves the catalog public surface has no
// writer: it only exposes Lookup/EnrichAll that return metadata pointers,
// never a method that inserts or renames models in a provider list.
func TestCatalogNeverWritesModels(t *testing.T) {
	_ = modelcatalog.New(nil) // catalog has no Add/Update/Delete — /models is the only writer
}

// TestEnrichDoesNotChangeKind proves enrichment never reclassifies a model:
// even when the catalog metadata kind differs ("image"), a chat-listed model
// keeps its kind from the lister.
func TestEnrichDoesNotChangeKind(t *testing.T) {
	app := &App{}
	app.ModelCatalog = &modelCatalogStub{}
	p := &domain.Provider{
		Kind:    domain.ProviderChat,
		BaseURL: "https://api.tokenrouter.com/v1",
		Models: []domain.Model{
			{ID: "deepseek/deepseek-v4-flash", Kind: domain.ModelKindChat},
		},
	}
	app.enrichProviderModelsAtRead(p)
	if p.Models[0].Kind != domain.ModelKindChat {
		t.Fatalf("catalog must not reclassify: got kind %q", p.Models[0].Kind)
	}
	if p.Models[0].Context == 0 {
		t.Fatalf("capability (context) was not assigned by the catalog")
	}
}

// modelCatalogStub is a minimal read-only catalog for tests. Its Lookup
// returns a capability with a deliberately wrong kind to prove the
// enricher never propagates kind.
type modelCatalogStub struct{}

func (s *modelCatalogStub) EnsureLoaded(ctx context.Context) error { return nil }
func (s *modelCatalogStub) Loaded() bool                           { return true }
func (s *modelCatalogStub) Lookup(providerHint, modelID string) *modelcatalog.ModelMetadata {
	if !strings.HasSuffix(modelID, "v4-flash") {
		return nil
	}
	return &modelcatalog.ModelMetadata{Context: 1000000, Kind: "image"} // wrong kind — must NOT propagate
}

// --- from provider_retry_test.go ---

func TestProviderRetryDelayHonorsRetryAfter(t *testing.T) {
	delay, retryable := providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: 3 * time.Second,
		Err:        errors.New("rate limited"),
	}, 1)
	if !retryable {
		t.Fatal("rate limit must be retryable")
	}
	if delay < 3*time.Second {
		t.Fatalf("retry delay = %s, want at least Retry-After", delay)
	}
}

func TestIsRetryableProviderError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{name: "request timeout", err: &domain.ProviderError{StatusCode: 408}, retryable: true},
		{name: "conflict", err: &domain.ProviderError{StatusCode: 409}, retryable: true},
		{name: "too early", err: &domain.ProviderError{StatusCode: 425}, retryable: true},
		{name: "rate limited with Retry-After", err: &domain.ProviderError{StatusCode: 429, RetryAfter: 3 * time.Second}, retryable: true},
		{name: "rate limited without Retry-After", err: &domain.ProviderError{StatusCode: 429}, retryable: false},
		{name: "server error", err: &domain.ProviderError{StatusCode: 503}, retryable: true},
		{name: "temporary transport error", err: &domain.ProviderError{Temporary: true}, retryable: true},
		{name: "invalid request", err: &domain.ProviderError{StatusCode: 400}, retryable: false},
		{name: "unauthorized", err: &domain.ProviderError{StatusCode: 401}, retryable: false},
		{name: "forbidden", err: &domain.ProviderError{StatusCode: 403}, retryable: false},
		{name: "not found", err: &domain.ProviderError{StatusCode: 404}, retryable: false},
		{name: "cancelled", err: &domain.ProviderError{Temporary: true, Err: context.Canceled}, retryable: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableProviderError(tt.err); got != tt.retryable {
				t.Fatalf("isRetryableProviderError(%v) = %t, want %t", tt.err, got, tt.retryable)
			}
		})
	}
}

// TestProviderRetryDelayRejectsLongRetryAfter verifies that a 429 with a
// Retry-After exceeding the cutoff (e.g. OpenRouter proxying an upstream with
// an 81-hour rate-limit reset) is NOT retried — the turn should fail fast so
// the user sees the error instead of waiting hours inside a retry sleep.
func TestProviderRetryDelayRejectsLongRetryAfter(t *testing.T) {
	delay, retryable := providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: 81 * time.Hour,
		Err:        errors.New("rate limited (reset after 81h 38m 41s)"),
	}, 1)
	if retryable {
		t.Fatalf("expected retryable=false for RetryAfter=81h, got delay=%s", delay)
	}
	if delay != 0 {
		t.Fatalf("expected delay=0 for non-retryable, got %s", delay)
	}
}

// TestProviderRetryDelayAcceptsShortRetryAfter verifies that a 429 with a
// Retry-After within the cutoff is still retried with the provider's delay.
func TestProviderRetryDelayAcceptsShortRetryAfter(t *testing.T) {
	delay, retryable := providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: 30 * time.Second,
		Err:        errors.New("rate limited"),
	}, 1)
	if !retryable {
		t.Fatal("expected retryable=true for RetryAfter=30s (within cutoff)")
	}
	if delay < 30*time.Second {
		t.Fatalf("retry delay = %s, want at least Retry-After (30s)", delay)
	}
}

// TestProviderRetryDelayAtCutoffBoundary verifies the boundary behavior:
// exactly at the cutoff is retryable, just above is not.
func TestProviderRetryDelayAtCutoffBoundary(t *testing.T) {
	// Exactly at cutoff — retryable
	_, retryable := providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: domain.RetryAfterCutoff,
		Err:        errors.New("rate limited"),
	}, 1)
	if !retryable {
		t.Fatalf("expected retryable=true at cutoff (%s)", domain.RetryAfterCutoff)
	}

	// Just above cutoff — not retryable
	_, retryable = providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: domain.RetryAfterCutoff + 1*time.Second,
		Err:        errors.New("rate limited"),
	}, 1)
	if retryable {
		t.Fatalf("expected retryable=false just above cutoff (%s+1s)", domain.RetryAfterCutoff)
	}
}

// TestDescribeProviderError verifies that the retry log helper surfaces
// ProviderError metadata (status code, Retry-After) alongside the underlying
// message, and falls back to the plain error string for non-Provider errors.
// This is what lets operators tell a 429 rate limit from a mid-stream EOF in
// the retry log line.
func TestDescribeProviderError(t *testing.T) {
	t.Run("non-upstream passthrough", func(t *testing.T) {
		src := errors.New("boom")
		if got := describeProviderError(src); got != "boom" {
			t.Fatalf("describeProviderError(non-upstream) = %q, want %q", got, "boom")
		}
	})

	t.Run("bare temporary EOF", func(t *testing.T) {
		err := &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: io.ErrUnexpectedEOF}
		got := describeProviderError(err)
		if !strings.Contains(got, "unexpected EOF") {
			t.Fatalf("describeProviderError must include underlying message, got %q", got)
		}
		if !strings.Contains(got, "kind=sse_transport") {
			t.Fatalf("describeProviderError must include kind, got %q", got)
		}
		if strings.Contains(got, "status=") || strings.Contains(got, "retry_after=") {
			t.Fatalf("bare temporary error must not invent metadata, got %q", got)
		}
	})

	t.Run("rate limit with status and retry-after", func(t *testing.T) {
		err := &domain.ProviderError{
			Kind:       domain.KindHTTPStatus,
			StatusCode: 429,
			RetryAfter: 30 * time.Second,
			Err:        errors.New("rate limited"),
		}
		got := describeProviderError(err)
		for _, want := range []string{"rate limited", "kind=http_status", "status=429", "retry_after=30s"} {
			if !strings.Contains(got, want) {
				t.Fatalf("describeProviderError missing %q in %q", want, got)
			}
		}
	})

	t.Run("server error with status only", func(t *testing.T) {
		err := &domain.ProviderError{
			Kind:       domain.KindHTTPStatus,
			StatusCode: 503,
			Err:        errors.New("upstream down"),
		}
		got := describeProviderError(err)
		if !strings.Contains(got, "status=503") {
			t.Fatalf("describeProviderError missing status=503 in %q", got)
		}
		if strings.Contains(got, "retry_after=") {
			t.Fatalf("describeProviderError must not add retry_after when absent, got %q", got)
		}
	})
}

// TestIsPermanentProviderFailure verifies that billing/credit errors are
// classified as permanent failures and NOT retried, even when the HTTP status
// is in the transient set (e.g. 503 with "out of credits" body). This prevents
// 3x wasted retry attempts on an account that has exhausted its balance.
func TestIsPermanentProviderFailure(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{name: "402 payment required", status: 402, body: "Payment Required", want: true},
		{name: "503 insufficient balance", status: 503, body: "insufficient balance", want: true},
		{name: "503 out of credits", status: 503, body: "out of credits", want: true},
		{name: "429 please recharge", status: 429, body: "please recharge your account", want: true},
		{name: "503 top up", status: 503, body: "please top up your balance", want: true},
		{name: "503 top-up hyphenated", status: 503, body: "top-up required", want: true},
		{name: "503 topup no hyphen", status: 503, body: "topup now", want: true},
		{name: "503 account suspended", status: 503, body: "account suspended", want: true},
		{name: "503 code 1113 string", status: 503, body: `{"code":"1113"}`, want: true},
		{name: "503 code 1113 number", status: 503, body: `{"code":1113}`, want: true},
		{name: "503 no resource package", status: 503, body: "no resource package", want: true},
		{name: "503 credit balance", status: 503, body: "credit balance is zero", want: true},
		{name: "503 billing", status: 503, body: "billing issue", want: true},
		{name: "503 generic server error", status: 503, body: "internal server error", want: false},
		{name: "429 generic rate limit", status: 429, body: "rate limit exceeded", want: false},
		{name: "502 bad gateway", status: 502, body: "bad gateway", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.IsPermanentProviderFailure(tt.status, tt.body); got != tt.want {
				t.Fatalf("domain.IsPermanentProviderFailure(%d, %q) = %t, want %t", tt.status, tt.body, got, tt.want)
			}
		})
	}
}

// TestIsRetryableProviderErrorRejectsPermanentFailure verifies that a 503 with
// a billing body is NOT retryable, even though 503 is in the transient status
// set. This is the integration point between isPermanentProviderFailure and
// domain.CanAutoRetry.
func TestShouldEmergencyCompact(t *testing.T) {
	overflow := &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 400,
		Err:        errors.New(`provider returned HTTP 400: Requested token count exceeds the model's maximum context length of 262144 tokens. You requested a total of 267042 tokens.`),
	}
	notOverflow := &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 400,
		Err:        errors.New("provider returned HTTP 400: unsupported parameter"),
	}

	if !shouldEmergencyCompact(overflow, 200_000, 150_000) {
		t.Error("expected emergency compact when estimate exceeds trigger")
	}
	// Even with a low heuristic estimate, an explicit context limit forces compaction.
	if !shouldEmergencyCompact(overflow, 100_000, 150_000) {
		t.Error("expected emergency compact with explicit context limit despite low estimate")
	}
	if shouldEmergencyCompact(notOverflow, 200_000, 150_000) {
		t.Error("unexpected emergency compact for non-overflow 400")
	}
}

func TestContextLimitFromError(t *testing.T) {
	overflow := &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 400,
		Err:        errors.New(`provider returned HTTP 400: Requested token count exceeds the model's maximum context length of 262144 tokens.`),
	}
	if got, ok := contextLimitFromError(overflow); !ok || got != 262144 {
		t.Fatalf("contextLimitFromError = (%d, %t), want (262144, true)", got, ok)
	}
	if _, ok := contextLimitFromError(errors.New("plain error")); ok {
		t.Fatal("contextLimitFromError should not match non-ProviderError")
	}
}

func TestIsRetryableProviderErrorRejectsPermanentFailure(t *testing.T) {
	err := &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 503,
		Err:        errors.New("provider returned HTTP 503: insufficient balance"),
	}
	if isRetryableProviderError(err) {
		t.Fatal("503 with billing body must NOT be retryable")
	}
}

// The observed OpenAI TPM rejection body (delivered either as an in-stream
// SSE error event on the Responses API or as an HTTP 429 on Chat
// Completions). Sanitized org id.
// TestIsTPMOverflowSemantics: the modern parser distinguishes three TPM
// rejection classes. Structural (requested > limit) and dominant (requested
// > half of limit) both require shrinking the request — the compact-then-
// retry path handles them via isTPMDominatedRequest, which covers structural
// as a subset. Modest requests (<= half the budget) are genuine congestion:
// waiting for the window to drain is the right fix.
func TestIsTPMDominatedRequestApp(t *testing.T) {
	structural := &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: errors.New(tpmOverflowBody)}
	if !isTPMDominatedRequest(structural) {
		t.Fatal("requested > limit must be dominated (structural subset)")
	}
	// Wrapping layers must not hide the signal.
	if !isTPMDominatedRequest(fmt.Errorf("stream round failed: %w", structural)) {
		t.Fatal("wrapped dominant TPM must still be detected")
	}
	dominant := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large for gpt-5.6-luna on tokens per min (TPM): Limit 500000, Used 271036, Requested 355391.")}
	if !isTPMDominatedRequest(dominant) {
		t.Fatal("requested > half the budget must be dominated")
	}
	modest := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large on tokens per min (TPM): Limit 500000, Used 271036, Requested 40000.")}
	if isTPMDominatedRequest(modest) {
		t.Fatal("requested <= half the budget is congestion, not dominated")
	}
	if isTPMDominatedRequest(errors.New("boom")) {
		t.Fatal("unrelated error must not match")
	}
	if isTPMDominatedRequest(nil) {
		t.Fatal("nil error must not match")
	}
}

// TestProviderRetryDelayRejectsStructuralTPM verifies that a structural TPM
// rejection is never retried, even when it arrives as an HTTP 429 with a
// Retry-After that would normally qualify. Retrying resends the same
// oversized request, which fails again in every window.
func TestProviderRetryDelayRejectsStructuralTPM(t *testing.T) {
	err := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second, Err: errors.New(tpmOverflowBody)}
	delay, retryable := providerRetryDelay(err, 1)
	if retryable {
		t.Fatalf("structural TPM must not be retryable, got delay=%s", delay)
	}
	// The transient variant with the same status stays retryable.
	transient := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large on tokens per min (TPM): Limit 200000, Requested 150000.")}
	if _, retryable := providerRetryDelay(transient, 1); !retryable {
		t.Fatal("transient TPM with Retry-After must stay retryable")
	}
}

// TestShouldEmergencyCompactTPM verifies that TPM rejections where the
// request dominates the per-minute budget trigger emergency compaction even
// when the local token estimate is far below the compaction trigger — the
// provider's own numbers are the proof (image-heavy transcripts are
// routinely undercounted by the chars/4 estimate). Structural (requested >
// limit) and dominant (requested > half of limit) both qualify; a modest
// request is congestion and must not force compaction.
func TestShouldEmergencyCompactTPM(t *testing.T) {
	structural := &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: errors.New(tpmOverflowBody)}
	if !shouldEmergencyCompact(structural, 50_000, 150_000) {
		t.Fatal("structural TPM must force emergency compaction despite low estimate")
	}
	dominant := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large on tokens per min (TPM): Limit 500000, Used 100000, Requested 300000.")}
	if !shouldEmergencyCompact(dominant, 50_000, 150_000) {
		t.Fatal("dominant TPM (request > half the budget) must force emergency compaction")
	}
	modest := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large on tokens per min (TPM): Limit 500000, Used 400000, Requested 40000.")}
	if shouldEmergencyCompact(modest, 50_000, 150_000) {
		t.Fatal("modest TPM (congestion) must not force compaction")
	}
}

// --- from ai_convert_test.go ---

// TestReasoningReplayInjectsPlaceholderWhenReasoningEmpty proves Bug 1:
// when ReasoningReplay is true but the prior assistant reasoning is empty
// (stripped, unavailable, or first-turn edge case), the replay must inject
// domain.ReasoningPlaceholder so models that require reasoning_content on
// every assistant message (deepseek, qwen, glm) do not 400 with
// "reasoning_content must be passed back".
func TestReasoningReplayInjectsPlaceholderWhenReasoningEmpty(t *testing.T) {
	req := ChatRequest{
		Model: "deepseek/deepseek-r1",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok", Reasoning: ""},
		},
		ReasoningReplay: true,
	}
	cr := ToCoreRequest(req, domain.ProviderChat, true)
	var assistant *core.Message
	for i := range cr.Messages {
		if cr.Messages[i].Role == core.RoleAssistant {
			assistant = &cr.Messages[i]
			break
		}
	}
	if assistant == nil {
		t.Fatalf("no assistant message in converted request")
	}
	var hasReasoningBlock bool
	for _, block := range assistant.Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			hasReasoningBlock = true
			if rb.Text != domain.ReasoningPlaceholder {
				t.Fatalf("reasoning block text = %q, want placeholder %q", rb.Text, domain.ReasoningPlaceholder)
			}
		}
	}
	if !hasReasoningBlock {
		t.Fatalf("ReasoningReplay=true with empty reasoning must inject placeholder ReasoningBlock, got blocks: %#v", assistant.Blocks)
	}
}

// TestReasoningReplayKeepsActualReasoning ensures non-empty reasoning is
// forwarded as-is (not replaced by the placeholder).
func TestReasoningReplayKeepsActualReasoning(t *testing.T) {
	req := ChatRequest{
		Model: "deepseek/deepseek-r1",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok", Reasoning: "I thought about it."},
		},
		ReasoningReplay: true,
	}
	cr := ToCoreRequest(req, domain.ProviderChat, true)
	for _, msg := range cr.Messages {
		if msg.Role != core.RoleAssistant {
			continue
		}
		for _, block := range msg.Blocks {
			if rb, ok := block.(core.ReasoningBlock); ok {
				if rb.Text != "I thought about it." {
					t.Fatalf("reasoning = %q, want original text", rb.Text)
				}
				return
			}
		}
		t.Fatalf("expected ReasoningBlock with original text, got blocks: %#v", msg.Blocks)
	}
}

// TestReasoningReplayOffDoesNotInjectPlaceholder ensures that when
// ReasoningReplay is false and reasoning is empty, no placeholder or
// reasoning block is injected.
func TestReasoningReplayOffDoesNotInjectPlaceholder(t *testing.T) {
	req := ChatRequest{
		Model: "openai/gpt-5",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok", Reasoning: ""},
		},
		ReasoningReplay: false,
	}
	cr := ToCoreRequest(req, domain.ProviderResponses, false)
	for _, msg := range cr.Messages {
		if msg.Role != core.RoleAssistant {
			continue
		}
		for _, block := range msg.Blocks {
			if _, ok := block.(core.ReasoningBlock); ok {
				t.Fatalf("ReasoningReplay=false with empty reasoning must not inject any ReasoningBlock, got: %#v", block)
			}
		}
	}
}

// TestReasoningSentEvenWhenReplayOff proves that reasoning from the
// persisted conversation is always sent when present, regardless of the
// ReasoningReplay flag. This is the conversation-store-as-source-of-truth
// approach: models that intermittently think (task-dependent reasoning)
// store reasoning on turns where they did think, and those turns replay
// correctly. Non-reasoning models safely ignore the reasoning field
// (verified against OpenRouter: minimax-m3 accepts reasoning without error).
//
// This fixes the original bug where reasoning was captured from the
// response but silently dropped on the next turn's replay because
// ReasoningReplay was false (model not in the catalog whitelist).
func TestReasoningSentEvenWhenReplayOff(t *testing.T) {
	req := ChatRequest{
		Model: "openrouter/some-glm-variant",
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 2+2?"},
			{Role: "assistant", Content: "Four", Reasoning: "User asks 2+2. Answer is 4."},
			{Role: "user", Content: "What is 3+3?"},
		},
		ReasoningReplay: false, // not in catalog whitelist
	}
	cr := ToCoreRequest(req, domain.ProviderChat, true)
	var assistant *core.Message
	for i := range cr.Messages {
		if cr.Messages[i].Role == core.RoleAssistant {
			assistant = &cr.Messages[i]
			break
		}
	}
	if assistant == nil {
		t.Fatalf("no assistant message in converted request")
	}
	var reasoningBlock *core.ReasoningBlock
	for i, block := range assistant.Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			reasoningBlock = &rb
			_ = i
			break
		}
	}
	if reasoningBlock == nil {
		t.Fatalf("expected ReasoningBlock with persisted reasoning text even when ReasoningReplay=false, got blocks: %#v", assistant.Blocks)
	}
	if reasoningBlock.Text != "User asks 2+2. Answer is 4." {
		t.Fatalf("reasoning = %q, want original persisted text", reasoningBlock.Text)
	}
}

// TestReasoningSentForRoutingModel proves that reasoning from persisted
// conversation is sent even for routing models (openrouter/auto). The
// conversation store tracks reasoning per-turn, so even if the underlying
// model changes between turns, the reasoning from the turn that produced
// it is replayed. Non-reasoning routes safely ignore the field.
func TestReasoningSentForRoutingModel(t *testing.T) {
	req := ChatRequest{
		Model: "openrouter/auto",
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 2+2?"},
			{Role: "assistant", Content: "Four", Reasoning: "User asks 2+2. Answer is 4."},
			{Role: "user", Content: "hi"}, // simple chit-chat, no reasoning expected
		},
		ReasoningReplay: false,
	}
	cr := ToCoreRequest(req, domain.ProviderChat, true)
	var assistant *core.Message
	for i := range cr.Messages {
		if cr.Messages[i].Role == core.RoleAssistant {
			assistant = &cr.Messages[i]
			break
		}
	}
	if assistant == nil {
		t.Fatalf("no assistant message in converted request")
	}
	var hasReasoning bool
	for _, block := range assistant.Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			hasReasoning = true
			if rb.Text != "User asks 2+2. Answer is 4." {
				t.Fatalf("reasoning = %q, want original persisted text", rb.Text)
			}
		}
	}
	if !hasReasoning {
		t.Fatalf("routing model should still receive persisted reasoning, got blocks: %#v", assistant.Blocks)
	}
}

// TestAssistantBlockCombinations proves that all combinations of
// reasoning/text/tool blocks in assistant messages are converted correctly:
//   - reasoning only
//   - text only
//   - tool only
//   - reasoning + text
//   - reasoning + tool
//   - text + tool
//   - reasoning + text + tool
//
// The key invariant: ReasoningBlock must always be first (Anthropic requires
// thinking blocks to be the first block in an assistant message). Every
// combination must produce valid blocks without errors.
func TestAssistantBlockCombinations(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		reasoning string
		toolCalls []domain.ToolCall
		wantOrder []string // block type names in expected order
	}{
		{
			name:      "reasoning only",
			reasoning: "I thought about it.",
			wantOrder: []string{"ReasoningBlock"},
		},
		{
			name:      "text only",
			content:   "Hello!",
			wantOrder: []string{"TextBlock"},
		},
		{
			name:      "tool only",
			toolCalls: []domain.ToolCall{{ID: "call_1", Name: "calc", Args: "{}"}},
			wantOrder: []string{"ToolUseBlock"},
		},
		{
			name:      "reasoning + text",
			reasoning: "Thinking...",
			content:   "Answer.",
			wantOrder: []string{"ReasoningBlock", "TextBlock"},
		},
		{
			name:      "reasoning + tool",
			reasoning: "Need to calculate.",
			toolCalls: []domain.ToolCall{{ID: "call_1", Name: "calc", Args: "{}"}},
			wantOrder: []string{"ReasoningBlock", "ToolUseBlock"},
		},
		{
			name:      "text + tool",
			content:   "Let me check.",
			toolCalls: []domain.ToolCall{{ID: "call_1", Name: "calc", Args: "{}"}},
			wantOrder: []string{"TextBlock", "ToolUseBlock"},
		},
		{
			name:      "reasoning + text + tool",
			reasoning: "I should use a tool.",
			content:   "Let me calculate.",
			toolCalls: []domain.ToolCall{{ID: "call_1", Name: "calc", Args: "{}"}},
			wantOrder: []string{"ReasoningBlock", "TextBlock", "ToolUseBlock"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ChatRequest{
				Model: "test/model",
				Messages: []ChatMessage{
					{Role: "user", Content: "hi"},
					{Role: "assistant", Content: tt.content, Reasoning: tt.reasoning, ToolCalls: tt.toolCalls},
				},
				ReasoningReplay: false,
			}
			cr := ToCoreRequest(req, domain.ProviderChat, true)

			var assistant *core.Message
			for i := range cr.Messages {
				if cr.Messages[i].Role == core.RoleAssistant {
					assistant = &cr.Messages[i]
					break
				}
			}
			if assistant == nil {
				t.Fatalf("no assistant message found")
			}

			if len(assistant.Blocks) != len(tt.wantOrder) {
				t.Fatalf("block count = %d, want %d. Blocks: %#v", len(assistant.Blocks), len(tt.wantOrder), assistant.Blocks)
			}

			for i, want := range tt.wantOrder {
				got := blockTypeName(assistant.Blocks[i])
				if got != want {
					t.Errorf("block[%d] = %s, want %s", i, got, want)
				}
			}
		})
	}
}

// TestReasoningBlockAlwaysFirstForAnthropic proves the Anthropic ordering
// requirement: if a ReasoningBlock is present, it must be the first block.
// Anthropic rejects with 400: "If an assistant message contains any thinking
// blocks, the first block must be `thinking` or `redacted_thinking`".
func TestReasoningBlockAlwaysFirstForAnthropic(t *testing.T) {
	req := ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 2+2?"},
			{Role: "assistant", Content: "Four", Reasoning: "User asks 2+2. Answer is 4.", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "verify", Args: "{}"}}},
			{Role: "user", Content: "Thanks!"},
		},
	}
	cr := ToCoreRequest(req, domain.ProviderMessages, false)

	var assistant *core.Message
	for i := range cr.Messages {
		if cr.Messages[i].Role == core.RoleAssistant {
			assistant = &cr.Messages[i]
			break
		}
	}
	if assistant == nil {
		t.Fatalf("no assistant message found")
	}
	if len(assistant.Blocks) == 0 {
		t.Fatalf("expected blocks, got none")
	}
	if _, ok := assistant.Blocks[0].(core.ReasoningBlock); !ok {
		t.Fatalf("first block must be ReasoningBlock for Anthropic, got %T: %#v", assistant.Blocks[0], assistant.Blocks[0])
	}
}

func blockTypeName(b core.Block) string {
	switch b.(type) {
	case core.ReasoningBlock:
		return "ReasoningBlock"
	case core.TextBlock:
		return "TextBlock"
	case core.ToolUseBlock:
		return "ToolUseBlock"
	default:
		return fmt.Sprintf("%T", b)
	}
}

func TestToCoreRequestCopiesToolChoice(t *testing.T) {
	choice := map[string]any{"type": "function", "function": map[string]any{"name": "summary"}}
	cr := ToCoreRequest(ChatRequest{Model: "m", ToolChoice: choice}, domain.ProviderChat, false)
	if cr.ToolChoice == nil {
		t.Fatal("ToolChoice dropped during conversion")
	}
}

func TestToCoreRequestSetsCompactionItemsForResponses(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "gpt-5.2", CompactionBlob: `[{"type":"compaction"}]`}, domain.ProviderResponses, false)
	if got := cr.ProviderOptions["compaction_items"]; got != `[{"type":"compaction"}]` {
		t.Fatalf("compaction_items = %#v, want the blob", got)
	}
}

func TestToCoreRequestOmitsCompactionItemsForChatKind(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "gpt-4o", CompactionBlob: `[{"type":"compaction"}]`}, domain.ProviderChat, false)
	if _, ok := cr.ProviderOptions["compaction_items"]; ok {
		t.Fatal("compaction_items must not be set for chat kind")
	}
}

func TestToCoreRequestMessagesCacheControlUsesTTL(t *testing.T) {
	req := ChatRequest{
		Model:         "claude-sonnet-4-6",
		System:        "you are helpful",
		PromptCaching: true,
		PromptCache:   &PromptCachePolicy{Mode: "auto", TTL: "1h", Key: "pc_unused"},
	}
	cr := ToCoreRequest(req, domain.ProviderMessages, false)
	if len(cr.Messages) == 0 {
		t.Fatal("expected system message")
	}
	tb, ok := cr.Messages[0].Blocks[0].(core.TextBlock)
	if !ok || tb.Cache == nil || tb.Cache.TTL != core.CacheTTL1h {
		t.Fatalf("system cache = %#v, want ttl 1h", cr.Messages[0].Blocks)
	}
	if cr.ProviderOptions["prompt_cache_key"] != nil {
		t.Fatal("messages kind must not send prompt_cache_key")
	}
}

// TestToCoreRequestDirectVanillaChatNeverPutsTTLOnSystemBreakpoint verifies
// the direct OpenAI Chat conversion path. A 5m/1h TTL is not represented as
// cache_control there, and the incompatible direct-Chat TTL option is omitted.
func TestToCoreRequestDirectVanillaChatNeverPutsTTLOnSystemBreakpoint(t *testing.T) {
	req := ChatRequest{
		Model:         "deepseek-v4-flash",
		System:        "you are helpful",
		PromptCaching: true,
		PromptCache:   &PromptCachePolicy{Mode: "auto", TTL: "1h", Key: "pc_oc"},
	}
	cr := ToCoreRequest(req, domain.ProviderChat, false)
	tb, ok := cr.Messages[0].Blocks[0].(core.TextBlock)
	if !ok {
		t.Fatalf("system block = %#v", cr.Messages[0].Blocks)
	}
	if tb.Cache != nil {
		t.Fatalf("vanilla chat system cache = %#v, want nil (direct Chat has no cache_control breakpoint)", tb.Cache)
	}
	if cr.ProviderOptions["prompt_cache_options"] != nil {
		t.Fatalf("vanilla chat must not send prompt_cache_options for 1h TTL (Console Go only accepts 5m|1h on cache_control, OpenAI Chat only accepts 30m): %#v", cr.ProviderOptions["prompt_cache_options"])
	}
	if got := cr.ProviderOptions["prompt_cache_key"]; got != "pc_oc" {
		t.Fatalf("prompt_cache_key = %#v, want pc_oc", got)
	}
}

func TestNewProviderContextCustomOpenCodeKeepsVanillaChatWire(t *testing.T) {
	p := &domain.Provider{
		ID:      "prov_b9587aa5f937c4f2",
		Driver:  domain.ProviderDriverOpenRouter,
		Kind:    domain.ProviderChat,
		BaseURL: "https://opencode.ai/zen/go/v1",
	}
	pc := NewProviderContext(p, nil)
	if pc.OpenRouter {
		t.Fatal("custom OpenCode zen/go must convert Chat requests with the vanilla OpenAI wire")
	}
	if pc.Driver != domain.ProviderDriverOpenRouter {
		t.Fatalf("Driver = %q, want stored openrouter, OpenRouter=%v", pc.Driver, pc.OpenRouter)
	}
	if pc.BaseURL != p.BaseURL {
		t.Fatalf("BaseURL = %q, want %q", pc.BaseURL, p.BaseURL)
	}
}

func TestBuildPromptCachePolicyForContextOpenCodeIgnoresOpenRouterFlag(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	adapter := ProviderContext{
		ProviderID: "prov_oc",
		Kind:       domain.ProviderChat,
		Driver:     domain.ProviderDriverOpenRouter,
		OpenRouter: false,
		BaseURL:    "https://opencode.ai/zen/go/v1",
	}
	policy := buildPromptCachePolicyForContext(settings, adapter, "deepseek-v4-flash", "conv_abc", promptCacheConversationPrefix)
	if policy == nil || policy.TTL != "5m" {
		t.Fatalf("opencode context TTL = %+v, want 5m (5m/1h enum, not 30m)", policy)
	}
}

func TestToCoreRequestResponsesSendsPromptCacheOptions(t *testing.T) {
	req := ChatRequest{
		Model:          "gpt-5",
		System:         "you are helpful",
		PromptCaching:  true,
		PromptCache:    &PromptCachePolicy{Mode: "auto", TTL: "30m", Key: "pc_abc"},
		CompactionBlob: `[{"type":"compaction"}]`,
	}
	cr := ToCoreRequest(req, domain.ProviderResponses, false)
	if got := cr.ProviderOptions["prompt_cache_key"]; got != "pc_abc" {
		t.Fatalf("prompt_cache_key = %#v", got)
	}
	opts, ok := cr.ProviderOptions["prompt_cache_options"].(map[string]any)
	if !ok || opts["ttl"] != "30m" {
		t.Fatalf("prompt_cache_options = %#v, want ttl 30m", cr.ProviderOptions["prompt_cache_options"])
	}
	if got := cr.ProviderOptions["compaction_items"]; got != `[{"type":"compaction"}]` {
		t.Fatalf("compaction_items dropped when merging cache options: %#v", got)
	}
}

func TestToCoreRequestMiniMaxChatSendsReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "minimax-m3"}, domain.ProviderChat, false)
	if cr.ProviderOptions["reasoning_split"] != true {
		t.Fatalf("MiniMax Chat reasoning_split = %#v, want true", cr.ProviderOptions["reasoning_split"])
	}
}

func TestToCoreRequestNonMiniMaxChatOmitsReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "glm-5.3-flash"}, domain.ProviderChat, false)
	if _, ok := cr.ProviderOptions["reasoning_split"]; ok {
		t.Fatalf("GLM Chat must not send reasoning_split, got %#v", cr.ProviderOptions)
	}
}

func TestToCoreRequestOpenRouterMiniMaxOmitsReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "minimax/minimax-m3:free"}, domain.ProviderChat, true)
	if _, ok := cr.ProviderOptions["reasoning_split"]; ok {
		t.Fatalf("OpenRouter MiniMax uses reasoning object, must not send reasoning_split: %#v", cr.ProviderOptions)
	}
}

func TestToCoreRequestMiniMaxMessagesOmitsReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "minimax-m3"}, domain.ProviderMessages, false)
	if _, ok := cr.ProviderOptions["reasoning_split"]; ok {
		t.Fatalf("Messages MiniMax uses thinking blocks, must not send reasoning_split: %#v", cr.ProviderOptions)
	}
}

func TestToCoreRequestMiniMaxChatHonorsStripReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{
		Model:       "minimax-m3",
		StripParams: []string{"reasoning_split"},
	}, domain.ProviderChat, false)
	if _, ok := cr.ProviderOptions["reasoning_split"]; ok {
		t.Fatalf("stripped MiniMax Chat must omit reasoning_split, got %#v", cr.ProviderOptions)
	}
}

func TestToCoreRequestChatSendsPromptCacheKey(t *testing.T) {
	req := ChatRequest{
		Model:         "gpt-5",
		System:        "you are helpful",
		PromptCaching: true,
		PromptCache:   &PromptCachePolicy{Mode: "auto", TTL: "30m", Key: "pc_chat"},
	}
	cr := ToCoreRequest(req, domain.ProviderChat, false)
	if got := cr.ProviderOptions["prompt_cache_key"]; got != "pc_chat" {
		t.Fatalf("chat prompt_cache_key = %#v", got)
	}
	opts, ok := cr.ProviderOptions["prompt_cache_options"].(map[string]any)
	if !ok || opts["ttl"] != "30m" {
		t.Fatalf("chat prompt_cache_options = %#v, want ttl 30m", cr.ProviderOptions["prompt_cache_options"])
	}
}

func TestToCoreRequestOpenRouterChatSendsPromptCacheKeyAndSessionID(t *testing.T) {
	req := ChatRequest{
		Model:         "anthropic/claude-sonnet-4",
		System:        "you are helpful",
		PromptCaching: true,
		PromptCache:   &PromptCachePolicy{Mode: "auto", TTL: "1h", Key: "nusashell_cv_0123456789012345678"},
	}
	cr := ToCoreRequest(req, domain.ProviderChat, true)
	if got := cr.ProviderOptions["prompt_cache_key"]; got != req.PromptCache.Key {
		t.Fatalf("OpenRouter chat prompt_cache_key = %#v, want %q", got, req.PromptCache.Key)
	}
	if got := cr.ProviderOptions["session_id"]; got != req.PromptCache.Key {
		t.Fatalf("OpenRouter chat session_id = %#v, want %q", got, req.PromptCache.Key)
	}
	tb, ok := cr.Messages[0].Blocks[0].(core.TextBlock)
	if !ok || tb.Cache == nil || tb.Cache.TTL != core.CacheTTL1h {
		t.Fatalf("openrouter system cache = %#v, want ttl 1h", cr.Messages[0].Blocks)
	}
}

func TestToCoreRequestOpenRouterDelegatedKindsCarrySessionID(t *testing.T) {
	for _, kind := range []domain.ProviderKind{domain.ProviderMessages, domain.ProviderResponses} {
		t.Run(string(kind), func(t *testing.T) {
			key := "nusashell_bg_0123456789012345678"
			cr := ToCoreRequest(ChatRequest{
				Model:         "model",
				PromptCaching: true,
				PromptCache:   &PromptCachePolicy{Mode: "auto", Key: key},
			}, kind, true)
			if got := cr.ProviderOptions["session_id"]; got != key {
				t.Fatalf("session_id = %#v, want %q", got, key)
			}
		})
	}
}

// --- from ai_convert_providerroute_test.go ---

// TestToCoreRequestProviderRouteInjection verifies that a pinned upstream
// route is translated to the OpenRouter provider object only when the
// gateway is an aggregator AND the route is non-empty — direct providers
// and auto routing must never carry the provider field.
func TestToCoreRequestProviderRouteInjection(t *testing.T) {
	base := ChatRequest{
		Model:         "meta-llama/llama-3.3-70b-instruct",
		ProviderRoute: "nebius/fp8",
		Messages:      []ChatMessage{{Role: "user", Content: "hi"}},
	}

	// 1) OpenRouter chat + route → provider object with strict pinning.
	cr := ToCoreRequest(base, domain.ProviderChat, true)
	raw, ok := cr.ProviderOptions["provider"]
	if !ok {
		t.Fatal("provider option missing for openrouter+route")
	}
	routing, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("provider option = %T, want map", raw)
	}
	order, ok := routing["order"].([]string)
	if !ok || len(order) != 1 || order[0] != "nebius/fp8" {
		t.Fatalf("order = %#v, want [nebius/fp8]", routing["order"])
	}
	if ff, ok := routing["allow_fallbacks"].(bool); !ok || ff {
		t.Fatalf("allow_fallbacks = %#v, want false", routing["allow_fallbacks"])
	}

	// 2) OpenRouter + empty route → no provider field (auto/load balance).
	auto := base
	auto.ProviderRoute = ""
	cr = ToCoreRequest(auto, domain.ProviderChat, true)
	if _, ok := cr.ProviderOptions["provider"]; ok {
		t.Fatal("provider option must be omitted for auto routing")
	}

	// 3) OpenRouter + whitespace route → treated as auto.
	ws := base
	ws.ProviderRoute = "   "
	cr = ToCoreRequest(ws, domain.ProviderChat, true)
	if _, ok := cr.ProviderOptions["provider"]; ok {
		t.Fatal("provider option must be omitted for whitespace route")
	}

	// 4) Non-aggregator provider (Anthropic messages) + route → never sent.
	cr = ToCoreRequest(base, domain.ProviderMessages, false)
	if _, ok := cr.ProviderOptions["provider"]; ok {
		t.Fatal("provider option must not reach direct providers")
	}

	// 5) Aggregator but non-chat wire (messages via OpenRouter) → the
	// provider object is a chat-completions extension; keep it clean.
	cr = ToCoreRequest(base, domain.ProviderMessages, true)
	if _, ok := cr.ProviderOptions["provider"]; ok {
		t.Fatal("provider option must not be injected for non-chat kinds")
	}
}
