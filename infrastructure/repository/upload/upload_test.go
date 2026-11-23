package repoupload

import (
	"os"
	"testing"
)

// Purge leaves a live, empty store: every upload dir is gone, the root is
// recreated private, and a new Save lands there.
func TestPurge(t *testing.T) {
	root := t.TempDir() + "/uploads"
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(t.Context(), "a.txt", "text/plain", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(t.Context(), "b.png", "image/png", []byte("png")); err != nil {
		t.Fatal(err)
	}
	if err := store.Purge(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("upload root holds %d entries: %v", len(entries), err)
	}
	if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("root mode = %v: %v", info.Mode().Perm(), err)
	}
	if _, _, err := store.Save(t.Context(), "c.txt", "text/plain", []byte("y")); err != nil {
		t.Fatalf("save after purge: %v", err)
	}
}
