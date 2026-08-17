package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestFindAudioAttachmentByPath(t *testing.T) {
	dir := t.TempDir()
	audioPath := testAbsPath(dir, "recording.mp3")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "listen", Attachments: []domain.Attachment{
			{Type: "audio", Name: "recording.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,abc", FilePath: audioPath},
			{Type: "text", Name: "note.txt", MediaType: "text/plain", Content: "see audio"},
		}},
		{ID: "a1", Role: domain.RoleAssistant, Content: "ok"},
		{ID: "u2", Role: domain.RoleUser, Content: "another", Attachments: []domain.Attachment{
			{Type: "image", Name: "dog.jpg", MediaType: "image/jpeg", DataURL: "data:image/jpeg;base64,def", FilePath: testAbsPath(dir, "dog.jpg")},
		}},
	}}

	// Exact match
	aud, err := findAudioAttachmentByPath(conv, audioPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aud.Name != "recording.mp3" {
		t.Errorf("got %q, want recording.mp3", aud.Name)
	}

	// Case-insensitive match
	aud, err = findAudioAttachmentByPath(conv, strings.ToUpper(audioPath))
	if err != nil {
		t.Fatalf("unexpected error on case-insensitive: %v", err)
	}
	if aud.Name != "recording.mp3" {
		t.Errorf("got %q, want recording.mp3", aud.Name)
	}

	// Not found
	_, err = findAudioAttachmentByPath(conv, testAbsPath(dir, "nonexistent.mp3"))
	if err == nil {
		t.Error("expected error for nonexistent audio")
	}
}

func TestExecuteReadAudioNative(t *testing.T) {
	dir := t.TempDir()
	audioPath := testAbsPath(dir, "recording.mp3")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "listen", Attachments: []domain.Attachment{
			{Type: "audio", Name: "recording.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,abc", FilePath: audioPath},
		}},
	}}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
	}
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
}

func TestExecuteReadAudioNonAudioNoFallback(t *testing.T) {
	dir := t.TempDir()
	audioPath := testAbsPath(dir, "recording.mp3")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "listen", Attachments: []domain.Attachment{
			{Type: "audio", Name: "recording.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,abc", FilePath: audioPath},
		}},
	}}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
	}
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
	audioPath := testAbsPath(dir, "recording.mp3")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "listen", Attachments: []domain.Attachment{
			{Type: "audio", Name: "recording.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,abc", FilePath: audioPath},
		}},
	}}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
	}
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
