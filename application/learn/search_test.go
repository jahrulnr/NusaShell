package learn

import (
	"context"
	"math"
	"testing"

	"nusashell/application/service/textsim"
	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
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

	fused := FuseRRF([][]string{list1, list2}, 60, 5)

	if len(fused) == 0 {
		t.Fatal("expected fused results")
	}
	top2 := map[string]bool{fused[0].ID: true, fused[1].ID: true}
	if !top2["a"] || !top2["b"] {
		t.Errorf("expected 'a' and 'b' in top 2, got %s and %s", fused[0].ID, fused[1].ID)
	}
}

func TestFuseRRF_Empty(t *testing.T) {
	fused := FuseRRF(nil, 60, 10)
	if len(fused) != 0 {
		t.Errorf("expected empty result, got %d", len(fused))
	}
}

func TestFuseRRF_SingleList(t *testing.T) {
	list := []string{"x", "y", "z"}
	fused := FuseRRF([][]string{list}, 60, 3)
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
	fused := FuseRRF([][]string{list1, list2}, 60, 3)
	if len(fused) != 3 {
		t.Errorf("expected 3 results, got %d", len(fused))
	}
}

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

type listSkillCatalog struct{ skills []*domain.Skill }

func (s *listSkillCatalog) List() []*domain.Skill { return s.skills }
func (s *listSkillCatalog) Get(id, ownedBy string) (*domain.Skill, error) {
	return nil, nil
}
func (s *listSkillCatalog) Save(sk *domain.Skill) error { return nil }

func testKeywordIndex(docs []KeywordDoc) KeywordIndex {
	bm25docs := make([]jsonstore.BM25Doc, len(docs))
	for i, d := range docs {
		bm25docs[i] = jsonstore.BM25Doc{ID: d.ID, Text: d.Text}
	}
	return bm25KeywordIndex{idx: jsonstore.NewBM25(bm25docs)}
}

type bm25KeywordIndex struct{ idx *jsonstore.BM25 }

func (b bm25KeywordIndex) Search(query string, topK int) []SearchResult {
	hits := b.idx.Search(query, topK)
	out := make([]SearchResult, len(hits))
	for i, h := range hits {
		out[i] = SearchResult{ID: h.ID, Score: h.Score}
	}
	return out
}

func TestSearchSkillsDisableEmbeddingSkipsEmbedder(t *testing.T) {
	embed := &countingEmbedder{}
	s := NewLearningSearcher(&listSkillCatalog{skills: []*domain.Skill{
		{ID: "s1", Name: "git-helper", Description: "git rebase guide"},
		{ID: "s2", Name: "docker-pro", Description: "docker build guide"},
	}}, nil, embed, nil, testKeywordIndex)

	if _, err := s.SearchSkillsWithOpts(context.Background(), "git", 5, DefaultSearchOptions()); err != nil {
		t.Fatal(err)
	}
	if embed.calls == 0 {
		t.Fatal("embedder should be used when embedding is enabled")
	}

	embed.calls = 0
	opts := DefaultSearchOptions()
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

func TestCosineSimilarity_MathSqrt(t *testing.T) {
	a := []float32{3, 4}
	b := []float32{3, 4}
	got := textsim.CosineSimilarity(a, b)
	if math.Abs(float64(got)-1.0) > 0.001 {
		t.Errorf("expected 1.0, got %.4f", got)
	}
}
