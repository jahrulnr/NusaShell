// Package application — learning edge builder.
//
// The edge builder pre-computes connections between memory entries and
// skills, storing them as LearningEdges in the graph. This runs as a
// background job (not per-search) so the frontend graph can render edges
// instantly without O(n²) client-side computation.
//
// Two edge discovery methods:
//
//  1. Embedding similarity (Layer 2): embed all memory + skill texts,
//     compute pairwise cosine similarity, create `related` edges for
//     pairs above the threshold (default 0.85). Uses the embedding cache
//     to avoid re-embedding on every run.
//
//  2. Token overlap (Layer 3, Hermes-style): for memory ↔ skill pairs,
//     compute Jaccard similarity on token sets (tokens ≥3 chars). Create
//     `related` edges for pairs with overlap ≥ 0.3. Zero API cost —
//     works even without an embedder.
//
// The builder is idempotent: running it again strengthens existing edges
// via CombineWeights instead of creating duplicates.
package application

import (
	"context"
	"strings"
	"sync"

	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
)

// EdgeBuilderConfig controls edge discovery thresholds.
type EdgeBuilderConfig struct {
	// EmbeddingThreshold is the cosine similarity above which two entries
	// get a `related` edge. Default 0.85.
	EmbeddingThreshold float32
	// TokenOverlapThreshold is the Jaccard similarity above which a
	// memory and skill get a `related` edge. Default 0.3.
	TokenOverlapThreshold float32
	// MinTokenLen is the minimum token length for overlap matching.
	// Default 3 (matches Hermes).
	MinTokenLen int
}

// DefaultEdgeBuilderConfig returns sensible defaults.
func DefaultEdgeBuilderConfig() EdgeBuilderConfig {
	return EdgeBuilderConfig{
		EmbeddingThreshold:    0.85,
		TokenOverlapThreshold: 0.3,
		MinTokenLen:           3,
	}
}

// EdgeBuilder pre-computes learning edges between memory fragments and
// skills. Fragments are the graph's memory nodes (one node per fact);
// primary memory is a single working-set document, not a graph node with
// meaningful similarity edges, so it is intentionally excluded.
type EdgeBuilder struct {
	fragments FragmentStore
	skills    SkillStore
	graph     *LearningGraphService
	embed     Embedder
	cache     *jsonstore.EmbeddingCache
	cfg       EdgeBuilderConfig
	modelID   string
}

// NewEdgeBuilder creates an edge builder. embed and cache may be nil —
// in that case only token overlap (Layer 3) runs.
func NewEdgeBuilder(
	fragments FragmentStore,
	skills SkillStore,
	graph *LearningGraphService,
	embed Embedder,
	cache *jsonstore.EmbeddingCache,
	cfg EdgeBuilderConfig,
	modelID string,
) *EdgeBuilder {
	if cfg.EmbeddingThreshold == 0 {
		cfg = DefaultEdgeBuilderConfig()
	}
	return &EdgeBuilder{
		fragments: fragments,
		skills:    skills,
		graph:     graph,
		embed:     embed,
		cache:     cache,
		cfg:       cfg,
		modelID:   modelID,
	}
}

// Build computes all edges and stores them in the graph. This is the
// main entry point — call it as a background job after memory/skill
// changes, or on a periodic timer.
func (b *EdgeBuilder) Build(ctx context.Context) error {
	// Layer 3 (token overlap) always runs — it's free.
	b.buildTokenOverlapEdges()

	// Layer 2 (embedding similarity) only runs if embedder + cache are
	// configured.
	if b.embed != nil && b.cache != nil {
		if err := b.buildEmbeddingEdges(ctx); err != nil {
			return err
		}
	}
	return nil
}

// buildTokenOverlapEdges creates edges between memory fragments and
// skills, and between related fragments, based on token Jaccard
// similarity. Zero API cost.
//
// Fragment↔fragment edges are what make the graph read as a chain of
// related facts (A relevant to B, B to C, …) instead of every memory
// node clinging to the skill hubs; without them the token-overlap pass
// only produces memory↔skill spokes.
func (b *EdgeBuilder) buildTokenOverlapEdges() {
	memories := b.fragments.List(domain.FragmentSearchFilter{Limit: 500})
	skills := b.skills.List()
	minLen := b.cfg.MinTokenLen
	if minLen <= 0 {
		minLen = 3
	}

	// Pre-tokenize everything once so pairwise comparison is a cheap set
	// intersection instead of re-splitting text per pair.
	type memTokens struct {
		id     string
		tokens map[string]bool
	}
	memToks := make([]memTokens, 0, len(memories))
	for _, m := range memories {
		toks := tokenizeForOverlap(m.Content, minLen)
		if len(toks) == 0 {
			continue
		}
		memToks = append(memToks, memTokens{id: m.ID, tokens: toks})
	}
	type skillTokens struct {
		id     string
		tokens map[string]bool
	}
	skillToks := make([]skillTokens, len(skills))
	for i, s := range skills {
		skillToks[i] = skillTokens{
			id:     s.ID,
			tokens: tokenizeForOverlap(s.Name+" "+s.Description+" "+s.Content, minLen),
		}
	}

	// Memory ↔ skill (existing spokes).
	for _, mt := range memToks {
		for _, st := range skillToks {
			if len(st.tokens) == 0 {
				continue
			}
			jaccard := jaccardSimilarity(mt.tokens, st.tokens)
			if jaccard >= b.cfg.TokenOverlapThreshold {
				weight := float64(jaccard) * 0.5 // cap at 0.5 for token overlap
				_, _ = b.graph.AddEdge(mt.id, st.id, domain.EdgeRelated, weight)
			}
		}
	}

	// Memory ↔ memory (neuron-like chains between related facts). Uses
	// the same threshold; identical-fact fragments land on the same
	// cluster and strengthen over time via CombineWeights.
	for i := 0; i < len(memToks); i++ {
		for j := i + 1; j < len(memToks); j++ {
			jaccard := jaccardSimilarity(memToks[i].tokens, memToks[j].tokens)
			if jaccard >= b.cfg.TokenOverlapThreshold {
				weight := float64(jaccard) * 0.5
				_, _ = b.graph.AddEdge(memToks[i].id, memToks[j].id, domain.EdgeRelated, weight)
			}
		}
	}
}

// buildEmbeddingEdges creates edges between entries with cosine similarity
// above the threshold. Uses the embedding cache to avoid re-embedding.
func (b *EdgeBuilder) buildEmbeddingEdges(ctx context.Context) error {
	memories := b.fragments.List(domain.FragmentSearchFilter{Limit: 500})
	skills := b.skills.List()

	// Collect all texts to embed
	type entry struct {
		id   string
		text string
	}
	var allEntries []entry
	for _, m := range memories {
		allEntries = append(allEntries, entry{m.ID, m.Content})
	}
	for _, s := range skills {
		allEntries = append(allEntries, entry{s.ID, s.Name + " " + s.Description + " " + s.Content})
	}
	if len(allEntries) < 2 {
		return nil
	}

	// Get all vectors (from cache or embed misses)
	texts := make([]string, len(allEntries))
	for i, e := range allEntries {
		texts[i] = e.text
	}

	vectors, err := b.embedWithCache(ctx, texts)
	if err != nil {
		return err
	}

	// Compute pairwise cosine similarity, create edges above threshold
	threshold := b.cfg.EmbeddingThreshold
	var mu sync.Mutex
	edgesCreated := 0

	for i := 0; i < len(allEntries); i++ {
		if vectors[i] == nil {
			continue
		}
		for j := i + 1; j < len(allEntries); j++ {
			if vectors[j] == nil {
				continue
			}
			sim := cosineSimilarity(vectors[i], vectors[j])
			if sim >= threshold {
				weight := float64(sim) * 0.8 // scale to [0, 0.8]
				_, _ = b.graph.AddEdge(allEntries[i].id, allEntries[j].id, domain.EdgeRelated, weight)
				mu.Lock()
				edgesCreated++
				mu.Unlock()
			}
		}
	}

	return nil
}

// embedWithCache returns vectors for all texts, using the cache for hits
// and embedding only the misses. Misses are stored in the cache after
// embedding.
func (b *EdgeBuilder) embedWithCache(ctx context.Context, texts []string) ([][]float32, error) {
	// Check cache for all texts
	vectors, misses := b.cache.GetBatch(b.modelID, texts)
	if len(misses) == 0 {
		return vectors, nil
	}

	// Embed only the misses
	missTexts := make([]string, len(misses))
	for i, idx := range misses {
		missTexts[i] = texts[idx]
	}
	newVecs, err := b.embed.EmbedBatch(ctx, missTexts)
	if err != nil {
		return nil, err
	}

	// Store misses in cache
	for i, idx := range misses {
		if newVecs[i] != nil {
			vectors[idx] = newVecs[i]
			_ = b.cache.Put(b.modelID, missTexts[i], newVecs[i])
		}
	}

	return vectors, nil
}

// tokenizeForOverlap returns a set of tokens (lowercase, ≥minLen chars)
// from the input text. Matches Hermes' _tokenize pattern.
func tokenizeForOverlap(text string, minLen int) map[string]bool {
	tokens := map[string]bool{}
	for _, t := range strings.Fields(strings.ToLower(text)) {
		t = strings.Trim(t, ".,;:!?\"'()[]{}")
		if len(t) >= minLen {
			tokens[t] = true
		}
	}
	return tokens
}

// jaccardSimilarity computes |A ∩ B| / |A ∪ B| for two token sets.
func jaccardSimilarity(a, b map[string]bool) float32 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for t := range a {
		if b[t] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float32(intersection) / float32(union)
}
