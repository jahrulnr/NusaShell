package turndiff

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

const (
	// ZeroOID is the all-zero git blob id used for /dev/null sides.
	ZeroOID = "0000000000000000000000000000000000000000"
	// DevNull is the git unified-diff path for a missing file.
	DevNull = "/dev/null"
	// RegularFileMode is the git mode Codex emits for tracked text files.
	RegularFileMode = "100644"
)

func gitBlobOID(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
