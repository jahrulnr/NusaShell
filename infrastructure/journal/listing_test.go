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
