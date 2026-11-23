package system

import (
	"os"
	"path/filepath"
	"testing"
)

// install lays down an nvm tree under home with a node binary per version.
func install(t *testing.T, home string, versions ...string) {
	t.Helper()
	for _, v := range versions {
		bin := filepath.Join(home, ".nvm", "versions", "node", v, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func bin(home, version string) string {
	return filepath.Join(home, ".nvm", "versions", "node", version, "bin")
}

func TestNodeDirInPrefersTheDefaultAlias(t *testing.T) {
	home := t.TempDir()
	install(t, home, "v20.1.0", "v22.4.0", "v26.12.1")
	if err := os.MkdirAll(filepath.Join(home, ".nvm", "alias"), 0o755); err != nil {
		t.Fatal(err)
	}
	set := func(alias string) {
		if err := os.WriteFile(filepath.Join(home, ".nvm", "alias", "default"), []byte(alias+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set("v22.4.0")
	if got := nodeDirIn(home); got != bin(home, "v22.4.0") {
		t.Fatalf("full alias: %s", got)
	}
	// An alias may be a prefix; the newest version under it wins.
	install(t, home, "v22.10.0")
	set("22")
	if got := nodeDirIn(home); got != bin(home, "v22.10.0") {
		t.Fatalf("prefix alias: %s", got)
	}
}

func TestNodeDirInFallsBackToTheNewestVersion(t *testing.T) {
	home := t.TempDir()
	install(t, home, "v9.0.0", "v18.20.1", "v20.5.0")
	// Versions order numerically: v9 is below v18, not above it.
	if got := nodeDirIn(home); got != bin(home, "v20.5.0") {
		t.Fatalf("newest: %s", got)
	}
}

func TestNodeDirInSkipsVersionsWithoutANode(t *testing.T) {
	home := t.TempDir()
	install(t, home, "v18.20.1")
	if err := os.MkdirAll(filepath.Join(home, ".nvm", "versions", "node", "v22.0.0", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := nodeDirIn(home); got != bin(home, "v18.20.1") {
		t.Fatalf("a broken install was chosen: %s", got)
	}
	if got := nodeDirIn(t.TempDir()); got != "" {
		t.Fatalf("no nvm at all gave %q", got)
	}
}
