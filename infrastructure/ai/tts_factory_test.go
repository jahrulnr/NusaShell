package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

// TestSpeechSynthesizerFactoryKinds pins the provider-kind gate: the
// OpenAI-compatible /audio/speech endpoint exists on chat hosts (including
// OpenRouter) and on the OpenAI Responses platform; Gemini is routed to its
// native generateContent AUDIO surface. Other kinds must fail fast so
// speech_generate falls back to offline piper.
func TestSpeechSynthesizerFactoryKinds(t *testing.T) {
	f := NewSpeechSynthesizerFactory()
	ok := []domain.ProviderKind{
		domain.ProviderChat,
		domain.ProviderResponses,
		domain.ProviderGemini,
	}
	for _, k := range ok {
		p := &domain.Provider{ID: "p1", Kind: k, BaseURL: "https://api.example.com/v1", Enabled: true}
		synth, err := f(p, "sk-test")
		if err != nil {
			t.Errorf("kind %q: expected client, got error %v", k, err)
			continue
		}
		if synth == nil {
			t.Errorf("kind %q: expected non-nil synthesizer", k)
		}
	}
	rejected := []domain.ProviderKind{
		domain.ProviderMessages,
	}
	for _, k := range rejected {
		p := &domain.Provider{ID: "p2", Kind: k, BaseURL: "https://api.example.com/v1", Enabled: true}
		if _, err := f(p, "sk-test"); err == nil {
			t.Errorf("kind %q: expected fail-fast error", k)
		}
	}
	if _, err := f(nil, "sk-test"); err == nil {
		t.Error("nil provider: expected error")
	}
	if _, err := f(&domain.Provider{ID: "p3", Kind: domain.ProviderChat}, "k"); err == nil {
		t.Error("empty base URL: expected error")
	}
}

// TestSpeechSynthesizerFactoryGeminiRouting verifies the Gemini kind returns
// a synthesizer that posts to the native :generateContent AUDIO surface, not
// the OpenAI-compatible /audio/speech endpoint.
func TestSpeechSynthesizerFactoryGeminiRouting(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{
						"inlineData": map[string]any{
							"mimeType": "audio/L16;rate=24000",
							"data":     "AAAA",
						},
					}},
				},
			}},
		})
	}))
	defer srv.Close()

	f := NewSpeechSynthesizerFactory()
	p := &domain.Provider{ID: "gem", Kind: domain.ProviderGemini, BaseURL: srv.URL, Enabled: true}
	synth, err := f(p, "gem-key")
	if err != nil {
		t.Fatalf("gemini factory: %v", err)
	}
	if synth == nil {
		t.Fatal("gemini factory: nil synthesizer")
	}
	_, sErr := synth.Synthesize(context.Background(), application.TTSRequest{
		Model: "gemini-3.1-flash-tts-preview", Text: "test",
	})
	if sErr != nil {
		t.Fatalf("Synthesize: %v", sErr)
	}
	if !strings.HasSuffix(gotPath, ":generateContent") {
		t.Errorf("path = %q, want :generateContent suffix (Gemini native surface)", gotPath)
	}
	if strings.Contains(gotPath, "/audio/speech") {
		t.Errorf("path = %q, must NOT be the OpenAI /audio/speech endpoint", gotPath)
	}
}

// TestSpeechModelListerFactoryGeminiNil verifies Gemini returns nil from the
// speech model lister factory — its TTS models are discovered through the
// shared GET /v1beta/models catalog + classification, mirroring image/video.
func TestSpeechModelListerFactoryGeminiNil(t *testing.T) {
	f := NewSpeechModelListerFactory()
	p := &domain.Provider{ID: "gem", Kind: domain.ProviderGemini, BaseURL: "https://generativelanguage.googleapis.com", Enabled: true}
	if lister := f(p); lister != nil {
		t.Errorf("gemini speech model lister = %T, want nil", lister)
	}
}
