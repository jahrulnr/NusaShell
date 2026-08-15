package application

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
)

// SearchResult is a ranked search hit from the learning search layer.
type SearchResult struct {
	ID    string
	Score float64
}

// SearchOptions controls hybrid search behavior. Zero value = defaults.
type SearchOptions struct {
	// MaxHops is the BFS expansion depth from BM25/embedding matches.
	// Default 2. Set 0 to disable graph expansion.
	MaxHops int
	// ApplyDecay enables temporal decay on fused results. Default true.
	ApplyDecay *bool
}

// defaultSearchOptions returns sensible defaults.
func defaultSearchOptions() SearchOptions {
	t := true
	return SearchOptions{MaxHops: 2, ApplyDecay: &t}
}

// LearningSearcher provides hybrid search over skills and memory entries.
// It combines in-memory BM25 (keyword) with optional embedding cosine
// similarity (semantic) and graph BFS expansion, fused via Reciprocal
// Rank Fusion. Temporal decay is applied to fused results to boost
// recently-used entries. When no Embedder is configured, it falls back
// to BM25 + graph only.
type LearningSearcher struct {
	skills SkillStore
	memory MemoryStore
	embed  Embedder // nil = BM25-only
	graph  *LearningGraphService
}

// NewLearningSearcher creates a searcher. embed and graph may be nil
// for BM25-only search.
func NewLearningSearcher(skills SkillStore, memory MemoryStore, embed Embedder, graph *LearningGraphService) *LearningSearcher {
	return &LearningSearcher{skills: skills, memory: memory, embed: embed, graph: graph}
}

// SearchSkills searches the skill library. Returns ranked results.
func (s *LearningSearcher) SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return s.searchSkillsWithOpts(ctx, query, topK, defaultSearchOptions())
}

// SearchSkillsWithOpts searches with custom options (e.g. disable graph BFS).
func (s *LearningSearcher) SearchSkillsWithOpts(ctx context.Context, query string, topK int, opts SearchOptions) ([]SearchResult, error) {
	return s.searchSkillsWithOpts(ctx, query, topK, opts)
}

func (s *LearningSearcher) searchSkillsWithOpts(ctx context.Context, query string, topK int, opts SearchOptions) ([]SearchResult, error) {
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

	// Channel 3: Graph BFS expansion from BM25/embedding seeds.
	// Finds related skills that don't lexically match the query.
	if s.graph != nil && opts.MaxHops > 0 {
		seeds := collectSeeds(lists)
		if len(seeds) > 0 {
			expanded := s.graph.BFS(seeds, opts.MaxHops)
			if len(expanded) > 0 {
				lists = append(lists, expanded)
			}
		}
	}

	// Fuse via RRF
	fused := fuseRRF(lists, 60, topK)

	// Apply temporal decay (recency boost) if enabled
	if opts.ApplyDecay == nil || *opts.ApplyDecay {
		fused = s.applyTemporalDecay(fused, skills, nil)
	}

	out := make([]SearchResult, len(fused))
	for i, f := range fused {
		out[i] = SearchResult{ID: f.ID, Score: f.Score}
	}
	return out, nil
}

// SearchMemory searches the memory library. Returns ranked results.
func (s *LearningSearcher) SearchMemory(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return s.searchMemoryWithOpts(ctx, query, topK, defaultSearchOptions())
}

// SearchMemoryWithOpts searches with custom options.
func (s *LearningSearcher) SearchMemoryWithOpts(ctx context.Context, query string, topK int, opts SearchOptions) ([]SearchResult, error) {
	return s.searchMemoryWithOpts(ctx, query, topK, opts)
}

func (s *LearningSearcher) searchMemoryWithOpts(ctx context.Context, query string, topK int, opts SearchOptions) ([]SearchResult, error) {
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

	// Channel 3: Graph BFS expansion
	if s.graph != nil && opts.MaxHops > 0 {
		seeds := collectSeeds(lists)
		if len(seeds) > 0 {
			expanded := s.graph.BFS(seeds, opts.MaxHops)
			if len(expanded) > 0 {
				lists = append(lists, expanded)
			}
		}
	}

	// Fuse via RRF
	fused := fuseRRF(lists, 60, topK)

	// Apply temporal decay
	if opts.ApplyDecay == nil || *opts.ApplyDecay {
		fused = s.applyTemporalDecay(fused, nil, entries)
	}

	out := make([]SearchResult, len(fused))
	for i, f := range fused {
		out[i] = SearchResult{ID: f.ID, Score: f.Score}
	}
	return out, nil
}

// collectSeeds deduplicates IDs from all ranked lists for BFS seeding.
func collectSeeds(lists [][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// applyTemporalDecay applies a recency boost to fused results. Entries
// accessed or created more recently get a small score bump. The decay
// function is: score *= (1 + decayFactor * recencyWeight) where
// recencyWeight is 1.0 for entries accessed today, decaying to 0 over
// 30 days. This mirrors memex's temporal decay multiplier.
func (s *LearningSearcher) applyTemporalDecay(fused []rrfResult, skills []*domain.Skill, memories []*domain.MemoryEntry) []rrfResult {
	if len(fused) == 0 {
		return fused
	}
	now := time.Now()
	halfLifeDays := 30.0 // 30-day half-life
	decayFactor := 0.15  // max 15% boost

	// Build lookup maps
	skillMap := make(map[string]*domain.Skill, len(skills))
	for _, sk := range skills {
		skillMap[sk.ID] = sk
	}
	memMap := make(map[string]*domain.MemoryEntry, len(memories))
	for _, m := range memories {
		memMap[m.ID] = m
	}

	for i := range fused {
		var lastUsed time.Time
		if sk, ok := skillMap[fused[i].ID]; ok {
			lastUsed = sk.LastUsedAt
			if lastUsed.IsZero() {
				lastUsed = sk.UpdatedAt
			}
		} else if m, ok := memMap[fused[i].ID]; ok {
			lastUsed = m.CreatedAt
		}
		if lastUsed.IsZero() {
			continue
		}
		daysSince := now.Sub(lastUsed).Hours() / 24
		if daysSince < 0 {
			daysSince = 0
		}
		// Exponential decay: weight = 0.5^(days / halfLife)
		recencyWeight := math.Pow(0.5, daysSince/halfLifeDays)
		fused[i].Score *= 1.0 + decayFactor*recencyWeight
	}

	// Re-sort after decay adjustment
	sort.Slice(fused, func(i, j int) bool { return fused[i].Score > fused[j].Score })
	return fused
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
			if m.Kind == domain.ModelKindEmbedding {
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
