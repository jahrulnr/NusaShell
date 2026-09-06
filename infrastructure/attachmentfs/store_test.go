package attachmentfs

import (
	"os"
	"path/filepath"
	"testing"

	"nusashell/domain"
)

func TestSaveImageWritesFileAndReturnsAbsPath(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 1x1 PNG (transparent)
	pngDataURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
	att := domain.Attachment{
		Type:      "image",
		Name:      "test.png",
		MediaType: "image/png",
		DataURL:   pngDataURL,
	}

	path, err := store.Save("conv_123", att)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Path must be absolute and under dir/conv_123/
	if !filepath.IsAbs(path) {
		t.Errorf("path is not absolute: %s", path)
	}
	expectedSuffix := filepath.Join("conv_123", "test.png")
	if !filepath.IsAbs(path) || filepath.Base(filepath.Dir(path)) != "conv_123" || filepath.Base(path) != "test.png" {
		t.Errorf("path = %s, want .../%s", path, expectedSuffix)
	}

	// File must exist and contain the decoded PNG bytes.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Errorf("file content is not a valid PNG, got %d bytes starting with %v", len(data), data[:min(8, len(data))])
	}
}

func TestSaveTextReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	att := domain.Attachment{
		Type:      "text",
		Name:      "note.txt",
		MediaType: "text/plain",
		Content:   "hello",
	}

	path, err := store.Save("conv_123", att)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if path != "" {
		t.Errorf("text attachment should return empty path, got %s", path)
	}
}

func TestSaveCreatesRootIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "attachments")
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New with nested dir failed: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("root dir was not created: %v", err)
	}
	_ = store
}

func TestWriteBytesAndReadFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3}
	path, err := store.WriteBytes("conv_gen", "gen-tc1.png", payload)
	if err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	got, err := store.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("round trip mismatch: %v vs %v", got, payload)
	}
}

func TestReadFileRejectsPathOutsideRoot(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadFile(outside); err == nil {
		t.Fatal("expected outside-root error")
	}
}

func TestWriteBytesRejectsTraversalName(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteBytes("conv", "../escape.png", []byte("x")); err == nil {
		t.Fatal("expected invalid name error")
	}
}

// TestRemoveDeletesDirAndIsIdempotent pins the cascade hook used by
// handleConversationsDelete: Remove must delete <root>/<id>/ entirely, and
// a missing dir is a no-op success so retried deletes stay safe.
func TestRemoveDeletesDirAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteBytes("conv_1", "a.png", []byte("A")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteBytes("conv_1", "b.png", []byte("B")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteBytes("conv_2", "sibling.png", []byte("S")); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("conv_1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "conv_1")); !os.IsNotExist(err) {
		t.Errorf("expected conv_1 dir gone, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "conv_2", "sibling.png")); err != nil {
		t.Errorf("sibling conv_2 must survive: %v", err)
	}
	// Idempotent: removing again is a no-op success.
	if err := store.Remove("conv_1"); err != nil {
		t.Errorf("second Remove must succeed (no-op), got %v", err)
	}
	if err := store.Remove("never-existed"); err != nil {
		t.Errorf("Remove of missing id must succeed, got %v", err)
	}
}

// TestRemoveRejectsTraversalID guards the cascade from path traversal: a
// hostile conversationID must be rejected before any RemoveAll.
func TestRemoveRejectsTraversalID(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-existing target outside the store: the cascade must not touch it.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../escape", "..", "a/b", `..\win`, "conv_\x00x"} {
		if err := store.Remove(id); err == nil {
			t.Errorf("Remove(%q) = nil, want an error", id)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("sentinel outside store was deleted: %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
