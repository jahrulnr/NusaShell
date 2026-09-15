package config

import "testing"

func TestIsKnownVideoModel(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		// Veo family — must classify as video.
		{"veo-3.1-generate-preview", true},
		{"veo-3.1-fast-generate-preview", true},
		{"veo-3.1-lite-generate-preview", true},
		{"veo-3.0-generate-001", true},
		{"veo-3.0-fast-generate-001", true},
		{"google/veo-3.1-generate-preview", true},
		{"google/veo-3.0-generate-001", true},
		// Negatives: chat / image / TTS models must NOT match.
		{"gemini-2.5-flash", false},
		{"gemini-3.1-pro-preview", false},
		{"gemini-3.1-flash-tts-preview", false},
		{"gemini-3-pro-image", false},
		{"gpt-4o", false},
		{"gpt-image-1", false},
		{"tts-1", false},
		// gemini-omni-* is intentionally NOT classified as video: the Veo
		// :predictLongRunning surface is documented for veo-* only, and
		// the omni family is not confirmed to be served by that endpoint.
		{"gemini-omni-1", false},
		{"google/gemini-omni-1", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsKnownVideoModel(tc.id); got != tc.want {
			t.Errorf("IsKnownVideoModel(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}
