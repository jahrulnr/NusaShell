// Package domain holds pure business rules with no I/O dependencies.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// NewID generates a random hex identifier with the given prefix.
func NewID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("domain: random id generation failed: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// NewULID generates a Crockford-base32 ULID-like identifier that is
// lexicographically sortable by creation time. Used for learning nodes
// (skills, memory, edges) so that listing by ID also lists by time.
// Format: 10-char timestamp + 16-char random (26 chars total, ULID-spec
// compatible).
func NewULID(prefix string) string {
	ts := uint64(time.Now().UnixMilli())
	timeBytes := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		timeBytes[i] = byte(ts & 0xff)
		ts >>= 8
	}
	randBytes := make([]byte, 10)
	if _, err := rand.Read(randBytes); err != nil {
		panic(fmt.Sprintf("domain: ulid random failed: %v", err))
	}
	combined := append(timeBytes, randBytes...)
	// Each 5 bits → 1 char. 16 bytes = 128 bits = 26 chars (with padding).
	bits := make([]byte, 0, len(combined)*8)
	for _, byt := range combined {
		for i := 7; i >= 0; i-- {
			bits = append(bits, (byt>>uint(i))&1)
		}
	}
	var encoded []byte
	for i := 0; i+5 <= len(bits); i += 5 {
		val := bits[i]<<4 | bits[i+1]<<3 | bits[i+2]<<2 | bits[i+3]<<1 | bits[i+4]
		encoded = append(encoded, crockford[val])
	}
	return prefix + "_" + string(encoded)
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// CombineWeights merges two edge weights using probability-union:
// w = 1 - (1-w1) * (1-w2). This strengthens edges that are observed
// repeatedly without ever reaching exactly 1.0.
func CombineWeights(w1, w2 float64) float64 {
	return 1 - (1-w1)*(1-w2)
}

// EstimateTokens is a rough heuristic (chars / 4) used for compaction
// thresholds. Exact tokenization belongs to the model provider.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return (len(text) + 3) / 4
}
