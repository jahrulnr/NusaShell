package domain

// Learning graph policy constants.
//
// These govern memory consolidation (merging near-duplicate entries) and
// edge building (inferring relations between memories and skills). The
// services that perform the work live in the application layer; the
// policy constants live here so the rules are visible at the layer that
// owns the memory and skill models.
const (
	// DefaultConsolidationSimilarityThreshold is the cosine similarity
	// above which two memory entries are considered near-duplicates and
	// merged.
	DefaultConsolidationSimilarityThreshold = 0.92
	// DefaultConsolidationMinContentLen is the minimum content length to
	// consider for consolidation. Short entries (e.g. "yes", "ok") are
	// skipped to avoid false merges.
	DefaultConsolidationMinContentLen = 20

	// DefaultEdgeEmbeddingThreshold is the cosine similarity above which
	// an embedding-based memory↔skill pair gets a `related` edge.
	DefaultEdgeEmbeddingThreshold = 0.85
	// DefaultEdgeTokenOverlapThreshold is the Jaccard similarity above
	// which a memory and skill get a `related` edge via token overlap.
	DefaultEdgeTokenOverlapThreshold = 0.3
	// DefaultEdgeMinTokenLen is the minimum token length for overlap
	// matching (matches Hermes).
	DefaultEdgeMinTokenLen = 3
	// MaxSpecificTagFrequency is the maximum number of fragments that may
	// share a tag before the tag is considered too broad to be a useful
	// deterministic relation.
	MaxSpecificTagFrequency = 12
)
