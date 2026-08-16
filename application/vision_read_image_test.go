package application

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

// absTestPath builds an absolute path on any OS: Linux gets /data/...,
// Windows gets \\data\\. Both satisfy filepath.IsAbs.
func absTestPath(parts ...string) string {
	return filepath.Join(append([]string{string(filepath.Separator), "data"}, parts...)...)
}

// filePathArgs builds a read_image args JSON with the given file path.
func filePathArgs(path string, question string) string {
	qs := ""
	if question != "" {
		qs = ",\"question\":\"" + question + "\""
	}
	return "{\"file_path\":\"" + path + "\"" + qs + "}"
}

func TestFindImageAttachmentByPath(t *testing.T) {
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "look", Attachments: []domain.Attachment{
			{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,abc", FilePath: absTestPath("attachments", "c1", "cat.png")},
			{Type: "text", Name: "note.txt", MediaType: "text/plain", Content: "see image"},
		}},
		{ID: "a1", Role: domain.RoleAssistant, Content: "ok"},
		{ID: "u2", Role: domain.RoleUser, Content: "another", Attachments: []domain.Attachment{
			{Type: "image", Name: "dog.jpg", MediaType: "image/jpeg", DataURL: "data:image/jpeg;base64,def", FilePath: absTestPath("attachments", "c1", "dog.jpg")},
		}},
	}}

	// Exact match
	img, err := findImageAttachmentByPath(conv, absTestPath("attachments", "c1", "cat.png"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img.Name != "cat.png" {
		t.Errorf("got %q, want cat.png", img.Name)
	}

	// Case-insensitive match
	img, err = findImageAttachmentByPath(conv, "/DATA/ATTACHMENTS/C1/CAT.PNG")
	if err != nil {
		t.Fatalf("unexpected error for case-insensitive: %v", err)
	}
	if img.Name != "cat.png" {
		t.Errorf("got %q, want cat.png", img.Name)
	}

	// Second image in different message
	img, err = findImageAttachmentByPath(conv, absTestPath("attachments", "c1", "dog.jpg"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img.Name != "dog.jpg" {
		t.Errorf("got %q, want dog.jpg", img.Name)
	}

	// Not found
	_, err = findImageAttachmentByPath(conv, absTestPath("attachments", "c1", "nonexistent.png"))
	if err == nil {
		t.Error("expected error for nonexistent image")
	}
}

func TestExecuteReadImageVisionModel(t *testing.T) {
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "look", Attachments: []domain.Attachment{
			{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,abc", FilePath: absTestPath("attachments", "c1", "cat.png")},
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
		Name: "read_image",
		Args: filePathArgs(absTestPath("attachments", "c1", "cat.png"), "what color is the cat?"),
	}

	output, atts, err := app.executeReadImage(run, toolCall, true, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "Image loaded") {
		t.Errorf("output should mention image loaded, got: %q", output)
	}
	if !strings.Contains(output, "what color") {
		t.Errorf("output should include question, got: %q", output)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment (image), got %d", len(atts))
	}
	if atts[0].Type != "image" || atts[0].Name != "cat.png" {
		t.Errorf("attachment should be the image, got %q %q", atts[0].Type, atts[0].Name)
	}
}

func TestExecuteReadImageNonVisionNoFallback(t *testing.T) {
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "look", Attachments: []domain.Attachment{
			{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,abc", FilePath: absTestPath("attachments", "c1", "cat.png")},
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
		Name: "read_image",
		Args: filePathArgs(absTestPath("attachments", "c1", "cat.png"), ""),
	}

	output, atts, err := app.executeReadImage(run, toolCall, false, domain.Settings{})
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
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "look", Attachments: []domain.Attachment{
			{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,abc", FilePath: absTestPath("attachments", "c1", "cat.png")},
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
		Name: "read_image",
		Args: filePathArgs(absTestPath("attachments", "c1", "nonexistent.png"), ""),
	}

	output, _, err := app.executeReadImage(run, toolCall, true, domain.Settings{})
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
		Name: "read_image",
		Args: `{}`,
	}

	output, _, err := app.executeReadImage(run, toolCall, true, domain.Settings{})
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
		Name: "read_image",
		Args: `{"file_path":"cat.png"}`,
	}

	output, _, err := app.executeReadImage(run, toolCall, true, domain.Settings{})
	if err == nil {
		t.Error("expected error for relative path")
	}
	if !strings.Contains(output, "absolute") {
		t.Errorf("output should mention absolute path required, got: %q", output)
	}
	_ = filepath.IsAbs // keep import used
}
