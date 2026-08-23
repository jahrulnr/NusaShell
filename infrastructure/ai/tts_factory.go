package ai

import (
	"fmt"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	ttsclient "nusashell/infrastructure/ai/tts"
)

// NewSpeechSynthesizerFactory builds online TTS clients for providers with
// an OpenAI-compatible /audio/speech endpoint (chat-kind providers). Other
// kinds fail fast so the caller can fall back to offline piper.
func NewSpeechSynthesizerFactory() application.SpeechSynthesizerFactory {
	return func(p *domain.Provider, apiKey string) (application.SpeechSynthesizer, error) {
		if p == nil {
			return nil, fmt.Errorf("tts: nil provider")
		}
		if p.Kind != domain.ProviderChat {
			return nil, fmt.Errorf("tts: provider kind %q has no /audio/speech endpoint", p.Kind)
		}
		base := strings.TrimRight(p.BaseURL, "/")
		if base == "" {
			return nil, fmt.Errorf("tts: provider %q has no base URL", p.ID)
		}
		return &ttsclient.Client{BaseURL: base, APIKey: apiKey}, nil
	}
}
