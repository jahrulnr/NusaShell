package mediaread

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realPNGHeader is a minimal valid PNG signature + IHDR chunk so the
// magic-number sniffer accepts it. The file does not need to be a
// decodable image — only the leading bytes must match the PNG magic.
var realPNGHeader = append(
	[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
	[]byte("\x00\x00\x00\x0DIHDR\x00\x00\x00\x01")...,
)

// realMP3Header is an ID3v2 tag header — the magic sniffer accepts any
// file starting with "ID3".
var realMP3Header = []byte("ID3\x03\x00\x00\x00\x00\x00\x00")

// realMP4Header is a minimal ftyp box for an MP4 video.
var realMP4Header = []byte("\x00\x00\x00\x20ftypisom\x00\x00\x00\x00isomiso2avc1mp41")

func TestLoadMediaAttachment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp3")
	if err := os.WriteFile(path, realMP3Header, 0o644); err != nil {
		t.Fatal(err)
	}

	att, err := LoadMediaAttachment("audio", path)
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
	_, err := LoadMediaAttachment("audio", filepath.Join(dir, "missing.mp3"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}
}

func TestLoadMediaAttachmentDirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadMediaAttachment("video", dir)
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected directory error, got: %v", err)
	}
}

func TestLoadMediaAttachmentRejectsOversizedFile(t *testing.T) {
	orig := MaxReadMediaBytes
	MaxReadMediaBytes = 16
	defer func() { MaxReadMediaBytes = orig }()

	path := filepath.Join(t.TempDir(), "big.mp4")
	if err := os.WriteFile(path, realMP4Header, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadMediaAttachment("video", path)
	if err == nil || !strings.Contains(err.Error(), "exceeding") {
		t.Fatalf("expected oversize error, got: %v", err)
	}
}

// ── Magic-number validation tests ───────────────────────────────────

// TestLoadMediaAttachmentRejectsNonMedia proves that a file claiming to
// be an image by extension but containing JavaScript is rejected. This
// is the core guard: extensions can be lied about, magic bytes cannot.
func TestLoadMediaAttachmentRejectsNonMedia(t *testing.T) {
	dir := t.TempDir()
	// A .js file renamed to .png — extension says image, bytes say JS.
	path := filepath.Join(dir, "app.png")
	js := []byte(`const app = () => { console.log("pretending to be PNG"); };`)
	if err := os.WriteFile(path, js, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadMediaAttachment("image", path)
	if err == nil {
		t.Fatal("expected error for non-media file with image extension, got nil")
	}
	if !strings.Contains(err.Error(), "magic") && !strings.Contains(err.Error(), "not a valid") {
		t.Fatalf("error should mention magic/validity, got: %v", err)
	}
}

// TestLoadMediaAttachmentRejectsKindMismatch proves that a real audio
// file passed to read_media (kind=image) is rejected — the magic bytes
// must match the expected kind.
func TestLoadMediaAttachmentRejectsKindMismatch(t *testing.T) {
	dir := t.TempDir()
	// A real MP3 file with .png extension — magic says audio, but
	// the caller expects image.
	path := filepath.Join(dir, "trick.png")
	if err := os.WriteFile(path, realMP3Header, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadMediaAttachment("image", path)
	if err == nil {
		t.Fatal("expected error for audio file loaded as image, got nil")
	}
	if !strings.Contains(err.Error(), "audio") {
		t.Fatalf("error should mention detected kind (audio), got: %v", err)
	}
}

// TestSniffMediaKindDetectsSVGWithXMLProlog verifies that SniffMediaKind
// reads enough bytes to find "<svg" past an XML prolog. The old 32-byte
// read window missed SVGs with <?xml version="1.0" encoding="UTF-8"?>
// because "<svg" appears at byte 38+.
func TestSniffMediaKindDetectsSVGWithXMLProlog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "diagram.svg")
	svg := `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
  <rect width="100" height="100"/>
</svg>`
	if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	kind, err := SniffMediaKind(mustMarshal(t, map[string]any{"file_path": path}))
	if err != nil {
		t.Fatalf("SniffMediaKind failed for SVG with XML prolog: %v", err)
	}
	if kind != "image" {
		t.Errorf("kind = %q, want image", kind)
	}
}

// TestLoadMediaAttachmentRejectsSVGAsImageInput verifies that read_media
// rejects SVG files with a clear error. Most providers (OpenAI, Anthropic)
// do not support SVG as image input — they reject data:image/svg+xml
// URLs. The error directs the user to show(op=image) for UI display.
func TestLoadMediaAttachmentRejectsSVGAsImageInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chart.svg")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100"/></svg>`)
	if err := os.WriteFile(path, svg, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadMediaAttachment("image", path)
	if err == nil {
		t.Fatal("expected error for SVG via read_media, got nil")
	}
	if !strings.Contains(err.Error(), "svg") && !strings.Contains(err.Error(), "SVG") {
		t.Fatalf("error should mention SVG, got: %v", err)
	}
	if !strings.Contains(err.Error(), "show") {
		t.Fatalf("error should suggest show(op=image), got: %v", err)
	}
}

// TestLoadMediaAttachmentAcceptsRealPNG proves that a real PNG file
// passes magic validation and gets the sniffed media type (not the
// extension-based one).
func TestLoadMediaAttachmentAcceptsRealPNG(t *testing.T) {
	dir := t.TempDir()
	// Write a PNG with .bin extension to prove the media type comes
	// from magic bytes, not the extension.
	path := filepath.Join(dir, "image.bin")
	if err := os.WriteFile(path, realPNGHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	att, err := LoadMediaAttachment("image", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.MediaType != "image/png" {
		t.Errorf("media type = %q, want image/png (from magic, not extension)", att.MediaType)
	}
}

// TestLoadMediaAttachmentAcceptsRealMP4 proves that a real MP4 video
// passes magic validation.
func TestLoadMediaAttachmentAcceptsRealMP4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, realMP4Header, 0o644); err != nil {
		t.Fatal(err)
	}
	att, err := LoadMediaAttachment("video", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.MediaType != "video/mp4" {
		t.Errorf("media type = %q, want video/mp4", att.MediaType)
	}
}

// TestLoadMediaAttachmentRejectsEmptyFile proves that a zero-byte file
// is rejected by the magic check, not silently accepted.
func TestLoadMediaAttachmentRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.png")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadMediaAttachment("image", path)
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
