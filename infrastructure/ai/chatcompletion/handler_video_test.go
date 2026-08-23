package chatcompletion

import (
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

// TestChatCompletionEncodesVideoAsVideoURL pins the wire format for video:
// OpenRouter routes video via the video_url content block (probe-verified
// 2026-08-23: gemini-2.5-flash described a real mp4 accurately through it;
// image_url with a data:video payload is rejected by strict providers).
func TestChatCompletionEncodesVideoAsVideoURL(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{{
			Role:    "user",
			Content: "Describe this clip",
			Attachments: []domain.Attachment{
				{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,AAAA"},
			},
		}},
	}

	body := marshalRequest(t, buildRequest(req, false))
	if !containsAll(body, "\"video_url\"", "data:video/mp4;base64,AAAA") {
		t.Fatalf("video must be encoded as a video_url block: %s", body)
	}
	if strings.Contains(body, "image_url") {
		t.Fatalf("video must not ride the image_url transport: %s", body)
	}
}
