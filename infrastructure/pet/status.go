package pet

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Resolver is the dependency-injected environment probe used by the
// installer. Tests inject a sandboxed Home + procfs root.
type Resolver struct {
	Home       string
	Env        func(string) string
	Stat       func(string) (any, error) // returns nil on success; opaque to keep tests lightweight
	ProcRoot   string                    // /proc equivalent (real or fake)
	UserBinDir string                    // defaults to <Home>/.local/bin when empty
}

// PetsBinary resolves the nusashell-pets executable. Order matches the
// detect package in apps/pets/internal/detect for parity:
//
//  1. NUSASHELL_PETS_INSTALL_ROOT env override
//  2. ~/.local/share/nusashell-pets/current/nusashell-pets
//  3. ~/.local/share/nusashell-pets/nusashell-pets (unversioned fallback)
//  4. ~/.local/bin/nusashell-pets (launcher)
//
// Returns the absolute path and installed=true when found. The pet binary
// is Linux-only — non-Linux platforms return installed=false regardless of
// what is on disk, so callers (UI, RPC handlers) can treat the icon as
// "not installed" without an extra platform check.
func (r *Resolver) PetsBinary() (string, bool) {
	if r == nil || runtime.GOOS != "linux" {
		return "", false
	}
	roots := []string{
		r.Env("NUSASHELL_PETS_INSTALL_ROOT"),
		filepath.Join(r.Home, ".local/share/nusashell-pets"),
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		for _, cand := range []string{
			filepath.Join(root, "current", "nusashell-pets"),
			filepath.Join(root, "nusashell-pets"),
		} {
			if r.executable(cand) {
				return cand, true
			}
		}
	}
	launcher := filepath.Join(r.Home, ".local/bin/nusashell-pets")
	if r.executable(launcher) {
		return launcher, true
	}
	return "", false
}

// PetsLauncher returns the ~/.local/bin/nusashell-pets path the installer
// creates. Independent of "installed" — the launcher exists whenever the
// installer has finished at least one successful run.
func (r *Resolver) PetsLauncher() string {
	if r == nil {
		return ""
	}
	bin := r.UserBinDir
	if bin == "" {
		bin = filepath.Join(r.Home, ".local/bin")
	}
	return filepath.Join(bin, "nusashell-pets")
}

// PetsAssetsPath returns the assets/pets directory the launcher depends on
// (spritesheet + config.json). Empty when nothing is installed. The path
// is the active versioned root + assets/pets; "current" is followed so the
// assets move with the version pointer.
func (r *Resolver) PetsAssetsPath() string {
	if r == nil || runtime.GOOS != "linux" {
		return ""
	}
	if override := r.Env("NUSASHELL_PETS_INSTALL_ROOT"); override != "" {
		return filepath.Join(override, "current/assets/pets")
	}
	return filepath.Join(r.Home, ".local/share/nusashell-pets/current/assets/pets")
}

// PetsInstalledVersion reads the VERSION file shipped with the active
// release. Empty when the versioned root is missing the file or binary.
func (r *Resolver) PetsInstalledVersion() string {
	if r == nil || runtime.GOOS != "linux" {
		return ""
	}
	candidates := []string{}
	if override := r.Env("NUSASHELL_PETS_INSTALL_ROOT"); override != "" {
		candidates = append(candidates, filepath.Join(override, "current/VERSION"))
	}
	candidates = append(candidates, filepath.Join(r.Home, ".local/share/nusashell-pets/current/VERSION"))
	for _, path := range candidates {
		if !r.executable(filepath.Join(filepath.Dir(path), "nusashell-pets")) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		v := strings.TrimSpace(string(data))
		if v != "" {
			return v
		}
	}
	return ""
}

// PetsRunning scans the procfs root for a process whose cmdline mentions
// nusashell-pets. Mirrors detect.ElectronRunning but scoped to the pet.
// Returns false when the procfs root is missing/empty (macOS, Windows, or
// sandboxed tests).
func (r *Resolver) PetsRunning() bool {
	return len(r.PetsPIDs()) > 0
}

// PetsPIDs returns PIDs whose cmdline looks like a desktop pet process.
func (r *Resolver) PetsPIDs() []int {
	return r.petsPIDsMatching("")
}

func (r *Resolver) petsPIDsMatching(binary string) []int {
	if r == nil || runtime.GOOS != "linux" || r.ProcRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(r.ProcRoot)
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(r.ProcRoot, entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		line := strings.ReplaceAll(string(cmdline), "\x00", " ")
		line = strings.TrimSpace(line)
		if !isPetCmdline(line, binary) {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

func isPetCmdline(line, binary string) bool {
	if strings.Contains(line, "nusashell-pets-install") || strings.Contains(line, "nusashell-pets-build") {
		return false
	}
	if binary != "" {
		return strings.Contains(line, binary)
	}
	return strings.Contains(line, "nusashell-pets")
}

// executable mirrors detect.Resolver.executable: a file exists AND has at
// least one executable bit set.
func (r *Resolver) executable(path string) bool {
	if path == "" || r.Stat == nil {
		return false
	}
	if _, err := r.Stat(path); err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// DefaultResolver builds a Resolver using real OS hooks. ProcRoot defaults
// to /proc on Linux; macOS/Windows callers should use a custom Resolver
// (they will not see Supported=true anyway).
func DefaultResolver(homeDir string) *Resolver {
	env := os.Getenv
	if homeDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h
		}
	}
	proc := "/proc"
	if runtime.GOOS != "linux" {
		proc = ""
	}
	return &Resolver{
		Home:     homeDir,
		Env:      env,
		Stat:     statExists,
		ProcRoot: proc,
	}
}

func statExists(path string) (any, error) {
	_, err := os.Stat(path)
	return nil, err
}
