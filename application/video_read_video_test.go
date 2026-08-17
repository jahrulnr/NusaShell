package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestFindVideoAttachmentByPath(t *testing.T) {
	dir := t.TempDir()
	videoPath := testAbsPath(dir, "clip.mp4")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "watch", Attachments: []domain.Attachment{
			{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,abc", FilePath: videoPath},
			{Type: "text", Name: "note.txt", MediaType: "text/plain", Content: "see video"},
		}},
		{ID: "a1", Role: domain.RoleAssistant, Content: "ok"},
		{ID: "u2", Role: domain.RoleUser, Content: "another", Attachments: []domain.Attachment{
			{Type: "image", Name: "dog.jpg", MediaType: "image/jpeg", DataURL: "data:image/jpeg;base64,def", FilePath: testAbsPath(dir, "dog.jpg")},
		}},
	}}

	// Exact match
	vid, err := findVideoAttachmentByPath(conv, videoPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vid.Name != "clip.mp4" {
		t.Errorf("got %q, want clip.mp4", vid.Name)
	}

	// Case-insensitive match
	vid, err = findVideoAttachmentByPath(conv, strings.ToUpper(videoPath))
	if err != nil {
		t.Fatalf("unexpected error on case-insensitive: %v", err)
	}
	if vid.Name != "clip.mp4" {
		t.Errorf("got %q, want clip.mp4", vid.Name)
	}

	// Not found
	_, err = findVideoAttachmentByPath(conv, testAbsPath(dir, "nonexistent.mp4"))
	if err == nil {
		t.Error("expected error for nonexistent video")
	}
}

func TestExecuteReadVideoNative(t *testing.T) {
	dir := t.TempDir()
	videoPath := testAbsPath(dir, "clip.mp4")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "watch", Attachments: []domain.Attachment{
			{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,abc", FilePath: videoPath},
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
		Name: "read_video",
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
}

func TestExecuteReadVideoNonVideoNoFallback(t *testing.T) {
	dir := t.TempDir()
	videoPath := testAbsPath(dir, "clip.mp4")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "watch", Attachments: []domain.Attachment{
			{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,abc", FilePath: videoPath},
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
		Name: "read_video",
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
	videoPath := testAbsPath(dir, "clip.mp4")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "watch", Attachments: []domain.Attachment{
			{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,abc", FilePath: videoPath},
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
		Name: "read_video",
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
		Name: "read_video",
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
		Name: "read_video",
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
