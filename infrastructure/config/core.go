// Package config holds process-wide constants and helpers that prevent
// "nusashell-go" and other magic strings from being duplicated across
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
// stores its data (credentials, docs, installation IDs, etc).
const DataDirName = "nusashell-light"

// DefaultDataDir returns the absolute path to the data directory under the
// OS user config dir (e.g. ~/.config/nusashell-light). Falls back to a relative
// path if the config dir cannot be resolved.
func DefaultDataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, DataDirName)
}

// CodexCLIDir returns the path to the Codex CLI config directory
// (~/.codex on Linux/macOS, %USERPROFILE%\.codex on Windows). Uses
// os.UserHomeDir() for cross-platform home resolution.
func CodexCLIDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".codex")
	}
	return filepath.Join(home, ".codex")
}

// CodexCLIAuthPath returns the path to the Codex CLI auth.json file.
func CodexCLIAuthPath() string {
	return filepath.Join(CodexCLIDir(), "auth.json")
}
