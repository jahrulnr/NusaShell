package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

func TestModelCapabilitiesUnknownModelDefaultsAllTrue(t *testing.T) {
	provider := &domain.Provider{ID: "p1", Models: nil}
	caps := modelCapabilities(provider, "unknown-model")
	if !caps.Vision || !caps.Audio || !caps.Video {
		t.Errorf("unknown model should default to all-true, got %+v", caps)
	}
}

func TestModelCapabilitiesNilProviderDefaultsAllTrue(t *testing.T) {
	caps := modelCapabilities(nil, "any")
	if !caps.Vision || !caps.Audio || !caps.Video {
		t.Errorf("nil provider should default to all-true, got %+v", caps)
	}
}

func TestModelCapabilitiesFromMetadata(t *testing.T) {
	provider := &domain.Provider{
		ID: "p1",
		Models: []domain.Model{
			{ID: "gemini-2.5-flash", Vision: true, Audio: true, Video: true},
			{ID: "gpt-4o", Vision: true, Audio: false, Video: false},
			{ID: "llama-3", Vision: false, Audio: false, Video: false},
		},
	}

	tests := []struct {
		model      string
		wantVision bool
		wantAudio  bool
		wantVideo  bool
	}{
		{"gemini-2.5-flash", true, true, true},
		{"gpt-4o", true, false, false},
		{"llama-3", false, false, false},
	}
	for _, tt := range tests {
		caps := modelCapabilities(provider, tt.model)
		if caps.Vision != tt.wantVision || caps.Audio != tt.wantAudio || caps.Video != tt.wantVideo {
			t.Errorf("model %s: got %+v, want vision=%v audio=%v video=%v",
				tt.model, caps, tt.wantVision, tt.wantAudio, tt.wantVideo)
		}
	}
}

func TestChatMessagesAudioPlaceholderForNonAudioModel(t *testing.T) {
	dir := t.TempDir()
	audioPath := testAbsPath(dir, "recording.mp3")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "listen to this", Attachments: []domain.Attachment{
			{Type: "audio", Name: "recording.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,abc", FilePath: audioPath},
		}},
	}}

	msgs := chatMessages(conv, "", ModelCapabilities{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	userMsg := msgs[0]
	if !strings.Contains(userMsg.Content, "audio content omitted") {
		t.Errorf("content should contain audio omission placeholder, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, audioPath) {
		t.Errorf("placeholder should include absolute file path, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "read_audio") {
		t.Errorf("placeholder should mention read_audio tool, got: %q", userMsg.Content)
	}
	// Audio attachment should be stripped (non-audio model)
	if len(userMsg.Attachments) != 0 {
		t.Errorf("expected 0 attachments (audio stripped), got %d", len(userMsg.Attachments))
	}
}

func TestChatMessagesVideoPlaceholderForNonVideoModel(t *testing.T) {
	dir := t.TempDir()
	videoPath := testAbsPath(dir, "clip.mp4")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "watch this", Attachments: []domain.Attachment{
			{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,abc", FilePath: videoPath},
		}},
	}}

	msgs := chatMessages(conv, "", ModelCapabilities{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	userMsg := msgs[0]
	if !strings.Contains(userMsg.Content, "video content omitted") {
		t.Errorf("content should contain video omission placeholder, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, videoPath) {
		t.Errorf("placeholder should include absolute file path, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "read_video") {
		t.Errorf("placeholder should mention read_video tool, got: %q", userMsg.Content)
	}
	// Video attachment should be stripped (non-video model)
	if len(userMsg.Attachments) != 0 {
		t.Errorf("expected 0 attachments (video stripped), got %d", len(userMsg.Attachments))
	}
}

func TestChatMessagesAudioKeptForAudioModel(t *testing.T) {
	dir := t.TempDir()
	audioPath := testAbsPath(dir, "recording.mp3")
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "listen", Attachments: []domain.Attachment{
			{Type: "audio", Name: "recording.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,abc", FilePath: audioPath},
		}},
	}}

	msgs := chatMessages(conv, "", ModelCapabilities{Audio: true})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	userMsg := msgs[0]
	if strings.Contains(userMsg.Content, "audio content omitted") {
		t.Errorf("audio-capable model should not get placeholder, got: %q", userMsg.Content)
	}
	if len(userMsg.Attachments) != 1 || userMsg.Attachments[0].Type != "audio" {
		t.Errorf("audio attachment should be kept for audio-capable model, got %d attachments", len(userMsg.Attachments))
	}
}
