package application

import (
	"context"
	"errors"
	"testing"

	"nusashell/application/service/modeloverrides"
	"nusashell/domain"
	"nusashell/infrastructure/ai/modelcatalog"
)

type directCodexCatalogStub struct {
	models map[string]*modelcatalog.ModelMetadata
}

func (s directCodexCatalogStub) EnsureLoaded(context.Context) error { return nil }
func (s directCodexCatalogStub) Loaded() bool                       { return true }
func (s directCodexCatalogStub) Lookup(_ string, modelID string) *modelcatalog.ModelMetadata {
	return s.models[modelID]
}

type failingCodexCatalogStub struct{}

func (failingCodexCatalogStub) EnsureLoaded(context.Context) error {
	return errors.New("catalog unavailable")
}
func (failingCodexCatalogStub) Loaded() bool { return false }
func (failingCodexCatalogStub) Lookup(string, string) *modelcatalog.ModelMetadata {
	return nil
}

func TestResolveModelWithMetaUsesDirectCodexCatalogContext(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"codex": {
			ID: "codex", Name: "Codex", Kind: domain.ProviderCodex, Enabled: true,
			Models: []domain.Model{{ID: "gpt-5.6-luna", Context: 272_000}},
		},
	}}
	app := &App{
		Providers:   providers,
		Credentials: &memCreds{m: map[string]string{"codex": "token"}},
		ModelCatalog: directCodexCatalogStub{models: map[string]*modelcatalog.ModelMetadata{
			"gpt-5.6-luna": {ID: "openai/gpt-5.6-luna", Context: 1_050_000},
		}},
	}

	p, m, _, err := app.resolveModelWithMeta("codex:gpt-5.6-luna")
	if err != nil {
		t.Fatalf("resolveModelWithMeta: %v", err)
	}
	if p == nil || m == nil {
		t.Fatal("expected Codex provider and model")
	}
	if m.Context != 1_050_000 {
		t.Fatalf("direct Codex context = %d, want catalog value 1050000", m.Context)
	}
}

func TestRefreshCodexContextWindowKeepsDiscoveryValueWhenCatalogUnavailable(t *testing.T) {
	p := &domain.Provider{
		ID: "codex", Kind: domain.ProviderCodex,
		Models: []domain.Model{{ID: "gpt-5.6-luna", Context: 272_000}},
	}
	app := &App{ModelCatalog: failingCodexCatalogStub{}}

	app.refreshCodexContextWindow(p)
	if got := p.Models[0].Context; got != 272_000 {
		t.Fatalf("Codex context after catalog failure = %d, want discovery value 272000", got)
	}
}

func TestRefreshCodexContextWindowSkipsNonCodexProviders(t *testing.T) {
	p := &domain.Provider{
		ID: "openai", Kind: domain.ProviderResponses,
		Models: []domain.Model{{ID: "gpt-5.6-luna", Context: 272_000}},
	}
	app := &App{ModelCatalog: directCodexCatalogStub{models: map[string]*modelcatalog.ModelMetadata{
		"gpt-5.6-luna": {ID: "openai/gpt-5.6-luna", Context: 1_050_000},
	}}}

	app.refreshCodexContextWindow(p)
	if got := p.Models[0].Context; got != 272_000 {
		t.Fatalf("non-Codex context after refresh = %d, want unchanged 272000", got)
	}
}

func TestResolveModelWithMetaKeepsManualContextOverrideAfterCodexCatalogRefresh(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"codex": {
			ID: "codex", Name: "Codex", Kind: domain.ProviderCodex, Enabled: true,
			Models: []domain.Model{{ID: "gpt-5.6-luna", Context: 272_000}},
		},
	}}
	overrides := modeloverrides.New(nil)
	manualContext := 123_456
	if err := overrides.Set(&domain.ModelOverride{Provider: "codex", Model: "gpt-5.6-luna", Context: &manualContext}); err != nil {
		t.Fatalf("set manual context override: %v", err)
	}
	app := &App{
		Providers:   providers,
		Credentials: &memCreds{m: map[string]string{"codex": "token"}},
		ModelCatalog: directCodexCatalogStub{models: map[string]*modelcatalog.ModelMetadata{
			"gpt-5.6-luna": {ID: "openai/gpt-5.6-luna", Context: 1_050_000},
		}},
		modelOverrides: overrides,
	}

	_, model, _, err := app.resolveModelWithMeta("codex:gpt-5.6-luna")
	if err != nil {
		t.Fatalf("resolveModelWithMeta: %v", err)
	}
	if model == nil || model.Context != manualContext {
		t.Fatalf("manual Codex context = %+v, want %d after catalog refresh", model, manualContext)
	}
}
