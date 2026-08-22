package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMediaAttachment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp3")
	if err := os.WriteFile(path, []byte("ID3 fake audio payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	att, err := loadMediaAttachment("audio", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Type != "audio" {
		t.Errorf("type = %q, want audio", att.Type)
	}
	if att.Name != "clip.mp3" {
		t.Errorf("name = %q, want clip.mp3", att.Name)
	}
	if att.MediaType != "audio/mpeg" {
		t.Errorf("media type = %q, want audio/mpeg", att.MediaType)
	}
	if att.FilePath != path {
		t.Errorf("file path = %q, want %q", att.FilePath, path)
	}
	if !strings.HasPrefix(att.DataURL, "data:audio/mpeg;base64,") {
		t.Errorf("data url should be audio/mpeg base64, got prefix %q", att.DataURL[:30])
	}
}

func TestLoadMediaAttachmentNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := loadMediaAttachment("audio", filepath.Join(dir, "missing.mp3"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}
}

func TestLoadMediaAttachmentDirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	_, err := loadMediaAttachment("video", dir)
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected directory error, got: %v", err)
	}
}

func TestLoadMediaAttachmentUnknownExtensionFallsBackToOctetStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, []byte("opaque"), 0o644); err != nil {
		t.Fatal(err)
	}
	att, err := loadMediaAttachment("image", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.MediaType != "application/octet-stream" {
		t.Errorf("media type = %q, want application/octet-stream", att.MediaType)
	}
}

func TestLoadMediaAttachmentRejectsOversizedFile(t *testing.T) {
	orig := maxReadMediaBytes
	maxReadMediaBytes = 16
	defer func() { maxReadMediaBytes = orig }()

	path := filepath.Join(t.TempDir(), "big.mp4")
	if err := os.WriteFile(path, make([]byte, 17), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadMediaAttachment("video", path)
	if err == nil || !strings.Contains(err.Error(), "exceeding") {
		t.Fatalf("expected oversize error, got: %v", err)
	}
}
