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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
