package system

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// candidate pairs a BackendEnv key with the manager it selects and a
// cheap presence probe — LookPath-level, never a subprocess: New runs
// on CLI paths that may have nothing to do with the service.
type candidate struct {
	key     string
	service Service
	present func() bool
}

// lookPath is a seam so tests can stage PATH presence.
var lookPath = exec.LookPath

func binaryOnPath(name string) func() bool {
	return func() bool { _, err := lookPath(name); return err == nil }
}

// execCommand runs a host program. Tests replace it so an adapter
// never talks to the real service manager.
var execCommand = func(name string, args ...string) ([]byte, int, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, 0, nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return out, exitErr.ExitCode(), nil
	}
	return out, -1, err
}

// New picks the host service manager for this platform — never nil,
// never a subprocess. BackendEnv names a specific candidate; an
// unknown or absent one yields an unavailable Service so the caller's
// degradation policy decides, never a silent fallback to a different
// manager.
func New() Service {
	if key := os.Getenv(BackendEnv); key != "" {
		for _, c := range candidates {
			if c.key == key {
				if c.present() {
					return c.service
				}
				return unavailable{name: c.key, reason: "named by " + BackendEnv + " but not found on PATH"}
			}
		}
		return unavailable{name: key, reason: "unknown " + BackendEnv}
	}
	for _, c := range candidates {
		if c.present() {
			return c.service
		}
	}
	if len(candidates) == 0 {
		return unavailable{name: runtime.GOOS, reason: "no supported service manager"}
	}
	return unavailable{name: "host service", reason: "no supported service manager found"}
}

// validName reports whether a registration identity is safe to render
// into registration filenames and pass as a manager argument — the
// portable set every manager accepts.
func validName(name string) bool {
	// A leading dash would read as a flag; "." and ".." are
	// path segments. The charset keeps the name safe in filenames
	// and rendered files alike.
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "-") {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool {
		return r != '_' && r != '-' && r != '.' && r != '@' &&
			!(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')
	}) < 0
}

func errBadName(name string) error {
	return fmt.Errorf("invalid service name %q", name)
}
