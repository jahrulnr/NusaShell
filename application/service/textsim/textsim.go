// Package textsim provides pure text similarity math helpers used by the
// learning edges and learning search subsystems. Extracted from the
// application root so the math lives in one testable leaf package.
package textsim

import (
	"math"
	"strings"
)

// TokenizeForOverlap splits text into a set of lowercase word tokens,
// dropping punctuation and tokens shorter than minLen. Used by edge
// detection to compute overlap between memory/skill entries.
func TokenizeForOverlap(text string, minLen int) map[string]bool {
	tokens := map[string]bool{}
	for _, t := range strings.Fields(strings.ToLower(text)) {
		t = strings.Trim(t, ".,;:!?\"'()[]{}")
		if len(t) >= minLen {
			tokens[t] = true
		}
	}
	return tokens
}

// JaccardSimilarity computes |A ∩ B| / |A ∪ B| for two token sets.
// Returns 0 when either set is empty.
func JaccardSimilarity(a, b map[string]bool) float32 {
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

// CosineSimilarity computes the cosine similarity between two float32
// vectors. Returns 0 when either vector is empty, lengths differ, or one
// vector is zero-length.
func CosineSimilarity(a, b []float32) float32 {
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
