package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListing_detectChanges(t *testing.T) {
	before := map[string]fileMeta{
		"keep.txt":   {Size: 10, ModTime: 100},
		"old.txt":    {Size: 5, ModTime: 200},
		"resize.txt": {Size: 1, ModTime: 300},
	}
	after := map[string]fileMeta{
		"keep.txt":   {Size: 10, ModTime: 100},
		"new.txt":    {Size: 7, ModTime: 400},
		"resize.txt": {Size: 2, ModTime: 300},
	}
	added, modified, deleted := diffListing(before, after)
	if len(added) != 1 || added[0] != "new.txt" {
		t.Fatalf("added: %v", added)
	}
	if len(deleted) != 1 || deleted[0] != "old.txt" {
		t.Fatalf("deleted: %v", deleted)
	}
	if len(modified) != 1 || modified[0] != "resize.txt" {
		t.Fatalf("modified: %v", modified)
	}
}

func TestListing_snapshotRealDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("bb"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := snapshotDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["a.txt"]; !ok {
		t.Fatal("missing a.txt")
	}
	// Snapshot keys are slash-normalized (filepath.ToSlash) so they are
	// stable across platforms — look them up with slashes, not filepath.Join.
	if _, ok := snap["sub/b.txt"]; !ok {
		t.Fatal("missing sub/b.txt")
	}
}

func TestListing_snapshotSkipsIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".git", "node_modules", ".nusashell", ".agent", "__pycache__", ".cache", ".tmp", ".venv"} {
		if err := os.MkdirAll(filepath.Join(root, dir, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "nested", "junk.txt"), []byte("j"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := snapshotDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["keep.txt"]; !ok {
		t.Fatal("missing keep.txt")
	}
	for rel := range snap {
		for _, dir := range []string{".git", "node_modules", ".nusashell", ".agent", "__pycache__", ".cache", ".tmp", ".venv"} {
			if strings.HasPrefix(rel, dir+"/") || rel == dir {
				t.Fatalf("ignored dir %q leaked into snapshot: %s", dir, rel)
			}
		}
	}
}

func TestListing_mtimeChangeCountsAsModified(t *testing.T) {
	before := map[string]fileMeta{"f": {Size: 1, ModTime: 1}}
	after := map[string]fileMeta{"f": {Size: 1, ModTime: 2}}
	_, modified, deleted := diffListing(before, after)
	if len(modified) != 1 || len(deleted) != 0 {
		t.Fatalf("mtime-only change: modified=%v deleted=%v", modified, deleted)
	}
	_ = time.Now()
}

// TestListing_snapshotSkipsPermissionDeniedDirs proves that a single
// unreadable subdirectory (e.g. systemd-private-* in /tmp) does NOT abort
// the entire workspace walk. Accessible files outside the denied dir must
// still appear in the snapshot, and the walk must not return an error.
//
// Without this, opaque mutations rooted at /tmp fail with
// "pre-listing failed" whenever a systemd-private directory exists, even
// though the agent's workspace files are perfectly readable.
func TestListing_snapshotSkipsPermissionDeniedDirs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not deny root access")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readable.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a subdirectory with no read/execute permission — simulates
	// /tmp/systemd-private-* which is mode 0o700 owned by root.
	denied := filepath.Join(root, "systemd-private-blocked")
	if err := os.Mkdir(denied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(denied, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Strip all permissions so the test user cannot read or enter it.
	if err := os.Chmod(denied, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(denied, 0o755) })

	snap, err := snapshotDir(root)
	if err != nil {
		t.Fatalf("snapshotDir returned error for permission-denied subdir: %v (expected skip)", err)
	}
	if _, ok := snap["readable.txt"]; !ok {
		t.Fatal("missing readable.txt — permission-denied subdir aborted the walk")
	}
}
