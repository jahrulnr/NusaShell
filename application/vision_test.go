package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

func containsImageOmissionNote(content string) bool {
	return containsOmissionNote(content, "image")
}

func TestChatMessagesStripsImagesForNonVisionModel(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "What's in this image?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo=", FilePath: "/data/attachments/c1/cat.png"},
				{Type: "text", Name: "note.txt", MediaType: "text/plain", Content: "see image"},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "I see a cat", Status: domain.StatusDone},
	}}

	// Non-vision model: image stripped, text attachment kept, placeholder added
	got := chatMessages(c, "", ModelCapabilities{})
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	userMsg := got[0]
	if userMsg.Role != "user" {
		t.Fatalf("first message role = %q, want user", userMsg.Role)
	}
	hasImage := false
	for _, a := range userMsg.Attachments {
		if a.Type == "image" {
			hasImage = true
		}
	}
	if hasImage {
		t.Error("image attachment should be stripped for non-vision model")
	}
	hasText := false
	for _, a := range userMsg.Attachments {
		if a.Type == "text" {
			hasText = true
		}
	}
	if !hasText {
		t.Error("text attachment should be preserved")
	}
	if !containsImageOmissionNote(userMsg.Content) {
		t.Errorf("content should contain image omission placeholder, got: %q", userMsg.Content)
	}
	// The placeholder must include the absolute file path and tell the model
	// to call read_image with file_path.
	if !strings.Contains(userMsg.Content, "/data/attachments/c1/cat.png") {
		t.Errorf("placeholder should include absolute file path, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "read_image") {
		t.Errorf("placeholder should mention read_image tool, got: %q", userMsg.Content)
	}
}

func TestChatMessagesPlaceholderIncludesFilePath(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "What's in this image?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "photo.jpg", MediaType: "image/jpeg", DataURL: "data:image/jpeg;base64,/9j/4AAQ=", FilePath: "/home/user/.config/nusashell/attachments/conv_1/photo.jpg"},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "ok", Status: domain.StatusDone},
	}}

	got := chatMessages(c, "", ModelCapabilities{})
	userMsg := got[0]
	// The placeholder must include the absolute file path so file-based
	// tools can access the image directly.
	if !strings.Contains(userMsg.Content, "/home/user/.config/nusashell/attachments/conv_1/photo.jpg") {
		t.Errorf("placeholder should include absolute file path, got: %q", userMsg.Content)
	}
}

func TestChatMessagesKeepsImagesForVisionModel(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "What's in this image?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "I see a cat", Status: domain.StatusDone},
	}}

	// Vision model: image kept, no placeholder
	got := chatMessages(c, "", ModelCapabilities{Vision: true})
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	userMsg := got[0]
	hasImage := false
	for _, a := range userMsg.Attachments {
		if a.Type == "image" {
			hasImage = true
		}
	}
	if !hasImage {
		t.Error("image attachment should be kept for vision model")
	}
	if containsImageOmissionNote(userMsg.Content) {
		t.Error("content should NOT contain image omission placeholder for vision model")
	}
}

func TestChatMessagesStripsImagesFromHistoryWhenSwitchingModel(t *testing.T) {
	// Simulate: turn 1 with vision model (image in history),
	// turn 2 with non-vision model (image should be stripped from history)
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "What's in this image?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "I see a cat on a windowsill", Status: domain.StatusDone},
		{ID: "u2", Role: domain.RoleUser, Content: "What color is it?"},
	}}

	// Non-vision model: image from u1 stripped, but assistant response a1 preserved
	got := chatMessages(c, "", ModelCapabilities{})
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
	// u1 should have no image
	u1 := got[0]
	for _, a := range u1.Attachments {
		if a.Type == "image" {
			t.Error("image from u1 should be stripped for non-vision model")
		}
	}
	if !containsImageOmissionNote(u1.Content) {
		t.Errorf("u1 content should contain placeholder, got: %q", u1.Content)
	}
	// a1 should be intact
	if got[1].Content != "I see a cat on a windowsill" {
		t.Errorf("assistant response should be preserved, got: %q", got[1].Content)
	}
}

func TestModelSupportsVision(t *testing.T) {
	// Model with Vision=true
	p := &domain.Provider{Models: []domain.Model{
		{ID: "gpt-5.5", Vision: true},
		{ID: "gpt-4", Vision: false},
	}}
	if !modelSupportsVision(p, "gpt-5.5") {
		t.Error("gpt-5.5 should support vision")
	}
	if modelSupportsVision(p, "gpt-4") {
		t.Error("gpt-4 should NOT support vision")
	}
	// Unknown model: default true (backward compat)
	if !modelSupportsVision(p, "unknown-model") {
		t.Error("unknown model should default to vision=true")
	}
	// Nil provider: default true
	if !modelSupportsVision(nil, "any") {
		t.Error("nil provider should default to vision=true")
	}
}
