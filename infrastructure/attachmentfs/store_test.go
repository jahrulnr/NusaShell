package attachmentfs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// TestWriteBytesConcurrentSameName proves the atomic write is
// collision-safe: concurrent writes to the same name must not tear, and
// no temp file may be left behind.
func TestWriteBytesConcurrentSameName(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	payloads := [][]byte{
		[]byte("AAAAAAAA"),
		[]byte("BBBBBBBB"),
		[]byte("CCCCCCCC"),
		[]byte("DDDDDDDD"),
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.WriteBytes("conv_race", "same.bin", payloads[i%len(payloads)]); err != nil {
				t.Errorf("WriteBytes: %v", err)
			}
		}(i)
	}
	wg.Wait()

	got, err := os.ReadFile(filepath.Join(dir, "conv_race", "same.bin"))
	if err != nil {
		t.Fatal(err)
	}
	valid := false
	for _, p := range payloads {
		if string(got) == string(p) {
			valid = true
		}
	}
	if !valid {
		t.Fatalf("final content %q is torn (not one of the written payloads)", got)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "conv_race"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

// TestWriteBytesReplacesExistingFile proves a rewrite fully replaces the
// old content rather than appending or leaving a partial file.
func TestWriteBytesReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteBytes("conv", "f.bin", []byte("long-original-content")); err != nil {
		t.Fatal(err)
	}
	path, err := store.WriteBytes("conv", "f.bin", []byte("short"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "short" {
		t.Fatalf("content = %q, want %q", got, "short")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// Windows has no POSIX permission bits: chmod(0o644) only maps to the
		// read-only attribute, so os.Stat reports 0666 for a writable file.
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("mode = %o, want 644", info.Mode().Perm())
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
