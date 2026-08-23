package ai

import (
	"fmt"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/stt"
)

// NewSpeechTranscriberFactory builds SpeechTranscribers for providers with a
// dedicated transcription endpoint. Only OpenAI-compatible chat hosts are
// supported: their POST <base>/audio/transcriptions is the probe-verified
// route for stt-kind models (whisper-1, gpt-4o-mini-transcribe). Other
// provider kinds have no known transcription endpoint and fail fast so the
// caller can fall back to the multimodal chat path.
func NewSpeechTranscriberFactory() application.SpeechTranscriberFactory {
	return func(p *domain.Provider, apiKey string) (application.SpeechTranscriber, error) {
		if p == nil {
			return nil, fmt.Errorf("stt: nil provider")
		}
		switch p.Kind {
		case domain.ProviderChat:
			base := strings.TrimRight(p.BaseURL, "/")
			if base == "" {
				return nil, fmt.Errorf("stt: provider %q has no base URL", p.ID)
			}
			return &stt.Client{BaseURL: base, APIKey: apiKey}, nil
		default:
			return nil, fmt.Errorf("stt: provider kind %q has no transcription endpoint", p.Kind)
		}
	}
}
