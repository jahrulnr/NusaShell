package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestExecuteReadVideoNative(t *testing.T) {
	dir := t.TempDir()
	videoPath := writeTestFile(t, dir, "clip.mp4")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(videoPath, "what happens in the video?"),
	}

	output, atts, err := app.executeReadVideo(run, toolCall, ModelCapabilities{Video: true}, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "Video loaded") {
		t.Errorf("output should mention video loaded, got: %q", output)
	}
	if !strings.Contains(output, "what happens") {
		t.Errorf("output should include question, got: %q", output)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment (video), got %d", len(atts))
	}
	if atts[0].Type != "video" || atts[0].Name != "clip.mp4" {
		t.Errorf("attachment should be the video, got %q %q", atts[0].Type, atts[0].Name)
	}
	if atts[0].MediaType != "video/mp4" || atts[0].DataURL == "" {
		t.Errorf("attachment should be inline mp4 video, got %q url=%v", atts[0].MediaType, atts[0].DataURL != "")
	}
}

func TestExecuteReadVideoNonVideoNoFallback(t *testing.T) {
	dir := t.TempDir()
	videoPath := writeTestFile(t, dir, "clip.mp4")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(videoPath, ""),
	}

	output, atts, err := app.executeReadVideo(run, toolCall, ModelCapabilities{}, domain.Settings{})
	if err != nil {
		t.Fatalf("expected graceful error message, not Go error: %v", err)
	}
	if !strings.Contains(output, "does not support video input") {
		t.Errorf("output should explain no video support, got: %q", output)
	}
	if len(atts) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(atts))
	}
}

func TestExecuteReadVideoVideoNotFound(t *testing.T) {
	dir := t.TempDir()
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(testAbsPath(dir, "nonexistent.mp4"), ""),
	}

	output, _, err := app.executeReadVideo(run, toolCall, ModelCapabilities{Video: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for nonexistent video")
	}
	if !strings.Contains(output, "not found") {
		t.Errorf("output should mention not found, got: %q", output)
	}
}

func TestExecuteReadVideoMissingArgs(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{}`,
	}

	output, _, err := app.executeReadVideo(run, toolCall, ModelCapabilities{Video: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for missing file_path")
	}
	if !strings.Contains(output, "file_path is required") {
		t.Errorf("output should mention missing arg, got: %q", output)
	}
}

func TestExecuteReadVideoRejectsRelativePath(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{"file_path":"clip.mp4"}`,
	}

	output, _, err := app.executeReadVideo(run, toolCall, ModelCapabilities{Video: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for relative path")
	}
	if !strings.Contains(output, "absolute") {
		t.Errorf("output should mention absolute path required, got: %q", output)
	}
}
