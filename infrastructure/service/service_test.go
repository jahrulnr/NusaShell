package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuardRefusesInsideService(t *testing.T) {
	t.Setenv(ServiceEnvMarker, "1")
	for _, action := range []string{ActionInstall, ActionUninstall, ActionStart, ActionStop, ActionRestart} {
		if err := guardOutsideService(action); err == nil {
			t.Fatalf("action %q must be refused inside the service", action)
		}
	}
	if err := guardOutsideService(ActionStatus); err != nil {
		t.Fatalf("status must be allowed inside the service: %v", err)
	}
}

func TestGuardAllowsOutsideService(t *testing.T) {
	t.Setenv(ServiceEnvMarker, "")
	for _, action := range []string{ActionInstall, ActionUninstall, ActionStatus, ActionStart, ActionStop, ActionRestart} {
		if err := guardOutsideService(action); err != nil {
			t.Fatalf("action %q must be allowed outside the service: %v", action, err)
		}
	}
}

func TestStableBinaryPathPrefersCurrentSymlink(t *testing.T) {
	root := t.TempDir()
	versions := filepath.Join(root, "versions", "0.4.1")
	current := filepath.Join(root, "current")
	for _, dir := range []string{versions, current} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(versions, current+"-link-target"); err != nil {
		t.Fatal(err)
	}
	// current must be a symlink to the version dir for the resolver to fire.
	if err := os.RemoveAll(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versions, current); err != nil {
		t.Fatal(err)
	}
	got := StableBinaryPath(filepath.Join(versions, "nusashell"))
	if want := filepath.Join(current, "nusashell"); got != want {
		t.Fatalf("StableBinaryPath = %q, want %q", got, want)
	}
}

func TestStableBinaryPathWithoutCurrentLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	execPath := filepath.Join(root, "nusashell")
	if got := StableBinaryPath(execPath); got != execPath {
		t.Fatalf("StableBinaryPath = %q, want unchanged %q", got, execPath)
	}
}

func TestStableBinaryPathMissingCurrent(t *testing.T) {
	root := t.TempDir()
	versions := filepath.Join(root, "versions", "0.4.1")
	if err := os.MkdirAll(versions, 0o755); err != nil {
		t.Fatal(err)
	}
	execPath := filepath.Join(versions, "nusashell")
	if got := StableBinaryPath(execPath); got != execPath {
		t.Fatalf("StableBinaryPath = %q, want unchanged %q", got, execPath)
	}
}

// TestStableBinaryPathResolvesSymlinkedRoot reproduces the CI failure on
// macOS/Windows: the install root is addressed through a symlink (macOS
// /var → /private/var), so EvalSymlinks(current) resolves to the real path
// while the caller-supplied versions dir still contains the symlink; the
// stable-path rewrite must fire anyway.
func TestStableBinaryPathResolvesSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	versions := filepath.Join(real, "versions", "0.4.1")
	current := filepath.Join(real, "current")
	for _, dir := range []string{versions, current} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versions, current); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	execPath := filepath.Join(link, "versions", "0.4.1", "nusashell")
	if got, want := StableBinaryPath(execPath), filepath.Join(link, "current", "nusashell"); got != want {
		t.Fatalf("StableBinaryPath = %q, want %q", got, want)
	}
}

func TestNewDispatchesByPlatform(t *testing.T) {
	m := New(Options{BinaryPath: "/x", DataDir: "/d"})
	if m == nil {
		t.Fatal("New returned nil")
	}
	// The concrete type is platform-specific; only assert it satisfies Manager.
	var _ Manager = m
}
