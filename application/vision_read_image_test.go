package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

// testAbsPath returns an absolute path under dir (a t.TempDir). On Windows,
// root-anchored paths without a drive letter (\\data\...) are NOT absolute
// per filepath.IsAbs, so the dir parameter must be a platform-native
// absolute base (t.TempDir()).
func testAbsPath(dir string, parts ...string) string {
	return filepath.Join(append([]string{dir}, parts...)...)
}

// filePathArgs builds a read_media args JSON with the given file path.
// encoding/json escapes the path properly (backslashes on Windows).
func filePathArgs(path string, question string) string {
	m := map[string]string{"file_path": path}
	if question != "" {
		m["question"] = question
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// writeTestFile writes a small media payload with real binary magic
// numbers so loadMediaAttachment's magic-number validation accepts it.
// The file does not need to be a decodable media file — only the
// leading bytes must match the expected magic signature.
func writeTestFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	ext := strings.ToLower(filepath.Ext(name))
	var payload []byte
	switch ext {
	case ".png":
		// PNG signature + minimal IHDR chunk header
		payload = append(
			[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
			[]byte("\x00\x00\x00\x0DIHDR\x00\x00\x00\x01")...,
		)
	case ".jpg", ".jpeg":
		payload = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	case ".gif":
		payload = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00")
	case ".webp":
		payload = []byte("RIFF\x00\x00\x00\x00WEBP")
	case ".bmp":
		payload = []byte("BM\x00\x00\x00\x00")
	case ".mp3":
		payload = []byte("ID3\x03\x00\x00\x00\x00\x00\x00")
	case ".wav":
		payload = []byte("RIFF\x00\x00\x00\x00WAVE")
	case ".ogg":
		payload = []byte("OggS\x00\x00\x00\x00")
	case ".flac":
		payload = []byte("fLaC\x00\x00\x00\x22")
	case ".mp4", ".m4v":
		payload = []byte("\x00\x00\x00\x20ftypisom\x00\x00\x00\x00isomiso2avc1mp41")
	case ".webm":
		payload = []byte("\x1aE\xdf\xa3\x01\x00\x00\x00\x00\x00\x00\x1fB\x82\x88webm")
	case ".mov":
		payload = []byte("\x00\x00\x00\x14ftypqt  \x00\x00\x00\x00")
	case ".avi":
		payload = []byte("RIFF\x00\x00\x00\x00AVI ")
	default:
		t.Fatalf("writeTestFile: no magic payload for extension %q (file %s)", ext, name)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExecuteReadImageVisionModel(t *testing.T) {
	dir := t.TempDir()
	catPath := writeTestFile(t, dir, "cat.png")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(catPath, "what color is the cat?"),
	}

	output, atts, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Vision-model tool output is the file path only; the image is delivered
	// as an attachment and reinjected as a user message by chat-compat
	// providers (or carried inside the tool result by native providers).
	if !strings.Contains(output, catPath) {
		t.Errorf("output should be the file path, got: %q", output)
	}
	// Question echo was removed from vision-model output (the model already
	// knows its own question — echoing it wastes tokens).
	if strings.Contains(output, "what color") {
		t.Errorf("vision output should NOT echo the question, got: %q", output)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment (image), got %d", len(atts))
	}
	if atts[0].Type != "image" || atts[0].Name != "cat.png" {
		t.Errorf("attachment should be the image, got %q %q", atts[0].Type, atts[0].Name)
	}
	if atts[0].MediaType != "image/png" {
		t.Errorf("media type should be image/png, got %q", atts[0].MediaType)
	}
	if atts[0].DataURL == "" {
		t.Error("attachment should carry inline data URL")
	}
}

func TestExecuteReadImageOutsideWorkspace(t *testing.T) {
	// Absolute path outside any workspace/attachment root must load fine.
	dir := t.TempDir()
	sub := filepath.Join(dir, "elsewhere", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeTestFile(t, sub, "photo.jpg")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(path, ""),
	}

	output, atts, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error for path outside attachment roots: %v", err)
	}
	if !strings.Contains(output, path) {
		t.Errorf("output should be the file path, got: %q", output)
	}
	if len(atts) != 1 || atts[0].MediaType != "image/jpeg" {
		t.Fatalf("expected jpeg attachment from arbitrary path, got %+v", atts)
	}
}

func TestExecuteReadImageNonVisionNoFallback(t *testing.T) {
	dir := t.TempDir()
	catPath := writeTestFile(t, dir, "cat.png")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(catPath, ""),
	}

	output, atts, err := app.executeReadImage(run, toolCall, ModelCapabilities{}, domain.Settings{})
	// No fallback configured → returns error message, no attachments
	if err != nil {
		t.Fatalf("expected graceful error message, not Go error: %v", err)
	}
	if !strings.Contains(output, "does not support image input") {
		t.Errorf("output should explain no vision support, got: %q", output)
	}
	if len(atts) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(atts))
	}
}

func TestExecuteReadImageImageNotFound(t *testing.T) {
	dir := t.TempDir()
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(testAbsPath(dir, "nonexistent.png"), ""),
	}

	output, _, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for nonexistent image")
	}
	if !strings.Contains(output, "not found") {
		t.Errorf("output should mention not found, got: %q", output)
	}
}

func TestExecuteReadImageMissingArgs(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{}`,
	}

	output, _, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for missing file_path")
	}
	if !strings.Contains(output, "file_path is required") {
		t.Errorf("output should mention missing arg, got: %q", output)
	}
}

func TestExecuteReadImageRejectsRelativePath(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{"file_path":"cat.png"}`,
	}

	output, _, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for relative path")
	}
	if !strings.Contains(output, "absolute") {
		t.Errorf("output should mention absolute path required, got: %q", output)
	}
}
