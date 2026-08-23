package application

import "context"
import "nusashell/domain"

// This file implements the speech-generation (text-to-speech) port.
// Online backends are OpenAI-compatible POST /audio/speech endpoints;
// the offline backend is the piper CLI (doc pattern mirrors the STT split:
// cloud-first, offline fills the gap, both configurable in Settings).

// SpeechSynthesizer converts text into spoken audio bytes.
type SpeechSynthesizer interface {
	Synthesize(ctx context.Context, req TTSRequest) (*TTSResult, error)
}

// TTSRequest is one synthesis call.
type TTSRequest struct {
	Text     string
	Model    string  // online: model id; offline: unused (voice carries the model)
	Voice    string  // provider-specific voice id ("alaine", "en_US-amy-medium", ...)
	Format   string  // "mp3" (default) | "wav" | "opus"
	Speed    float64 // 0.25–4.0; 0 = provider default
	Language string  // optional hint for offline engines (voice selection)
}

// TTSResult is synthesized audio plus metadata for persistence.
type TTSResult struct {
	Audio     []byte
	MediaType string // "audio/mpeg" | "audio/wav" ...
	Ext       string // "mp3" | "wav" ...
	Provider  string
	Model     string
	Voice     string
}

// SpeechSynthesizerFactory builds a SpeechSynthesizer for a configured
// online provider. Optional; nil = online TTS unavailable.
type SpeechSynthesizerFactory func(p *domain.Provider, apiKey string) (SpeechSynthesizer, error)
