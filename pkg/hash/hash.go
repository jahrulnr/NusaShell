// Package hash provides content-addressed SHA-256 digests used by
// caching, journaling, and embedding layers.
//
// This is a shared leaf package at the module root (under pkg/) so both
// application/ and infrastructure/ can import it without violating Go's
// internal package rule or the Clean Architecture dependency rule. It
// depends only on the standard library.
package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

// Content returns the SHA-256 hex digest of data.
func Content(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Text returns the SHA-256 hex digest of s. The input is hashed as-is;
// callers that need normalization (lowercasing, whitespace collapse)
// should normalize before calling.
func Text(s string) string {
	return Content([]byte(s))
}

// File returns the SHA-256 hex digest and byte size of the file at path.
// Directories return an empty hash and the directory's reported size
// without reading contents. A missing or unreadable file returns an error.
func File(path string) (digest string, size int64, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	if info.IsDir() {
		return "", info.Size(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	return Content(data), int64(len(data)), nil
}
