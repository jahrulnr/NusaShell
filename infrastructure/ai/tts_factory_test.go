package ai

import (
	"testing"

	"nusashell/domain"
)

// TestSpeechSynthesizerFactoryKinds pins the provider-kind gate: the
// OpenAI-compatible /audio/speech endpoint exists on chat hosts (including
// OpenRouter) and on the OpenAI Responses platform; other kinds must fail
// fast so speech_generate falls back to offline piper.
func TestSpeechSynthesizerFactoryKinds(t *testing.T) {
	f := NewSpeechSynthesizerFactory()
	ok := []domain.ProviderKind{
		domain.ProviderChat,
		domain.ProviderResponses,
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
