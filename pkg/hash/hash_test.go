package hash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContent_deterministic(t *testing.T) {
	data := []byte("hello journal")
	got := Content(data)
	if got == "" {
		t.Fatal("expected non-empty hash")
	}
	if Content(data) != got {
		t.Fatal("hash must be deterministic")
	}
}

func TestText_matchesContent(t *testing.T) {
	s := "hello"
	if Text(s) != Content([]byte(s)) {
		t.Fatal("Text and Content must agree on the same bytes")
	}
}

func TestFile_existing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, size, err := File(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != 3 {
		t.Fatalf("size: got %d want 3", size)
	}
	if digest != Content([]byte("abc")) {
		t.Fatalf("hash mismatch: %s", digest)
	}
}

func TestFile_missing(t *testing.T) {
	_, _, err := File(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFile_directory(t *testing.T) {
	dir := t.TempDir()
	digest, _, err := File(dir)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "" {
		t.Fatalf("directory hash should be empty, got %s", digest)
	}
}
