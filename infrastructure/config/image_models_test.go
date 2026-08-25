package config

import "testing"

func TestIsKnownImageModel(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"gpt-image-1", true},
		{"openai/gpt-image-1", true},
		{"dall-e-3", true},
		{"black-forest-labs/flux-1.1-pro", true},
		{"google/gemini-2.5-flash-image", true},
		{"google/imagen-4", true},
		{"krea/krea-2-medium-turbo", true},
		{"krea/krea-2-large", true},
		{"krea-2-medium", true},
		{"gpt-4o", false},
		{"llama-3.2-11b-vision", false},
		{"gemini-3.0-pro-image-preview", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsKnownImageModel(tc.id); got != tc.want {
			t.Errorf("IsKnownImageModel(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}
