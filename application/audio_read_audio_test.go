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
		Name: "read_media",
		Args: filePathArgs(audioPath, "what is being said?"),
	}

	output, atts, err := app.executeReadAudio(run, toolCall, ModelCapabilities{Audio: true}, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "Audio loaded") {
		t.Errorf("output should mention audio loaded, got: %q", output)
	}
	// Question echo removed from native audio output (model already knows).
	if strings.Contains(output, "what is being said") {
		t.Errorf("audio output should NOT echo the question, got: %q", output)
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
		Name: "read_media",
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
		Name: "read_media",
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
		Name: "read_media",
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
		Name: "read_media",
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

func TestDescribeAudiosWithFallbackSkipsExistingTranscript(t *testing.T) {
	adapter := &fakeVisionAdapter{description: "should not run"}
	var factoryCalls int
	app := visionFallbackTestApp(adapter, &factoryCalls)
	settings := domain.Settings{AudioProviderID: "vision-prov", AudioModelID: "gpt-4o"}
	atts := []domain.Attachment{
		{Type: "audio", Name: "clip.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,AAA="},
		{Type: "text", Name: "audio:clip.mp3", MediaType: "text/plain", Content: "[Audio transcript for clip.mp3]\nexisting"},
	}
	out := app.describeAudiosWithFallback(context.Background(), settings, atts)
	if len(out) != 2 {
		t.Fatalf("expected unchanged attachments, got %d", len(out))
	}
	if factoryCalls != 0 || adapter.calls != 0 {
		t.Fatalf("audio fallback called factory=%d chat=%d, want 0/0", factoryCalls, adapter.calls)
	}
}

func TestUndescribedMediaIndexes(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "image", Name: "a.png"},
		{Type: "text", Name: "vision:a.png"},
		{Type: "image", Name: "b.png"},
		{Type: "audio", Name: "c.mp3"},
	}
	got := undescribedMediaIndexes(atts, "image", mediaDescPrefixVision)
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("undescribed images = %v, want [2]", got)
	}
	if n := undescribedMediaIndexes(atts, "audio", mediaDescPrefixAudio); len(n) != 1 || n[0] != 3 {
		t.Fatalf("undescribed audio = %v, want [3]", n)
	}
	if n := undescribedMediaIndexes(nil, "image", mediaDescPrefixVision); len(n) != 0 {
		t.Fatalf("empty attachments = %v, want []", n)
	}
}
