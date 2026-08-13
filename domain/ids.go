// Package domain holds pure business rules with no I/O dependencies.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewID generates a random hex identifier with the given prefix.
func NewID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("domain: random id generation failed: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// EstimateTokens is a rough heuristic (chars / 4) used for compaction
// thresholds. Exact tokenization belongs to the model provider.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return (len(text) + 3) / 4
}
