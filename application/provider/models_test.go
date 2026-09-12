package provider

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/modelcatalog"
)

type modelListStore struct {
	providers []*domain.Provider
}

func (s modelListStore) List() []*domain.Provider { return s.providers }
func (s modelListStore) Get(id string) (*domain.Provider, error) {
	for _, p := range s.providers {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil
}
func (s modelListStore) Save(*domain.Provider) error { return nil }
func (s modelListStore) Delete(string) error         { return nil }

func TestHandleModelsListUsesCodexCatalogContextOverDiscoveryValue(t *testing.T) {
	svc := New(Deps{
		Store: modelListStore{providers: []*domain.Provider{{
			ID: "codex", Name: "Codex", Kind: domain.ProviderCodex, Enabled: true,
			Models: []domain.Model{{ID: "gpt-5.6-luna", Context: 272_000}},
		}}},
		Catalog: contextCatalogStub{models: map[string]*modelcatalog.ModelMetadata{
			"gpt-5.6-luna": {ID: "openai/gpt-5.6-luna", Context: 1_050_000},
		}},
	})

	result, rpcErr := svc.HandleModelsList()
	if rpcErr != nil {
		t.Fatalf("HandleModelsList: %v", rpcErr.Message)
	}
	models := result.(contracts.ModelsListResult).Models
	var luna *contracts.ModelDTO
	for i := range models {
		if models[i].ID == "gpt-5.6-luna" {
			luna = &models[i]
			break
		}
	}
	if luna == nil {
		t.Fatalf("models = %+v, want Luna model", models)
	}
	if luna.Context != 1_050_000 {
		t.Fatalf("Codex model context = %d, want catalog value 1050000", luna.Context)
	}
}

func TestHandleModelsListUsesRuntimeAdjustedContextWindow(t *testing.T) {
	svc := New(Deps{
		Store: modelListStore{providers: []*domain.Provider{{
			ID: "p1", Name: "Provider", Kind: domain.ProviderChat, Enabled: true,
			Models: []domain.Model{{ID: "model", Context: 1_000_000}},
		}}},
		AdjustModel: func(_ *domain.Provider, model *domain.Model) {
			model.Context = 272_000
		},
	})

	result, rpcErr := svc.HandleModelsList()
	if rpcErr != nil {
		t.Fatalf("HandleModelsList: %v", rpcErr.Message)
	}
	models := result.(contracts.ModelsListResult).Models
	if len(models) != 1 {
		t.Fatalf("model count = %d, want 1", len(models))
	}
	if models[0].Context != 272_000 {
		t.Fatalf("model context = %d, want runtime-adjusted 272000", models[0].Context)
	}
}
