package application

import (
	"context"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
)

// fakeEmbedder is a controllable Embedder for consolidation tests.
type fakeEmbedder struct {
	vectors map[string][]float32
	dim     int
}

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if v, ok := f.vectors[text]; ok {
		return v, nil
	}
	return nil, nil
}
func (f *fakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.vectors[t]
	}
	return out, nil
}
func (f *fakeEmbedder) Dim() int { return f.dim }

func TestConsolidateMergesNearDuplicates(t *testing.T) {
	dir := t.TempDir()
	cache, err := jsonstore.NewEmbeddingCache(dir)
	if err != nil {
		t.Fatalf("NewEmbeddingCache: %v", err)
	}
	defer cache.Close()

	// Two near-identical entries with very similar embeddings
	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "use docker for container builds in CI", Tags: []string{"docker"}},
		{ID: "mem_2", Content: "use docker for container builds in CI pipeline", Tags: []string{"ci"}},
	}}
	// Embeddings: near-identical (cosine ~0.99)
	embed := &fakeEmbedder{
		vectors: map[string][]float32{
			"use docker for container builds in CI":          {1.0, 0.0, 0.0},
			"use docker for container builds in CI pipeline": {0.99, 0.01, 0.0},
		},
		dim: 3,
	}
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	c := NewConsolidator(mem, g, embed, cache, ConsolidationConfig{SimilarityThreshold: 0.92, MinContentLen: 10}, "test-model")

	result, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if result.Merged != 1 {
		t.Errorf("Merged = %d, want 1", result.Merged)
	}
	// mem_2 should be deleted, mem_1 should survive with unioned tags
	surviving := mem.List()
	if len(surviving) != 1 {
		t.Errorf("expected 1 surviving entry, got %d", len(surviving))
	}
	if surviving[0].ID != "mem_1" {
		t.Errorf("expected mem_1 to survive, got %s", surviving[0].ID)
	}
	// Tags should be unioned
	hasCI := false
	for _, tag := range surviving[0].Tags {
		if tag == "ci" {
			hasCI = true
		}
	}
	if !hasCI {
		t.Error("surviving entry should have 'ci' tag from merged entry")
	}
}

func TestConsolidateKeepsDissimilar(t *testing.T) {
	dir := t.TempDir()
	cache, _ := jsonstore.NewEmbeddingCache(dir)
	defer cache.Close()

	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "use docker for container builds"},
		{ID: "mem_2", Content: "prefer postgres for the database"},
	}}
	embed := &fakeEmbedder{
		vectors: map[string][]float32{
			"use docker for container builds":  {1.0, 0.0},
			"prefer postgres for the database": {0.0, 1.0},
		},
		dim: 2,
	}
	g := NewLearningGraphService(&fakeEdgeStore{})
	c := NewConsolidator(mem, g, embed, cache, DefaultConsolidationConfig(), "test-model")

	result, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if result.Merged != 0 {
		t.Errorf("Merged = %d, want 0 (dissimilar)", result.Merged)
	}
	if len(mem.List()) != 2 {
		t.Errorf("expected 2 entries to survive, got %d", len(mem.List()))
	}
}

func TestConsolidateSkipsWithoutEmbedder(t *testing.T) {
	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "test entry one"},
		{ID: "mem_2", Content: "test entry two"},
	}}
	c := NewConsolidator(mem, nil, nil, nil, DefaultConsolidationConfig(), "")
	result, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if result.Merged != 0 {
		t.Errorf("Merged = %d, want 0 (no embedder)", result.Merged)
	}
}

func TestConsolidateSkipsShortEntries(t *testing.T) {
	dir := t.TempDir()
	cache, _ := jsonstore.NewEmbeddingCache(dir)
	defer cache.Close()

	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "ok"},  // too short
		{ID: "mem_2", Content: "yes"}, // too short
		{ID: "mem_3", Content: "use docker for container builds in CI pipeline"},
	}}
	embed := &fakeEmbedder{
		vectors: map[string][]float32{
			"ok":  {1.0, 0.0},
			"yes": {0.99, 0.01},
			"use docker for container builds in CI pipeline": {1.0, 0.0},
		},
		dim: 2,
	}
	c := NewConsolidator(mem, NewLearningGraphService(&fakeEdgeStore{}), embed, cache,
		ConsolidationConfig{SimilarityThreshold: 0.92, MinContentLen: 20}, "test-model")

	result, _ := c.Consolidate(context.Background())
	if result.Merged != 0 {
		t.Errorf("Merged = %d, want 0 (short entries skipped)", result.Merged)
	}
}
