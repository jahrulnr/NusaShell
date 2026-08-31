package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashContent_roundtrip(t *testing.T) {
	data := []byte("hello journal")
	got := hashContent(data)
	if got == "" {
		t.Fatal("expected non-empty hash")
	}
	if hashContent(data) != got {
		t.Fatal("hash must be deterministic")
	}
}

func TestHashFile_existing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, size, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != 3 {
		t.Fatalf("size: got %d want 3", size)
	}
	if hash != hashContent([]byte("abc")) {
		t.Fatalf("hash mismatch: %s", hash)
	}
}

func TestHashFile_missing(t *testing.T) {
	_, _, err := hashFile(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
