package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

func TestModelCapabilitiesUnknownModelDefaultsVisionOnly(t *testing.T) {
	provider := &domain.Provider{ID: "p1", Models: nil}
	caps := modelCapabilities(provider, "unknown-model")
	if !caps.Vision {
		t.Errorf("unknown model should default Vision=true, got %+v", caps)
	}
	if caps.Audio {
		t.Errorf("unknown model should default Audio=false (rare capability, causes provider errors), got %+v", caps)
	}
	if caps.Video {
		t.Errorf("unknown model should default Video=false (rare capability, causes provider errors), got %+v", caps)
	}
}

func TestModelCapabilitiesNilProviderDefaultsVisionOnly(t *testing.T) {
	caps := modelCapabilities(nil, "any")
	if !caps.Vision {
		t.Errorf("nil provider should default Vision=true, got %+v", caps)
	}
	if caps.Audio {
		t.Errorf("nil provider should default Audio=false, got %+v", caps)
	}
	if caps.Video {
		t.Errorf("nil provider should default Video=false, got %+v", caps)
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

// TestFilterToolAttachmentsByCapsStripsAudio: a non-audio model should not
// receive audio attachments from tool results (e.g. read_audio). The audio
// should be stripped and replaced with a text note.
func TestFilterToolAttachmentsByCapsStripsAudio(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "audio", Name: "recording.mp3", FilePath: "/tmp/rec.mp3"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Audio loaded.", ModelCapabilities{})
	if len(filtered) != 0 {
		t.Errorf("audio should be stripped for non-audio model, got %d attachments", len(filtered))
	}
	if !strings.Contains(content, "cannot be played") {
		t.Errorf("content should note audio can't be played, got: %q", content)
	}
	if !strings.Contains(content, "/tmp/rec.mp3") {
		t.Errorf("content should include file path, got: %q", content)
	}
}

// TestFilterToolAttachmentsByCapsKeepsAudioForAudioModel: an audio-capable
// model should receive audio attachments from tool results unchanged.
func TestFilterToolAttachmentsByCapsKeepsAudioForAudioModel(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "audio", Name: "recording.mp3", FilePath: "/tmp/rec.mp3"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Audio loaded.", ModelCapabilities{Audio: true})
	if len(filtered) != 1 || filtered[0].Type != "audio" {
		t.Errorf("audio should be kept for audio-capable model, got %d attachments", len(filtered))
	}
	if strings.Contains(content, "cannot be played") {
		t.Errorf("content should not note audio can't be played, got: %q", content)
	}
}

// TestFilterToolAttachmentsByCapsStripsVideo: a non-video model should not
// receive video attachments from tool results.
func TestFilterToolAttachmentsByCapsStripsVideo(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "video", Name: "clip.mp4", FilePath: "/tmp/clip.mp4"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Video loaded.", ModelCapabilities{})
	if len(filtered) != 0 {
		t.Errorf("video should be stripped for non-video model, got %d attachments", len(filtered))
	}
	if !strings.Contains(content, "cannot be shown") {
		t.Errorf("content should note video can't be shown, got: %q", content)
	}
}

// TestFilterToolAttachmentsByCapsKeepsImageForVisionModel: a vision-capable
// model should receive image attachments from tool results unchanged.
func TestFilterToolAttachmentsByCapsKeepsImageForVisionModel(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "image", Name: "photo.png", FilePath: "/tmp/photo.png"},
	}
	filtered, content := filterToolAttachmentsByCaps(atts, "Image loaded.", ModelCapabilities{Vision: true})
	if len(filtered) != 1 || filtered[0].Type != "image" {
		t.Errorf("image should be kept for vision-capable model, got %d attachments", len(filtered))
	}
	if strings.Contains(content, "cannot be shown") {
		t.Errorf("content should not note image can't be shown, got: %q", content)
	}
}
