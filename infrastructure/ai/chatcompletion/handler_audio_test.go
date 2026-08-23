package chatcompletion

import (
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
	aiutil "nusashell/infrastructure/ai/internal"
)

// TestChatCompletionEncodesAudioAsInputAudio is a regression test for the
// Nvidia NIM failure "Failed to load image from data:audio/mpeg;...": audio
// attachments must be encoded as Chat Completions input_audio blocks
// (base64 + format), never as image_url data URLs. This applies to both
// user-message attachments and tool results returned by read_audio.
func TestChatCompletionEncodesAudioAsInputAudio(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{
			{
				Role:    "user",
				Content: "What is being said?",
				Attachments: []domain.Attachment{
					{Type: "audio", Name: "clip.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,QUJD"},
				},
			},
			{
				Role:      "assistant",
				ToolCalls: []domain.ToolCall{{ID: "tc1", Name: "read_audio", Args: `{}`}},
			},
			{
				Role: "tool",
				ToolResult: &application.ToolResult{
					ToolCallID: "tc1", Name: "read_audio", Content: "Audio loaded.",
					Attachments: []domain.Attachment{
						{Type: "audio", Name: "clip.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,QUJD"},
					},
				},
			},
		},
	}

	body := marshalRequest(t, buildRequest(req, false))
	if !containsAll(body, "\"input_audio\"", "\"format\":\"mp3\"", "\"data\":\"QUJD\"") {
		t.Fatalf("audio must be encoded as input_audio blocks, got: %s", body)
	}
	if strings.Contains(body, "data:audio/mpeg") {
		t.Fatalf("audio data URL leaked into the image_url transport: %s", body)
	}
}

// Video must use the video_url content type (OpenRouter's dedicated video
// transport), NOT image_url. Sending video as image_url causes providers to
// reject it with HTTP 400 because they attempt image decoding on a video
// payload.
func TestChatCompletionVideoUsesVideoURLTransport(t *testing.T) {
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
	if !containsAll(body, "video_url", "data:video/mp4;base64,AAAA") {
		t.Fatalf("video must use the video_url transport: %s", body)
	}
	if strings.Contains(body, "image_url") {
		t.Fatalf("video must NOT use image_url transport: %s", body)
	}
}

func TestInputAudioFormatMapping(t *testing.T) {
	cases := map[string]string{
		"audio/mpeg":  "mp3",
		"audio/mp3":   "mp3",
		"":            "mp3",
		"video/mp4":   "mp3",
		"audio/wav":   "wav",
		"audio/x-wav": "wav",
		"audio/wave":  "wav",
		"audio/ogg":   "ogg",
		"audio/webm":  "webm",
		"audio/flac":  "flac",
		"AUDIO/WAV":   "wav",
	}
	for mt, want := range cases {
		if got := aiutil.InputAudioFormat(mt); got != want {
			t.Errorf("InputAudioFormat(%q) = %q, want %q", mt, got, want)
		}
	}
}
