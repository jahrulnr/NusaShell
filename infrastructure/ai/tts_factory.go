package ai

import (
	"fmt"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	ttsclient "nusashell/infrastructure/ai/tts"
)

// NewSpeechSynthesizerFactory builds online TTS clients for providers whose
// host serves the OpenAI-compatible POST /audio/speech endpoint: Chat
// (any OpenAI-compatible gateway, including OpenRouter's /api/v1/audio/speech)
// and Responses (same OpenAI platform host). Other kinds fail fast so the
// caller can fall back to offline piper.
func NewSpeechSynthesizerFactory() application.SpeechSynthesizerFactory {
	return func(p *domain.Provider, apiKey string) (application.SpeechSynthesizer, error) {
		if p == nil {
			return nil, fmt.Errorf("tts: nil provider")
		}
		switch p.Kind {
		case domain.ProviderChat, domain.ProviderResponses:
			base := strings.TrimRight(p.BaseURL, "/")
			if base == "" {
				return nil, fmt.Errorf("tts: provider %q has no base URL", p.ID)
			}
			return &ttsclient.Client{BaseURL: base, APIKey: apiKey}, nil
		default:
			return nil, fmt.Errorf("tts: provider kind %q has no /audio/speech endpoint", p.Kind)
		}
	}
}
