package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestExecuteReadAudioNative(t *testing.T) {
	dir := t.TempDir()
	audioPath := writeTestFile(t, dir, "recording.mp3")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_audio",
		Args: filePathArgs(audioPath, "what is being said?"),
	}

	output, atts, err := app.executeReadAudio(run, toolCall, ModelCapabilities{Audio: true}, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "Audio loaded") {
		t.Errorf("output should mention audio loaded, got: %q", output)
	}
	if !strings.Contains(output, "what is being said") {
		t.Errorf("output should include question, got: %q", output)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment (audio), got %d", len(atts))
	}
	if atts[0].Type != "audio" || atts[0].Name != "recording.mp3" {
		t.Errorf("attachment should be the audio, got %q %q", atts[0].Type, atts[0].Name)
	}
	if atts[0].MediaType != "audio/mpeg" || atts[0].DataURL == "" {
		t.Errorf("attachment should be inline mpeg audio, got %q url=%v", atts[0].MediaType, atts[0].DataURL != "")
	}
}

func TestExecuteReadAudioNonAudioNoFallback(t *testing.T) {
	dir := t.TempDir()
	audioPath := writeTestFile(t, dir, "recording.mp3")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_audio",
		Args: filePathArgs(audioPath, ""),
	}

	output, atts, err := app.executeReadAudio(run, toolCall, ModelCapabilities{}, domain.Settings{})
	if err != nil {
		t.Fatalf("expected graceful error message, not Go error: %v", err)
	}
	if !strings.Contains(output, "does not support audio input") {
		t.Errorf("output should explain no audio support, got: %q", output)
	}
	if len(atts) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(atts))
	}
}

func TestExecuteReadAudioAudioNotFound(t *testing.T) {
	dir := t.TempDir()
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_audio",
		Args: filePathArgs(testAbsPath(dir, "nonexistent.mp3"), ""),
	}

	output, _, err := app.executeReadAudio(run, toolCall, ModelCapabilities{Audio: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for nonexistent audio")
	}
	if !strings.Contains(output, "not found") {
		t.Errorf("output should mention not found, got: %q", output)
	}
}

func TestExecuteReadAudioMissingArgs(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_audio",
		Args: `{}`,
	}

	output, _, err := app.executeReadAudio(run, toolCall, ModelCapabilities{Audio: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for missing file_path")
	}
	if !strings.Contains(output, "file_path is required") {
		t.Errorf("output should mention missing arg, got: %q", output)
	}
}

func TestExecuteReadAudioRejectsRelativePath(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_audio",
		Args: `{"file_path":"recording.mp3"}`,
	}

	output, _, err := app.executeReadAudio(run, toolCall, ModelCapabilities{Audio: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for relative path")
	}
	if !strings.Contains(output, "absolute") {
		t.Errorf("output should mention absolute path required, got: %q", output)
	}
}
