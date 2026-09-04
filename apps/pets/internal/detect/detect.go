// Package detect resolves where NusaShell components live on this machine
// (Go core and Electron wrapper) and probes whether they are running. All
// environment access goes through injecting functions so behavior is
// deterministic in unit tests; the SDL side only uses DefaultResolver.
package detect

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Resolver collects the environment-dependent probes used by detection.
type Resolver struct {
	Home     string
	Env      func(string) string
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
}

// DefaultResolver uses the real user home, process environment, PATH lookup,
// and filesystem.
func DefaultResolver() *Resolver {
	home, _ := os.UserHomeDir()
	return &Resolver{
		Home:     home,
		Env:      os.Getenv,
		LookPath: exec.LookPath,
		Stat:     os.Stat,
	}
}

// executable reports whether the file at path exists and is executable.
func (r *Resolver) executable(path string) bool {
	if path == "" {
		return false
	}
	info, err := r.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// GoBinary resolves the NusaShell Go core binary. Candidates are checked in
// order: the env override install root, the default install location, the
// user launcher, and finally the PATH. The second return value is false when
// the core is not installed.
func (r *Resolver) GoBinary() (string, bool) {
	if r == nil {
		return "", false
	}
	roots := []string{
		r.Env("NUSASHELL_GO_INSTALL_ROOT"),
		filepath.Join(r.Home, ".local/share/nusashell"),
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		for _, cand := range []string{
			filepath.Join(root, "current", "nusashell"),
			filepath.Join(root, "nusashell"),
		} {
			if r.executable(cand) {
				return cand, true
			}
		}
	}
	launcher := filepath.Join(r.Home, ".local/bin/nusashell")
	if r.executable(launcher) {
		return launcher, true
	}
	if r.LookPath != nil {
		if path, err := r.LookPath("nusashell"); err == nil && r.executable(path) {
			return path, true
		}
	}
	return "", false
}

// ElectronBinary resolves the Electron desktop wrapper binary. An explicitly
// configured path wins when present and executable; otherwise the env
// override install root, the default install location, and the user launcher
// are checked in order. The second return value is false when the wrapper is
// not installed.
func (r *Resolver) ElectronBinary(configured string) (string, bool) {
	if r == nil {
		return "", false
	}
	if r.executable(configured) {
		return configured, true
	}
	roots := []string{
		r.Env("NUSASHELL_ELECTRON_INSTALL_ROOT"),
		filepath.Join(r.Home, ".local/share/nusashell-electron"),
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		for _, cand := range []string{
			filepath.Join(root, "current", "nusashell-desktop"),
			filepath.Join(root, "nusashell-desktop"),
		} {
			if r.executable(cand) {
				return cand, true
			}
		}
	}
	launcher := filepath.Join(r.Home, ".local/bin/nusashell-desktop")
	if r.executable(launcher) {
		return launcher, true
	}
	return "", false
}
