package provider

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
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
