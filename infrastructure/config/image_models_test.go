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
		// Gemini image families (Nano Banana) — must classify as image
		// without false positives on vision chat models.
		{"gemini-3-pro-image", true},
		{"gemini-3.1-flash-image", true},
		{"gemini-3.1-flash-lite-image", true},
		{"google/gemini-3-pro-image", true},
		{"nano-banana-pro-preview", true},
		{"google/nano-banana-pro-preview", true},
		// Negative: Gemini chat models must NOT match.
		{"gemini-2.5-flash", false},
		{"gemini-3.1-pro-preview", false},
		{"gemini-2.5-pro", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsKnownImageModel(tc.id); got != tc.want {
			t.Errorf("IsKnownImageModel(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestIsGeminiImageI2IModel(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		// Nano Banana family accepts reference images for editing (i2i).
		{"gemini-3-pro-image", true},
		{"gemini-3.1-flash-image", true},
		{"gemini-3.1-flash-lite-image", true},
		{"gemini-2.5-flash-image", true},
		{"google/gemini-3-pro-image", true},
		{"nano-banana-pro-preview", true},
		{"google/nano-banana-pro-preview", true},
		// imagen-* is text-to-image only — no reference image input.
		{"imagen-4", false},
		{"google/imagen-4", false},
		{"imagen-3.0-generate-002", false},
		// Gemini chat / vision models are NOT image i2i models.
		{"gemini-2.5-flash", false},
		{"gemini-2.5-pro", false},
		{"gemini-3.1-pro-preview", false},
		// Non-Gemini image models are not Gemini i2i.
		{"gpt-image-2", false},
		{"dall-e-3", false},
		{"flux-1.1-pro", false},
		// Preview-suffixed Gemini image ids are not classified as image by
		// the allowlist and must not be tagged i2i.
		{"gemini-3.0-pro-image-preview", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsGeminiImageI2IModel(tc.id); got != tc.want {
			t.Errorf("IsGeminiImageI2IModel(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}
