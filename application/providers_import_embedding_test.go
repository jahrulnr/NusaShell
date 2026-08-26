package application

import (
	"context"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

// fakeEmbeddingImportAdapter simulates an OpenAI-compatible /v1/models:
// every installed model (chat and embedding alike) is returned untagged.
type fakeEmbeddingImportAdapter struct {
	fakeVisionAdapter
}

func (f *fakeEmbeddingImportAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }

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
