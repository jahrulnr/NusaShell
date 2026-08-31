// Consolidation merges memory entries that are semantically near-duplicates.
//
// When two memory entries have cosine similarity above the consolidation
// threshold (default 0.92), they are merged: the newer entry absorbs the
// older entry's tags and is kept; the older entry is deleted. Edges that
// referenced the deleted entry are rewired to the surviving entry.
//
// This runs as part of the lifecycle manager's periodic prune cycle, not
// per-turn, to avoid embedding spam. The embedding cache makes re-runs
// cheap — only new/changed entries need embedding.
package application

import (
	"context"
	"fmt"
	"strings"

	"nusashell/application/service/textsim"
	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
)

// ConsolidationConfig controls memory consolidation behavior.
type ConsolidationConfig struct {
	// SimilarityThreshold is the cosine similarity above which two entries
	// are considered near-duplicates and merged. Default 0.92.
	SimilarityThreshold float32
	// MinContentLen is the minimum content length to consider for
	// consolidation. Short entries (e.g. "yes", "ok") are skipped to
	// avoid false merges. Default 20.
	MinContentLen int
}

// DefaultConsolidationConfig returns sensible defaults.
func DefaultConsolidationConfig() ConsolidationConfig {
	return ConsolidationConfig{
		SimilarityThreshold: domain.DefaultConsolidationSimilarityThreshold,
		MinContentLen:       domain.DefaultConsolidationMinContentLen,
	}
}

// Consolidator merges near-duplicate memory entries.
type Consolidator struct {
	memory  MemoryStore
	graph   *LearningGraphService
	embed   Embedder
	cache   *jsonstore.EmbeddingCache
	cfg     ConsolidationConfig
	modelID string
}

// NewConsolidator creates a consolidator. embed and cache may be nil —
// in that case consolidation is skipped (no way to compute similarity).
func NewConsolidator(
	memory MemoryStore,
	graph *LearningGraphService,
	embed Embedder,
	cache *jsonstore.EmbeddingCache,
	cfg ConsolidationConfig,
	modelID string,
) *Consolidator {
	if cfg.SimilarityThreshold == 0 {
		cfg = DefaultConsolidationConfig()
	}
	return &Consolidator{
		memory:  memory,
		graph:   graph,
		embed:   embed,
		cache:   cache,
		cfg:     cfg,
		modelID: modelID,
	}
}

// ConsolidateResult reports what was merged.
type ConsolidateResult struct {
	Merged   int // number of entries deleted (absorbed into survivors)
	Examined int // number of pairs compared
}

// Consolidate scans all memory entries, embeds them (via cache), and
// merges pairs with cosine similarity above the threshold. Returns the
// number of entries merged. Skips entirely if no embedder is configured.
func (c *Consolidator) Consolidate(ctx context.Context) (*ConsolidateResult, error) {
	if c.embed == nil || c.cache == nil {
		return &ConsolidateResult{}, nil
	}
	entries := c.memory.List()
	// Filter to entries with sufficient content length.
	var candidates []*domain.MemoryEntry
	for _, e := range entries {
		if len(strings.TrimSpace(e.Content)) >= c.cfg.MinContentLen {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) < 2 {
		return &ConsolidateResult{Examined: 0}, nil
	}

	// Embed all candidates (via cache — only misses hit the API).
	texts := make([]string, len(candidates))
	for i, e := range candidates {
		texts[i] = e.Content
	}
	vectors, err := c.embedWithCache(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("consolidate: embed failed: %w", err)
	}

	result := &ConsolidateResult{Examined: len(candidates) * (len(candidates) - 1) / 2}
	merged := map[string]bool{} // IDs already absorbed

	for i := 0; i < len(candidates); i++ {
		if merged[candidates[i].ID] {
			continue
		}
		if vectors[i] == nil {
			continue
		}
		for j := i + 1; j < len(candidates); j++ {
			if merged[candidates[j].ID] {
				continue
			}
			if vectors[j] == nil {
				continue
			}
			sim := textsim.CosineSimilarity(vectors[i], vectors[j])
			if sim >= c.cfg.SimilarityThreshold {
				// Merge j into i: i survives, j is absorbed.
				c.mergeEntries(candidates[i], candidates[j])
				merged[candidates[j].ID] = true
				result.Merged++
			}
		}
	}

	return result, nil
}

// mergeEntries absorbs the old entry into the new entry: union tags,
// rewire edges, delete the old entry.
func (c *Consolidator) mergeEntries(survivor, absorbed *domain.MemoryEntry) {
	survivor.MergeFrom(absorbed)
	_ = c.memory.Save(survivor)

	// Rewire edges: any edge pointing to absorbed → point to survivor
	if c.graph != nil {
		for _, e := range c.graph.AllEdges() {
			if e.TargetID == absorbed.ID {
				e.TargetID = survivor.ID
				_ = c.graph.SaveEdge(e)
			}
			if e.SourceID == absorbed.ID {
				e.SourceID = survivor.ID
				_ = c.graph.SaveEdge(e)
			}
		}
	}

	// Delete the absorbed entry
	_ = c.memory.Delete(absorbed.ID)
}

// embedWithCache returns vectors for all texts, using the cache for hits.
func (c *Consolidator) embedWithCache(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, misses := c.cache.GetBatch(c.modelID, texts)
	if len(misses) == 0 {
		return vectors, nil
	}
	missTexts := make([]string, len(misses))
	for i, idx := range misses {
		missTexts[i] = texts[idx]
	}
	newVecs, err := c.embed.EmbedBatch(ctx, missTexts)
	if err != nil {
		return nil, err
	}
	for i, idx := range misses {
		if newVecs[i] != nil {
			vectors[idx] = newVecs[i]
			_ = c.cache.Put(c.modelID, missTexts[i], newVecs[i])
		}
	}
	return vectors, nil
}
