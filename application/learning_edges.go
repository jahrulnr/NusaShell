// Package application — learning edge builder.
//
// The edge builder pre-computes connections between memory entries and
// skills, storing them as LearningEdges in the graph. This runs as a
// background job (not per-search) so the frontend graph can render edges
// instantly without O(n²) client-side computation.
//
// Edge discovery combines three deterministic/optional signals:
//
//  1. Embedding similarity (Layer 2): embed all memory + skill texts,
//     compute pairwise cosine similarity, create `related` edges for
//     pairs above the threshold (default 0.85). Uses the embedding cache
//     to avoid re-embedding on every run.
//
//  2. Metadata and token overlap (Layer 3, Hermes-style): for memory ↔ skill
//     and memory ↔ memory pairs, compare frontmatter/name metadata and token
//     sets. Create `related` edges for pairs with enough overlap. Zero API
//     cost — works even without an embedder.
//
//  3. Used-with edges are recorded by the application turn runner when
//     multiple memory/skill nodes are observed by successful tools in one
//     turn; they are not inferred from similarity here.
//
// Token overlap uses tokens ≥3 characters and creates `related` edges for
// pairs with overlap ≥ 0.3. It is free and works without an embedder.
//
// The builder is idempotent: running it again strengthens existing edges
// via CombineWeights instead of creating duplicates.
package application

import (
	"context"
	"strings"
	"sync"

	"nusashell/application/service/textsim"
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
	primary   PrimaryStore
	graph     *LearningGraphService
	embed     Embedder
	cache     *jsonstore.EmbeddingCache
	cfg       EdgeBuilderConfig
	modelID   string
	buildMu   sync.Mutex
}

// NewEdgeBuilder creates an edge builder. embed and cache may be nil —
// in that case deterministic metadata and token-overlap discovery still runs.
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

// SetPrimaryStore supplies the primary-memory node catalog used while
// pruning stale edges. It is separate from the constructor so tests and
// lightweight callers that only have fragments and skills remain valid.
func (b *EdgeBuilder) SetPrimaryStore(primary PrimaryStore) {
	if b == nil {
		return
	}
	b.buildMu.Lock()
	b.primary = primary
	b.buildMu.Unlock()
}

// SetEmbedder installs the lazily resolved embedder once settings are known.
// The same build mutex protects this assignment from concurrent graph RPCs.
func (b *EdgeBuilder) SetEmbedder(embed Embedder, modelID string) {
	if b == nil || embed == nil {
		return
	}
	b.buildMu.Lock()
	if b.embed == nil {
		b.embed = embed
		b.modelID = modelID
	}
	b.buildMu.Unlock()
}

// Build computes all edges and stores them in the graph. This is the
// main entry point — call it as a background job after memory/skill
// changes, or on a periodic timer.
func (b *EdgeBuilder) Build(ctx context.Context) error {
	if b == nil || b.graph == nil || b.fragments == nil || b.skills == nil {
		return nil
	}
	b.buildMu.Lock()
	defer b.buildMu.Unlock()

	// A fragment or skill can be deleted without touching the edge log. Drop
	// those records before building so the graph RPC does not keep filtering
	// away an ever-growing set of stale connections.
	b.pruneDanglingEdges()

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
	b.buildMetadataEdges(memories, skills)

	// Pre-tokenize everything once so pairwise comparison is a cheap set
	// intersection instead of re-splitting text per pair.
	type memTokens struct {
		id     string
		tokens map[string]bool
	}
	memToks := make([]memTokens, 0, len(memories))
	for _, m := range memories {
		toks := textsim.TokenizeForOverlap(m.Content, minLen)
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
			tokens: textsim.TokenizeForOverlap(s.Name+" "+s.Description+" "+s.Content, minLen),
		}
	}

	// Memory ↔ skill (existing spokes).
	for _, mt := range memToks {
		for _, st := range skillToks {
			if len(st.tokens) == 0 {
				continue
			}
			jaccard := textsim.JaccardSimilarity(mt.tokens, st.tokens)
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
			jaccard := textsim.JaccardSimilarity(memToks[i].tokens, memToks[j].tokens)
			if jaccard >= b.cfg.TokenOverlapThreshold {
				weight := float64(jaccard) * 0.5
				_, _ = b.graph.AddEdge(memToks[i].id, memToks[j].id, domain.EdgeRelated, weight)
			}
		}
	}

	// Skill↔skill links keep the skill catalog from becoming a collection of
	// isolated hubs when their descriptions are related but no memory has
	// mentioned them yet.
	for i := 0; i < len(skillToks); i++ {
		for j := i + 1; j < len(skillToks); j++ {
			if len(skillToks[i].tokens) == 0 || len(skillToks[j].tokens) == 0 {
				continue
			}
			jaccard := textsim.JaccardSimilarity(skillToks[i].tokens, skillToks[j].tokens)
			if jaccard >= b.cfg.TokenOverlapThreshold {
				weight := float64(jaccard) * 0.5
				_, _ = b.graph.AddEdge(skillToks[i].id, skillToks[j].id, domain.EdgeRelated, weight)
			}
		}
	}
}

// Tags used by more than this many fragments are too broad to be useful as a
// deterministic relation (for example, a tag shared by an entire catalog).
const maxSpecificTagFrequency = 12

// buildMetadataEdges adds cheap deterministic relations for sparse fragments
// whose useful context lives in frontmatter rather than prose. It also links
// a fragment tag to a skill name/category, which makes freshly saved memories
// discoverable in the graph before an embedding provider is configured.
func (b *EdgeBuilder) buildMetadataEdges(memories []*domain.MemoryFragment, skills []*domain.Skill) {
	tagFrequency := make(map[string]int)
	for _, memory := range memories {
		if memory == nil {
			continue
		}
		seen := make(map[string]struct{}, len(memory.Tags))
		for _, tag := range memory.Tags {
			key := learningMetadataKey(tag)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			tagFrequency[key]++
		}
	}

	for i := 0; i < len(memories); i++ {
		left := memories[i]
		if left == nil || left.ID == "" {
			continue
		}
		for j := i + 1; j < len(memories); j++ {
			right := memories[j]
			if right == nil || right.ID == "" {
				continue
			}
			weight := fragmentMetadataWeight(left, right, tagFrequency)
			if weight > 0 {
				_, _ = b.graph.AddEdge(left.ID, right.ID, domain.EdgeRelated, weight)
			}
		}
	}

	for _, memory := range memories {
		if memory == nil || memory.ID == "" {
			continue
		}
		for _, skill := range skills {
			if skill == nil || skill.ID == "" {
				continue
			}
			if fragmentSkillMetadataMatch(memory, skill, tagFrequency) {
				_, _ = b.graph.AddEdge(memory.ID, skill.ID, domain.EdgeRelated, 0.4)
			}
		}
	}
}

func fragmentMetadataWeight(left, right *domain.MemoryFragment, tagFrequency map[string]int) float64 {
	weight := 0.0
	if sameLearningMetadata(left.Project, right.Project) {
		weight += 0.35
	}
	if sameLearningMetadata(left.Task, right.Task) {
		weight += 0.35
	}

	leftTags := learningTagSet(left.Tags)
	rightTags := learningTagSet(right.Tags)
	sharedTags := 0
	for tag := range leftTags {
		if _, ok := rightTags[tag]; !ok || tagFrequency[tag] > maxSpecificTagFrequency {
			continue
		}
		sharedTags++
	}
	if sharedTags > 0 {
		if sharedTags > 3 {
			sharedTags = 3
		}
		weight += float64(sharedTags) * 0.15
	}
	if weight > 0.8 {
		return 0.8
	}
	return weight
}

func fragmentSkillMetadataMatch(memory *domain.MemoryFragment, skill *domain.Skill, tagFrequency map[string]int) bool {
	if memory == nil || skill == nil {
		return false
	}
	keys := learningMetadataSet([]string{skill.Name, skill.Category})
	for tag := range learningTagSet(memory.Tags) {
		if tagFrequency[tag] > maxSpecificTagFrequency {
			continue
		}
		if _, ok := keys[tag]; ok {
			return true
		}
	}
	return false
}

func sameLearningMetadata(left, right string) bool {
	left = learningMetadataKey(left)
	right = learningMetadataKey(right)
	return left != "" && left == right
}

func learningMetadataSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := learningMetadataKey(value)
		if key != "" {
			set[key] = struct{}{}
			for _, token := range strings.FieldsFunc(key, func(r rune) bool {
				return r < 'a' || r > 'z'
			}) {
				if token != "" {
					set[token] = struct{}{}
				}
			}
		}
	}
	return set
}

func learningTagSet(tags []string) map[string]struct{} {
	set := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if key := learningMetadataKey(tag); key != "" {
			set[key] = struct{}{}
		}
	}
	return set
}

func learningMetadataKey(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

// pruneDanglingEdges removes persisted edges whose endpoints no longer exist
// in any current learning store. Primary IDs are included because promoted
// entries may intentionally no longer have a fragment counterpart.
func (b *EdgeBuilder) pruneDanglingEdges() {
	known := make(map[string]struct{})
	seen := make(map[string]struct{})
	// The graph viewport is intentionally capped, but pruning must inspect the
	// complete fragment catalog so entries outside that viewport are not
	// mistaken for deleted nodes.
	for _, memory := range b.fragments.List(domain.FragmentSearchFilter{}) {
		if memory != nil && memory.ID != "" {
			known[memory.ID] = struct{}{}
		}
	}
	for _, skill := range b.skills.List() {
		if skill != nil && skill.ID != "" {
			known[skill.ID] = struct{}{}
		}
	}
	if b.primary != nil {
		if memory := b.primary.Load(); memory != nil {
			for _, entry := range memory.Entries {
				if entry.ID != "" {
					known[entry.ID] = struct{}{}
				}
			}
		}
	}
	for _, edge := range b.graph.AllEdges() {
		if edge == nil {
			continue
		}
		if _, ok := known[edge.SourceID]; ok {
			if _, ok := known[edge.TargetID]; ok {
				if edge.InvalidAt == nil {
					key := learningEdgePairKey(edge)
					if _, duplicate := seen[key]; duplicate {
						_ = b.graph.DeleteEdge(edge.ID)
						continue
					}
					seen[key] = struct{}{}
				}
				continue
			}
		}
		_ = b.graph.DeleteEdge(edge.ID)
	}
}

func learningEdgePairKey(edge *domain.LearningEdge) string {
	left, right := edge.SourceID, edge.TargetID
	if edge.Type == domain.EdgeRelated || edge.Type == domain.EdgeUsedWith {
		if left > right {
			left, right = right, left
		}
	}
	return left + "\x00" + right + "\x00" + string(edge.Type)
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

	for i := 0; i < len(allEntries); i++ {
		if vectors[i] == nil {
			continue
		}
		for j := i + 1; j < len(allEntries); j++ {
			if vectors[j] == nil {
				continue
			}
			sim := textsim.CosineSimilarity(vectors[i], vectors[j])
			if sim >= threshold {
				weight := float64(sim) * 0.8 // scale to [0, 0.8]
				_, _ = b.graph.AddEdge(allEntries[i].id, allEntries[j].id, domain.EdgeRelated, weight)
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
