// Package hash provides content-addressed SHA-256 digests used by
// caching and embedding layers.
//
// This is a shared leaf package at the module root (under pkg/) so both
// application/ and infrastructure/ can import it without violating Go's
// internal package rule or the Clean Architecture dependency rule. It
// depends only on the standard library.
package hash

import (
	"crypto/sha256"
	"encoding/hex"
)

// Text returns the SHA-256 hex digest of s. The input is hashed as-is;
// callers that need normalization (lowercasing, whitespace collapse)
// should normalize before calling.
func Text(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
