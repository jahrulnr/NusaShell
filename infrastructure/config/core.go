// Package config holds process-wide constants and helpers that prevent
// "nusashell" and other magic strings from being duplicated across
// packages. Anything that needs the data directory or a file inside it
// goes through here.
package config

import (
	"os"
	"path/filepath"
)

// AppName is the human-readable product name used in client-info payloads.
const AppName = "NusaShell"

// DataDirName is the subdirectory under the OS config dir where NusaShell
// stores its data (credentials, docs, installation IDs, etc). The "light"
// suffix was dropped when the Go app became the main NusaShell product.
const DataDirName = "nusashell"

// DefaultDataDir returns the absolute path to the data directory under the
// OS user config dir (e.g. ~/.config/nusashell). Falls back to a relative
// path if the config dir cannot be resolved.
func DefaultDataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, DataDirName)
}
