package application

import (
	"context"
	"math"
	"sort"
	"strings"

	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
)

// SearchResult is a ranked search hit from the learning search layer.
type SearchResult struct {
	ID    string
	Score float64
}

// LearningSearcher provides hybrid search over skills and memory entries.
// It combines in-memory BM25 (keyword) with optional embedding cosine
// similarity (semantic), fused via Reciprocal Rank Fusion. When no
// Embedder is configured, it falls back to BM25-only.
type LearningSearcher struct {
	skills SkillStore
	memory MemoryStore
	embed  Embedder // nil = BM25-only
}

// NewLearningSearcher creates a searcher. embed may be nil for BM25-only.
func NewLearningSearcher(skills SkillStore, memory MemoryStore, embed Embedder) *LearningSearcher {
	return &LearningSearcher{skills: skills, memory: memory, embed: embed}
}

// SearchSkills searches the skill library. Returns ranked results.
func (s *LearningSearcher) SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}
	skills := s.skills.List()
	if len(skills) == 0 {
		return nil, nil
	}

	docs := make([]jsonstore.BM25Doc, len(skills))
	for i, sk := range skills {
		docs[i] = jsonstore.BM25Doc{ID: sk.ID, Text: sk.Name + " " + sk.Description + " " + sk.Content}
	}

	var lists [][]string

	// Channel 1: BM25
	bm25 := jsonstore.NewBM25(docs)
	bm25Results := bm25.Search(query, topK*2)
	bm25IDs := make([]string, len(bm25Results))
	for i, r := range bm25Results {
		bm25IDs[i] = r.ID
	}
	lists = append(lists, bm25IDs)

	// Channel 2: Embedding cosine similarity (if available)
	if s.embed != nil {
		embIDs, err := s.embeddingSearch(ctx, query, docs, topK*2)
		if err == nil && len(embIDs) > 0 {
			lists = append(lists, embIDs)
		}
	}

	// Fuse via RRF
	fused := fuseRRF(lists, 60, topK)
	out := make([]SearchResult, len(fused))
	for i, f := range fused {
		out[i] = SearchResult{ID: f.ID, Score: f.Score}
	}
	return out, nil
}

// SearchMemory searches the memory library. Returns ranked results.
func (s *LearningSearcher) SearchMemory(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}
	entries := s.memory.List()
	if len(entries) == 0 {
		return nil, nil
	}

	docs := make([]jsonstore.BM25Doc, len(entries))
	for i, e := range entries {
		docs[i] = jsonstore.BM25Doc{ID: e.ID, Text: e.Content + " " + strings.Join(e.Tags, " ")}
	}

	var lists [][]string

	// Channel 1: BM25
	bm25 := jsonstore.NewBM25(docs)
	bm25Results := bm25.Search(query, topK*2)
	bm25IDs := make([]string, len(bm25Results))
	for i, r := range bm25Results {
		bm25IDs[i] = r.ID
	}
	lists = append(lists, bm25IDs)

	// Channel 2: Embedding cosine similarity (if available)
	if s.embed != nil {
		embIDs, err := s.embeddingSearch(ctx, query, docs, topK*2)
		if err == nil && len(embIDs) > 0 {
			lists = append(lists, embIDs)
		}
	}

	// Fuse via RRF
	fused := fuseRRF(lists, 60, topK)
	out := make([]SearchResult, len(fused))
	for i, f := range fused {
		out[i] = SearchResult{ID: f.ID, Score: f.Score}
	}
	return out, nil
}

// embeddingSearch embeds the query and all docs, computes cosine similarity,
// and returns ranked doc IDs.
func (s *LearningSearcher) embeddingSearch(ctx context.Context, query string, docs []jsonstore.BM25Doc, topK int) ([]string, error) {
	qVec, err := s.embed.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	texts := make([]string, len(docs))
	for i, d := range docs {
		texts[i] = d.Text
	}
	docVecs, err := s.embed.EmbedBatch(ctx, texts)
	if err != nil {
		return nil, err
	}
	type result struct {
		id    string
		score float32
	}
	results := make([]result, len(docVecs))
	for i, dv := range docVecs {
		results[i] = result{docs[i].ID, cosineSimilarity(qVec, dv)}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })
	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.id
	}
	return out, nil
}

// cosineSimilarity computes cosine similarity between two float32 vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(na))*math.Sqrt(float64(nb)))
}

// rrfResult is a fused search result from multiple channels.
type rrfResult struct {
	ID    string
	Score float64
}

// fuseRRF merges multiple ranked lists using Reciprocal Rank Fusion.
// k is the RRF constant (typically 60). Returns fused results sorted by
// combined RRF score descending, limited to topK.
func fuseRRF(lists [][]string, k float64, topK int) []rrfResult {
	type entry struct {
		id    string
		score float64
	}
	fused := make(map[string]*entry)
	for _, list := range lists {
		for rank, id := range list {
			e, ok := fused[id]
			if !ok {
				e = &entry{id: id}
				fused[id] = e
			}
			e.score += 1.0 / (k + float64(rank+1))
		}
	}
	out := make([]rrfResult, 0, len(fused))
	for _, e := range fused {
		out = append(out, rrfResult{ID: e.id, Score: e.score})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out
}

// ResolveEmbedder finds an embedding provider. If embeddingProviderID is
// non-empty, it tries that specific provider first. Otherwise it auto-detects
// the first enabled provider with an embedding model. Returns nil if no
// embedding-capable provider is configured.
func ResolveEmbedder(providers ProviderStore, creds CredentialStore, factory EmbedderFactory, embeddingProviderID string) Embedder {
	all := providers.List()
	// If a specific provider is selected, try it first.
	if embeddingProviderID != "" {
		for _, p := range all {
			if p.ID != embeddingProviderID || !p.Enabled {
				continue
			}
			key, _, _ := creds.Get(p.ID)
			embed, err := factory(p, key)
			if err == nil && embed != nil {
				return embed
			}
		}
	}
	// Auto-detect: first enabled provider with an embedding model.
	for _, p := range all {
		if !p.Enabled {
			continue
		}
		hasEmbedding := false
		for _, m := range p.Models {
			if m.IsEmbedding {
				hasEmbedding = true
				break
			}
		}
		if !hasEmbedding && p.Kind != domain.ProviderOllama {
			continue
		}
		key, _, _ := creds.Get(p.ID)
		embed, err := factory(p, key)
		if err != nil || embed == nil {
			continue
		}
		return embed
	}
	return nil
}
