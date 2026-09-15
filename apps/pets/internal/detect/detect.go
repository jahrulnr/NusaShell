// Package detect resolves where NusaShell components live on this machine
// (Go core and Electron wrapper) and probes whether they are running. All
// environment access goes through injecting functions so behavior is
// deterministic in unit tests; the SDL side only uses DefaultResolver.
package detect

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// ElectronSpawnSpec is the executable and argv to open/focus the Electron
// wrapper. Args may include install-time flags such as --no-sandbox.
type ElectronSpawnSpec struct {
	Path string
	Args []string
}

// ElectronBinary resolves the Electron desktop wrapper binary. An explicitly
// configured path wins when present and executable; otherwise the user
// launcher is preferred over the versioned install payload because the
// installer writes the Chromium sandbox decision (--no-sandbox or not) into
// ~/.local/bin/nusashell-desktop. The second return value is false when the
// wrapper is not installed.
func (r *Resolver) ElectronBinary(configured string) (string, bool) {
	spec, ok := r.ElectronSpawn(configured)
	return spec.Path, ok
}

// ElectronSpawn resolves how to launch the Electron wrapper. Prefer the
// user launcher shim; when only the versioned binary is present and the
// installer disabled chrome-sandbox, append --no-sandbox so a pet click
// matches `nusashell-desktop` from a shell.
func (r *Resolver) ElectronSpawn(configured string) (ElectronSpawnSpec, bool) {
	if r == nil {
		return ElectronSpawnSpec{}, false
	}
	if r.executable(configured) {
		return r.electronSpec(configured, true), true
	}
	launcher := filepath.Join(r.Home, ".local/bin/nusashell-desktop")
	if r.executable(launcher) {
		// Older launcher shims may predate the sandbox decision. Inspect the
		// payload marker too, so an installed Electron without chrome-sandbox
		// still starts when the pet is clicked.
		return r.electronSpec(launcher, true), true
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
				return r.electronSpec(cand, true), true
			}
		}
	}
	return ElectronSpawnSpec{}, false
}

func (r *Resolver) electronSpec(bin string, allowNoSandbox bool) ElectronSpawnSpec {
	spec := ElectronSpawnSpec{Path: bin}
	if allowNoSandbox && r.electronNeedsNoSandbox(bin) {
		spec.Args = []string{"--no-sandbox"}
	}
	return spec
}

func (r *Resolver) electronNeedsNoSandbox(bin string) bool {
	if bin == "" || r.Stat == nil {
		return false
	}
	if r.isElectronLauncher(bin) && r.launcherHasNoSandbox(bin) {
		return false
	}
	candidates := []string{filepath.Join(filepath.Dir(bin), "chrome-sandbox.disabled")}
	if r.isElectronLauncher(bin) {
		for _, root := range []string{
			r.Env("NUSASHELL_ELECTRON_INSTALL_ROOT"),
			filepath.Join(r.Home, ".local/share/nusashell-electron"),
		} {
			if root == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(root, "current/chrome-sandbox.disabled"),
				filepath.Join(root, "chrome-sandbox.disabled"),
			)
		}
	}
	for _, candidate := range candidates {
		if _, err := r.Stat(candidate); err == nil {
			return true
		}
	}
	return false
}

func (r *Resolver) isElectronLauncher(bin string) bool {
	return r != nil && filepath.Clean(bin) == filepath.Clean(filepath.Join(r.Home, ".local/bin/nusashell-desktop"))
}

func (r *Resolver) launcherHasNoSandbox(bin string) bool {
	data, err := os.ReadFile(bin)
	return err == nil && strings.Contains(string(data), "--no-sandbox")
}
