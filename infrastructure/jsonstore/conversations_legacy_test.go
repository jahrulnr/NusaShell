package jsonstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nusashell/domain"
)

// Regression: the conversations directory may contain legacy sidecars from
// the retired desktop app (conv_<id>.meta.json with an object "model",
// conv_<id>.runtime.json) plus other stores' files. Loading must ignore
// them — and skip unparsable conversation files — instead of failing
// Store construction.
func TestLoadIgnoresLegacySidecarsAndUnparsableFiles(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ok := &domain.Conversation{ID: "conv_ok", Title: "ok"}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(convDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.MarshalIndent(ok, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write("conv_ok.json", string(b))
	// Legacy desktop-era meta: "model" is an object, not a string.
	write("conv_legacy.meta.json", `{"id":"conv_legacy","title":"legacy","model":{"modelKey":"m","effort":"auto","explicit":true}}`)
	write("conv_legacy.runtime.json", `{"pending":true}`)
	write("conv_broken.json", `{not json`)
	write("todos.json", `[{"id":"t1"}]`)

	s, err := New(dir)
	if err != nil {
		t.Fatalf("New() must survive legacy/unparsable files, got: %v", err)
	}
	convs := s.List()
	if len(convs) != 1 || convs[0].ID != "conv_ok" {
		t.Fatalf("want only conv_ok loaded, got %d conversations", len(convs))
	}
}

// TestStorePath verifies the Path method returns the absolute file path for
// a conversation ID and rejects unsafe path segments.
func TestStorePath(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := s.Path("conv_abc123")
	want := filepath.Join(dir, "conversations", "conv_abc123.json")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	// Unsafe IDs must return "" to prevent path traversal.
	if s.Path("../etc/passwd") != "" {
		t.Error("Path should reject path traversal attempts")
	}
	if s.Path("") != "" {
		t.Error("Path should reject empty ID")
	}
}
