package media

import (
	"testing"

	"nusashell/domain"
)

func TestAudioFallbackRoute(t *testing.T) {
	sttProvider := &domain.Provider{Models: []domain.Model{{ID: "whisper-x", Kind: domain.ModelKindSTT}}}
	if got := audioFallbackRoute(sttProvider, "whisper-x"); got != audioRouteTranscriptions {
		t.Errorf("stt kind should route to transcriptions, got %q", got)
	}

	chatProvider := &domain.Provider{Models: []domain.Model{{ID: "gem", Kind: domain.ModelKindChat}}}
	if got := audioFallbackRoute(chatProvider, "gem"); got != audioRouteChat {
		t.Errorf("chat kind should route to chat, got %q", got)
	}

	if got := audioFallbackRoute(&domain.Provider{}, "unknown-model"); got != audioRouteChat {
		t.Errorf("unknown model should default to chat route, got %q", got)
	}
}
