// Package nonce generates short random identifiers used for synthetic
// tool-call IDs and subagent result prefixes. It is a shared leaf package
// at the module root (under internal/) so both application/ and any other
// package within the nusashell module can import it.
package nonce

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Random returns an 8-byte hex-encoded random string. On crypto/rand
// failure it falls back to a timestamp-based nonce (non-crypto, but
// unique enough for synthetic ID generation).
func Random() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
