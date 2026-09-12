package provider

import (
	"context"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/modelcatalog"
)

type contextCatalogStub struct {
	models map[string]*modelcatalog.ModelMetadata
}

func (s contextCatalogStub) EnsureLoaded(context.Context) error { return nil }
func (s contextCatalogStub) Loaded() bool                       { return true }
func (s contextCatalogStub) Lookup(_ string, modelID string) *modelcatalog.ModelMetadata {
	return s.models[modelID]
}

type codexImportAdapter struct {
	AIProvider
	models []domain.Model
}

func (a codexImportAdapter) ListModels(context.Context, string) ([]domain.Model, error) {
	return a.models, nil
}

func TestContextWindowFromCatalogUsesDirectCodexMetadata(t *testing.T) {
	if got := contextWindowFromCatalog(domain.ProviderCodex, 272_000, 1_050_000); got != 1_050_000 {
		t.Fatalf("Codex context = %d, want catalog value 1050000", got)
	}
	if got := contextWindowFromCatalog(domain.ProviderCodex, 272_000, 0); got != 272_000 {
		t.Fatalf("Codex context without catalog = %d, want existing value 272000", got)
	}
	if got := contextWindowFromCatalog(domain.ProviderChat, 272_000, 1_050_000); got != 272_000 {
		t.Fatalf("non-Codex context = %d, want existing provider value 272000", got)
	}
	if got := contextWindowFromCatalog(domain.ProviderChat, 0, 128_000); got != 128_000 {
		t.Fatalf("missing non-Codex context = %d, want catalog value 128000", got)
	}
}

func TestImportCodexUsesCatalogContextOverAppServerDiscoveryValue(t *testing.T) {
	store := &modelListStore{providers: []*domain.Provider{{
		ID: "codex", Name: "Codex", Kind: domain.ProviderCodex, Enabled: true,
	}}}
	svc := New(Deps{
		Store: store,
		Factory: func(context.Context, *domain.Provider, string) (AIProvider, error) {
			return codexImportAdapter{models: []domain.Model{{ID: "gpt-5.6-luna", Context: 272_000}}}, nil
		},
		Catalog: contextCatalogStub{models: map[string]*modelcatalog.ModelMetadata{
			"gpt-5.6-luna": {ID: "openai/gpt-5.6-luna", Context: 1_050_000},
		}},
	})

	models, err := svc.importModelsForProvider(context.Background(), store.providers[0], "")
	if err != nil {
		t.Fatalf("importModelsForProvider: %v", err)
	}
	var luna *domain.Model
	for i := range models {
		if models[i].ID == "gpt-5.6-luna" {
			luna = &models[i]
			break
		}
	}
	if luna == nil || luna.Context != 1_050_000 {
		t.Fatalf("imported models = %+v, want Luna context 1050000", models)
	}
}

func TestImportSeedCodexImageModelsDoesNotDuplicate(t *testing.T) {
	in := []domain.Model{{ID: "gpt-image-2", Kind: domain.ModelKindChat}}
	out := seedCodexImageModels(in)
	count := 0
	for _, m := range out {
		if m.ID == "gpt-image-2" {
			count++
			if m.Kind != domain.ModelKindImage {
				t.Fatalf("kind = %q", m.Kind)
			}
			if !m.Vision {
				t.Fatal("gpt-image-2 must be tagged Vision for i2i edits")
			}
		}
	}
	if count != 1 {
		t.Fatalf("duplicates = %d", count)
	}
}

func TestImportSeedCodexImageModelsAddsMissing(t *testing.T) {
	out := seedCodexImageModels([]domain.Model{{ID: "gpt-5.4"}})
	got := map[string]domain.ModelKind{}
	gotVision := map[string]bool{}
	for _, m := range out {
		got[m.ID] = m.Kind
		gotVision[m.ID] = m.Vision
	}
	if got["gpt-5.4"] == domain.ModelKindImage {
		t.Fatal("chat model must not be retagged image")
	}
	if got["gpt-image-2"] != domain.ModelKindImage || got["gpt-image-1.5"] != domain.ModelKindImage {
		t.Fatalf("seeded kinds = %v", got)
	}
	if !gotVision["gpt-image-2"] || !gotVision["gpt-image-1.5"] {
		t.Fatalf("seeded vision flags = %v, want both true (i2i-capable)", gotVision)
	}
}
