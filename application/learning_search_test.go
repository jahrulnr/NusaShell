package application

import (
	"context"
	"math"
	"nusashell/application/service/textsim"
	"nusashell/domain"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{"empty", []float32{}, []float32{1, 0}, 0.0},
		{"mismatched len", []float32{1, 0}, []float32{1, 0, 0}, 0.0},
		{"zero vector", []float32{0, 0, 0}, []float32{1, 0, 0}, 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := textsim.CosineSimilarity(tc.a, tc.b)
			if abs32(got-tc.want) > 0.001 {
				t.Errorf("cosineSimilarity = %.4f, want %.4f", got, tc.want)
			}
		})
	}
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func TestFuseRRF(t *testing.T) {
	list1 := []string{"a", "b", "c", "d"}
	list2 := []string{"b", "e", "a", "f"}

	fused := fuseRRF([][]string{list1, list2}, 60, 5)

	if len(fused) == 0 {
		t.Fatal("expected fused results")
	}
	top2 := map[string]bool{fused[0].ID: true, fused[1].ID: true}
	if !top2["a"] || !top2["b"] {
		t.Errorf("expected 'a' and 'b' in top 2, got %s and %s", fused[0].ID, fused[1].ID)
	}
}

func TestFuseRRF_Empty(t *testing.T) {
	fused := fuseRRF(nil, 60, 10)
	if len(fused) != 0 {
		t.Errorf("expected empty result, got %d", len(fused))
	}
}

func TestFuseRRF_SingleList(t *testing.T) {
	list := []string{"x", "y", "z"}
	fused := fuseRRF([][]string{list}, 60, 3)
	if len(fused) != 3 {
		t.Fatalf("expected 3 results, got %d", len(fused))
	}
	if fused[0].ID != "x" {
		t.Errorf("expected 'x' first, got '%s'", fused[0].ID)
	}
}

func TestFuseRRF_TopK(t *testing.T) {
	list1 := []string{"a", "b", "c", "d", "e"}
	list2 := []string{"f", "g", "h"}
	fused := fuseRRF([][]string{list1, list2}, 60, 3)
	if len(fused) != 3 {
		t.Errorf("expected 3 results, got %d", len(fused))
	}
}

// countingEmbedder counts Embed/EmbedBatch calls so tests can assert that
// DisableEmbedding skips the embedding channel entirely.
type countingEmbedder struct{ calls int }

func (e *countingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.calls++
	return []float32{1, 0}, nil
}
func (e *countingEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	e.calls++
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}
func (e *countingEmbedder) Dim() int { return 2 }

func TestSearchSkillsDisableEmbeddingSkipsEmbedder(t *testing.T) {
	embed := &countingEmbedder{}
	s := NewLearningSearcher(&stubSkillStoreHyd{skills: []*domain.Skill{
		{ID: "s1", Name: "git-helper", Description: "git rebase guide"},
		{ID: "s2", Name: "docker-pro", Description: "docker build guide"},
	}}, nil, embed, nil)

	// Default options use the embedder.
	if _, err := s.SearchSkillsWithOpts(context.Background(), "git", 5, defaultSearchOptions()); err != nil {
		t.Fatal(err)
	}
	if embed.calls == 0 {
		t.Fatal("embedder should be used when embedding is enabled")
	}

	// DisableEmbedding: BM25 only — the embedder must not be called.
	embed.calls = 0
	opts := defaultSearchOptions()
	opts.DisableEmbedding = true
	res, err := s.SearchSkillsWithOpts(context.Background(), "git", 5, opts)
	if err != nil {
		t.Fatal(err)
	}
	if embed.calls != 0 {
		t.Fatalf("embedder called %d times despite DisableEmbedding", embed.calls)
	}
	if len(res) == 0 || res[0].ID != "s1" {
		t.Fatalf("expected s1 as BM25 top hit, got %+v", res)
	}
}

// Sanity: math.Sqrt used in cosineSimilarity is correct.
func TestCosineSimilarity_MathSqrt(t *testing.T) {
	a := []float32{3, 4}
	b := []float32{3, 4}
	got := textsim.CosineSimilarity(a, b)
	// |a| = |b| = 5, dot = 25, cos = 25/(5*5) = 1.0
	if math.Abs(float64(got)-1.0) > 0.001 {
		t.Errorf("expected 1.0, got %.4f", got)
	}
}
