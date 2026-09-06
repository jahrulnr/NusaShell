package codex

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nusashell/infrastructure/config"
)

// InstallationIDHeader is the HTTP header the Codex backend uses to route
// requests from the same install to the same cache shard.
const InstallationIDHeader = "x-codex-installation-id"

// LoadOrGenerateInstallationID loads a persistent installation UUID from
// <data-dir>/config/codex-installation-id, or generates a new one and
// persists it if none exists. This ID is sent as x-codex-installation-id
// so the Codex backend can route requests from the same install to the
// same cache shard. Respects NUSASHELL_DATA_DIR so the ID lives alongside
// the rest of the user's data, not in a hardcoded default location.
func LoadOrGenerateInstallationID() string {
	dir := os.Getenv("NUSASHELL_DATA_DIR")
	if dir == "" {
		dir = config.DefaultDataDir()
	}
	path := filepath.Join(dir, "config", "codex-installation-id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}
	id := mustGenerateUUID()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte(id), 0o600)
	return id
}

// mustGenerateUUID generates a random UUID v4 string. Panics on read failure
// (should never happen with crypto/rand).
func mustGenerateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}
