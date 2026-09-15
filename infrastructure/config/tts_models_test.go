package config

import "testing"

func TestIsKnownTTSModel(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"tts-1", true},
		{"tts-1-hd", true},
		{"openai/tts-1", true},
		{"gpt-4o-mini-tts", true},
		{"openai/gpt-4o-mini-tts", true},
		// Gemini TTS ids classify via the -tts suffix / -tts- segment.
		{"gemini-3.1-flash-tts-preview", true},
		{"gemini-2.5-flash-preview-tts", true},
		{"gemini-2.5-pro-preview-tts", true},
		{"google/gemini-3.1-flash-tts-preview", true},
		{"tts-something-else", true},
		{"eleven-tts", true},
		// Negatives: chat, image, video, and stt ids must not classify as TTS.
		{"speechify", false},
		{"gpt-audio", false},
		{"whisper-1", false},
		{"gpt-image-2", false},
		{"gemini-3-pro-image", false},
		{"veo-3.1-generate-preview", false},
		{"gemini-2.5-flash", false},
		{"gpt-4o", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsKnownTTSModel(c.id); got != c.want {
			t.Errorf("IsKnownTTSModel(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}
