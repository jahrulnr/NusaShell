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
		{"gemini-3.1-flash-tts-preview", true},
		{"tts-something-else", true},
		{"eleven-tts", true},
		{"speechify", false},
		{"gpt-audio", false},
		{"whisper-1", false},
		{"gpt-image-2", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsKnownTTSModel(c.id); got != c.want {
			t.Errorf("IsKnownTTSModel(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}
