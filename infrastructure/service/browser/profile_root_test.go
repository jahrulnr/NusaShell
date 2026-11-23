package browser_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/service/browser"
)

func TestProfileRootKeepsAFolderThatHoldsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps the preferred folder")
	}
	preferred := filepath.Join(t.TempDir(), "data", "browser")
	dir, moved := browser.ProfileRoot(preferred)
	if dir != preferred || moved {
		t.Fatalf("ProfileRoot = %q, %v; want %q, false", dir, moved, preferred)
	}
	if info, err := os.Stat(preferred); err != nil || !info.IsDir() {
		t.Fatalf("the folder was not created: %v", err)
	}
}

func TestProfileRootMovesOffAFolderItCannotUse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps the preferred folder")
	}
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("HOME", config)
	// A folder cannot be made under a file, which stands in for a
	// filesystem that cannot hold what Chrome's lock needs.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	preferred := filepath.Join(blocker, "browser")

	dir, moved := browser.ProfileRoot(preferred)
	want := filepath.Join(mustConfigDir(t), "nusashell", "browser")
	if !moved || dir != want {
		t.Fatalf("ProfileRoot = %q, %v; want %q, true", dir, moved, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("the new folder was not created: %v", err)
	}
}

func TestProfileRootStaysPutWhenNothingBetterExists(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux reports a missing config directory from the environment alone")
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	preferred := filepath.Join(blocker, "browser")
	if dir, moved := browser.ProfileRoot(preferred); dir != preferred || moved {
		t.Fatalf("ProfileRoot = %q, %v; want %q, false", dir, moved, preferred)
	}
}

func mustConfigDir(t *testing.T) string {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
