package acp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBypassPathResolvesAbsoluteOutsideWorkspace verifies that bypassPath
// resolves an existing absolute host file outside the workspace to its
// canonical location, without rejecting it as containedPath would.
func TestBypassPathResolvesAbsoluteOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "file.txt")
	if err := os.WriteFile(outsideFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := bypassPath(ws, outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	want := canonicalForTest(t, outsideFile)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestBypassPathPreservesRelativeInsideWorkspace verifies that relative
// paths are joined onto the workspace root, matching containedPath for
// in-workspace targets.
func TestBypassPathPreservesRelativeInsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	got, err := bypassPath(ws, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalForTest(t, ws), "main.go")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestBypassPathCreatesMissingOutsideSuffix verifies that bypassPath can
// resolve a not-yet-existing outside path (missing suffix), matching
// canonicalPath semantics for write-before-exists.
func TestBypassPathCreatesMissingOutsideSuffix(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	missingOutside := filepath.Join(outside, "newdir", "newfile.txt")
	got, err := bypassPath(ws, missingOutside)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalForTest(t, outside), "newdir", "newfile.txt")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestBypassPathResolvesSymlinkToOutside verifies that a symlink inside
// the workspace pointing to an outside file is resolved to the real
// outside target, not remapped back into the workspace.
func TestBypassPathResolvesSymlinkToOutside(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "real.txt")
	if err := os.WriteFile(outsideFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "link.txt")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := bypassPath(ws, link)
	if err != nil {
		t.Fatal(err)
	}
	want := canonicalForTest(t, outsideFile)
	if got != want {
		t.Fatalf("got %q want %q (symlink must resolve to outside target, not remap to workspace)", got, want)
	}
}
