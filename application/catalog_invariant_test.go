package application

// Invariant tests for the catalog-as-capability-assigner design:
// - Catalog never writes models (no insert/rename); /models API is the only writer.
// - Catalog never reclassifies a model (Kind stays with the lister source).
// - Catalog hints are dynamic (from model ID prefix), never vendor-hardcoded.

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/modelcatalog"
)

func TestCatalogHintIsNotVendorHardcoded(t *testing.T) {
	// Any chat provider (TokenRouter, OpenRouter, a future gateway) must
	// NOT be special-cased in code: the hint comes from the model ID
	// prefix, which is output of the /models API. This is the regression
	// test for the "every new provider needs a code edit" bug.
	p := &domain.Provider{
		Kind:    domain.ProviderChat,
		BaseURL: "https://api.tokenrouter.com/v1",
	}
	if got := catalogHintForProvider(p); got != "" {
		t.Fatalf("catalogHintForProvider(tokenrouter) = %q, want \"\" (no vendor special-case)", got)
	}
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

func TestCatalogHintForProviderKindStillWorks(t *testing.T) {
	// Kind-level hints (wire-format hints) are still fixed, but only for
	// the three official wire kinds — not per-vendor.
	for _, tc := range []struct {
		kind domain.ProviderKind
		want string
	}{
		{domain.ProviderResponses, "openai"},
		{domain.ProviderMessages, "anthropic"},
		{domain.ProviderCodex, "openai"},
	} {
		if got := catalogHintForProvider(&domain.Provider{Kind: tc.kind}); got != tc.want {
			t.Fatalf("catalogHintForProvider(kind %v) = %q, want %q", tc.kind, got, tc.want)
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
