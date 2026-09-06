package learn

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"nusashell/application/service/textsim"
	"nusashell/domain"
	clock "nusashell/pkg/time"
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
	// DisableEmbedding skips the embedding channel even when an embedder is
	// configured. The agent-loop search tools set this to avoid per-call
	// embedding cost; the Learning UI keeps the full hybrid path.
	DisableEmbedding bool
}

// defaultSearchOptions returns sensible defaults.
func DefaultSearchOptions() SearchOptions {
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
	skills   SkillCatalog
	memory   RecordStore
	embed    Embedder // nil = BM25-only
	graph    *LearningGraphService
	newIndex NewKeywordIndex
}

// NewLearningSearcher creates a searcher. embed, graph, and newIndex may be nil.
func NewLearningSearcher(skills SkillCatalog, memory RecordStore, embed Embedder, graph *LearningGraphService, newIndex NewKeywordIndex) *LearningSearcher {
	return &LearningSearcher{skills: skills, memory: memory, embed: embed, graph: graph, newIndex: newIndex}
}

// SearchSkills searches the skill library. Returns ranked results.
func (s *LearningSearcher) SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return s.searchSkillsWithOpts(ctx, query, topK, DefaultSearchOptions())
}

// SearchSkillsWithOpts searches with custom options (e.g. disable graph BFS).
func (s *LearningSearcher) SearchSkillsWithOpts(ctx context.Context, query string, topK int, opts SearchOptions) ([]SearchResult, error) {
	return s.searchSkillsWithOpts(ctx, query, topK, opts)
}

func (s *LearningSearcher) searchSkillsWithOpts(ctx context.Context, query string, topK int, opts SearchOptions) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}
	if s.skills == nil {
		return nil, nil
	}
	skills := s.skills.List()
	if len(skills) == 0 {
		return nil, nil
	}

	docs := make([]KeywordDoc, len(skills))
	for i, sk := range skills {
		docs[i] = KeywordDoc{ID: sk.ID, Text: sk.Name + " " + sk.Description + " " + sk.Content}
	}

	var lists [][]string

	// Channel 1: BM25
	if bm25IDs := s.keywordIDs(docs, query, topK*2); len(bm25IDs) > 0 {
		lists = append(lists, bm25IDs)
	}

	// Channel 2: Embedding cosine similarity (if available and enabled).
	if s.embed != nil && !opts.DisableEmbedding {
		embIDs, err := s.embeddingSearch(ctx, query, docs, topK*2)
		if err == nil && len(embIDs) > 0 {
			lists = append(lists, embIDs)
		}
	}

	// Channel 3: Graph BFS expansion from BM25/embedding seeds.
	// Finds related skills that don't lexically match the query.
	if s.graph != nil && opts.MaxHops > 0 {
		seeds := CollectSeeds(lists)
		if len(seeds) > 0 {
			expanded := s.graph.BFS(seeds, opts.MaxHops)
			if len(expanded) > 0 {
				lists = append(lists, expanded)
			}
		}
	}

	// Fuse via RRF
	fused := FuseRRF(lists, 60, topK)

	// Apply temporal decay (recency boost) if enabled
	if opts.ApplyDecay == nil || *opts.ApplyDecay {
		fused = s.ApplyTemporalDecay(fused, skills, nil)
	}

	out := make([]SearchResult, len(fused))
	for i, f := range fused {
		out[i] = SearchResult(f)
	}
	return out, nil
}

// SearchMemory searches the memory library. Returns ranked results.
func (s *LearningSearcher) SearchMemory(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return s.searchMemoryWithOpts(ctx, query, topK, DefaultSearchOptions())
}

// SearchMemoryWithOpts searches with custom options.
func (s *LearningSearcher) SearchMemoryWithOpts(ctx context.Context, query string, topK int, opts SearchOptions) ([]SearchResult, error) {
	return s.searchMemoryWithOpts(ctx, query, topK, opts)
}

func (s *LearningSearcher) searchMemoryWithOpts(ctx context.Context, query string, topK int, opts SearchOptions) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}
	if s.memory == nil {
		return nil, nil
	}
	entries := s.memory.List()
	if len(entries) == 0 {
		return nil, nil
	}
	live := make([]*domain.MemoryRecord, 0, len(entries))
	for _, e := range entries {
		if e != nil && e.Retrievable() {
			live = append(live, e)
		}
	}
	entries = live
	if len(entries) == 0 {
		return nil, nil
	}

	docs := make([]KeywordDoc, len(entries))
	for i, e := range entries {
		docs[i] = KeywordDoc{ID: e.ID, Text: recordSearchText(e)}
	}

	var lists [][]string

	// Channel 1: BM25
	if bm25IDs := s.keywordIDs(docs, query, topK*2); len(bm25IDs) > 0 {
		lists = append(lists, bm25IDs)
	}

	// Channel 2: Embedding cosine similarity (if available and enabled).
	if s.embed != nil && !opts.DisableEmbedding {
		embIDs, err := s.embeddingSearch(ctx, query, docs, topK*2)
		if err == nil && len(embIDs) > 0 {
			lists = append(lists, embIDs)
		}
	}

	// Channel 3: Graph BFS expansion
	if s.graph != nil && opts.MaxHops > 0 {
		seeds := CollectSeeds(lists)
		if len(seeds) > 0 {
			expanded := s.graph.BFS(seeds, opts.MaxHops)
			if len(expanded) > 0 {
				lists = append(lists, expanded)
			}
		}
	}

	// Fuse via RRF
	fused := FuseRRF(lists, 60, topK)

	// Apply temporal decay
	if opts.ApplyDecay == nil || *opts.ApplyDecay {
		fused = s.ApplyTemporalDecay(fused, nil, entries)
	}

	out := make([]SearchResult, len(fused))
	for i, f := range fused {
		out[i] = SearchResult(f)
	}
	return out, nil
}

func (s *LearningSearcher) keywordIDs(docs []KeywordDoc, query string, topK int) []string {
	if s.newIndex == nil || len(docs) == 0 {
		return nil
	}
	results := s.newIndex(docs).Search(query, topK)
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	return ids
}

// collectSeeds deduplicates IDs from all ranked lists for BFS seeding.
func CollectSeeds(lists [][]string) []string {
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

// ApplyTemporalDecay applies a recency boost to fused results. Entries
// accessed or created more recently get a small score bump. The decay
// function is: score *= (1 + decayFactor * recencyWeight) where
// recencyWeight is 1.0 for entries accessed today, decaying to 0 over
// 30 days. This mirrors memex's temporal decay multiplier.
func (s *LearningSearcher) ApplyTemporalDecay(fused []RRFResult, skills []*domain.Skill, memories []*domain.MemoryRecord) []RRFResult {
	if len(fused) == 0 {
		return fused
	}
	now := clock.NewTime().Time()
	halfLifeDays := 30.0 // 30-day half-life
	decayFactor := 0.15  // max 15% boost

	// Build lookup maps
	skillMap := make(map[string]*domain.Skill, len(skills))
	for _, sk := range skills {
		skillMap[sk.ID] = sk
	}
	memMap := make(map[string]*domain.MemoryRecord, len(memories))
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
			lastUsed = m.LastConfirmed
			if lastUsed.IsZero() {
				lastUsed = m.UpdatedAt
			}
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
func (s *LearningSearcher) embeddingSearch(ctx context.Context, query string, docs []KeywordDoc, topK int) ([]string, error) {
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
		results[i] = result{docs[i].ID, textsim.CosineSimilarity(qVec, dv)}
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

// RRFResult is a fused search result from multiple channels.
type RRFResult struct {
	ID    string
	Score float64
}

// fuseRRF merges multiple ranked lists using Reciprocal Rank Fusion.
// k is the RRF constant (typically 60). Returns fused results sorted by
// combined RRF score descending, limited to topK.
func FuseRRF(lists [][]string, k float64, topK int) []RRFResult {
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
	out := make([]RRFResult, 0, len(fused))
	for _, e := range fused {
		out = append(out, RRFResult{ID: e.id, Score: e.score})
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
		if !hasEmbedding {
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

func recordSearchText(e *domain.MemoryRecord) string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(strings.Join([]string{e.Body, e.Subject, e.Predicate, e.Object, e.Type, e.Scope.Project}, " "))
}
