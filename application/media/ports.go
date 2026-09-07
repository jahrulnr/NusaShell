package media

import (
	"context"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// Call is the turn-scoped context media tools need. Media never takes *TurnRun.
type Call struct {
	Ctx            context.Context
	ConversationID string
	// TurnID is the agent run/turn identifier. Backends that support turn
	// correlation (Codex images) send it as x-codex-image-turn-id.
	TurnID string
}

func (c Call) ctx() context.Context {
	if c.Ctx != nil {
		return c.Ctx
	}
	return context.Background()
}

// Caps is the subset of model capabilities read tools consult.
type Caps struct {
	Vision   bool
	Audio    bool
	Video    bool
	Document bool
}

// Describe runs a chat-model description/transcription of one attachment.
// App wires this with Factory + NewProviderContext + completeWithRetry so
// media never imports provider/ChatRequest.
type Describe func(ctx context.Context, providerID, model, prompt string, att domain.Attachment, maxTokens int) (string, error)

// Resolve looks up an enabled provider and its API key.
type Resolve func(id string) (p *domain.Provider, apiKey string, ok bool)

// ProviderName resolves a provider ID to a human-readable name.
type ProviderName func(id string) string

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// Bus publishes install-progress events.
type Bus interface {
	Emit(event string, payload any)
}

// GoFunc runs background work. App wires this to goSafe.
type GoFunc func(name string, fn func())

// Settings reads STT active-model (and any other media settings).
type Settings interface {
	Get() domain.Settings
}

// Attachments persists generated artifacts. The root AttachmentStore assigns.
type Attachments interface {
	WriteBytes(conversationID, name string, data []byte) (string, error)
}

// RetryDelay returns backoff and whether the error is retryable.
type RetryDelay func(err error, retry int) (time.Duration, bool)

// WaitRetry sleeps until delay elapses or ctx is cancelled.
type WaitRetry func(ctx context.Context, delay time.Duration) error

// ---- image generation ----

// ImageGenerator produces images from a text prompt and optional reference
// images. Implemented by OpenAI Images, OpenRouter Image API, and Codex
// ChatGPT plan image endpoints.
type ImageGenerator interface {
	Generate(ctx context.Context, req ImageGenRequest) (*ImageGenResult, error)
}

// ImageGenRequest is the provider-neutral generate/edit request.
type ImageGenRequest struct {
	Model      string
	Prompt     string
	Size       string // auto | 1024x1024 | 1536x1024 | 1024x1536
	Quality    string // auto | low | medium | high
	Background string // auto | transparent | opaque
	N          int
	References []ImageReference
	// TurnID is the agent turn identifier. The Codex image backend sends it
	// as the x-codex-image-turn-id turn-correlation header. Empty = header
	// omitted (OpenAI/OpenRouter do not use it).
	TurnID string
}

// ImageReference is a source image for image-to-image editing.
type ImageReference struct {
	MediaType string
	Data      []byte
}

// GeneratedImage is one decoded image from an image-generation response.
type GeneratedImage struct {
	Bytes     []byte
	MediaType string
}

// ImageGenResult is the decoded response from an image backend.
type ImageGenResult struct {
	Images      []GeneratedImage
	Provider    string // "openai" | "openrouter" | "codex"
	Model       string
	UsageTokens int
	CostUSD     float64
}

// ImageGeneratorFactory builds an ImageGenerator for a configured provider.
// ctx bounds token refresh and the HTTP call; media passes the turn context.
// Returns an error when the provider kind has no image-generation API
// (Anthropic Messages has none). OpenAI and OpenRouter hosts serve image
// generation directly; the Codex ChatGPT plan image endpoints are routed by
// the composition-root factory (infrastructure/ai/factory.go), which also
// resolves and refreshes the OAuth token.
type ImageGeneratorFactory func(ctx context.Context, p *domain.Provider, apiKey string) (ImageGenerator, error)

// ---- speech synthesis ----

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

// OfflineSynthesizer is the local piper engine. The application never
// imports piper/CGO — the adapter lives in infrastructure.
type OfflineSynthesizer interface {
	Available() bool
	UnavailableReason() string
	Synthesize(req TTSRequest) (*TTSResult, error)
}

// TTSInstaller is the port for the offline TTS engine installer.
type TTSInstaller interface {
	Status() contracts.TTSInstallStatusResult
	Install(ctx context.Context, voiceID string, report func(contracts.TTSInstallProgressDTO)) error
}

// ---- speech transcription ----

// SpeechTranscriber converts recorded audio into text via a provider's
// dedicated transcription endpoint (OpenAI-style POST /audio/transcriptions,
// multipart). Probe-verified 2026-08-23: catalog models of kind "stt"
// (gpt-4o-mini-transcribe, whisper-1) work ONLY through this endpoint — chat
// input_audio and the Responses API reject them.
type SpeechTranscriber interface {
	Transcribe(ctx context.Context, req STTRequest) (string, error)
}

// STTRequest is one transcription call: raw audio bytes plus metadata.
type STTRequest struct {
	Model    string
	Data     []byte
	Filename string // e.g. "clip.mp3"; extension drives server-side decoding
	Language string // optional ISO-639-1 hint; empty = auto-detect
	Prompt   string // optional spelling/style hint for the model
}

// SpeechTranscriberFactory builds a SpeechTranscriber for a provider.
// Optional; nil = STT routing unavailable and read_media falls back to the
// multimodal chat path with a clear error when an stt-kind model is picked.
type SpeechTranscriberFactory func(p *domain.Provider, apiKey string) (SpeechTranscriber, error)

// OfflineTranscriber converts recorded audio to text using a LOCAL engine
// (no network). It is the offline sibling of SpeechTranscriber.
type OfflineTranscriber interface {
	TranscribeOffline(ctx context.Context, req OfflineSTTRequest) (string, error)
}

// OfflineSTTRequest is one local transcription call.
type OfflineSTTRequest struct {
	Data       []byte // encoded audio bytes (wav/mp3/flac...); decoding is the engine adapter's job
	Language   string // optional ISO-639-1 hint ("id", "en"); empty = engine default/auto
	Model      string // optional: resolved ggml model file ("" = engine picks the first installed model)
	Translate  bool   // optional: transcribe-and-translate to English when the engine supports it
	MaxSeconds int    // optional duration cap; 0 = engine default; protects against runaway jobs
}

// OfflineTranscriberStatus reports whether a local STT engine is usable
// right now. Implementations answer WITHOUT loading heavy models where
// possible (model files present? native init succeeded at startup?).
type OfflineTranscriberStatus interface {
	OfflineSTTAvailable() bool
	OfflineSTTUnavailableReason() string
}

// OfflineTranscriberFactory builds the configured local engine once at
// startup. Returns an error when no engine is bundled/configured; callers
// treat that as "offline STT disabled", not as a fatal error.
type OfflineTranscriberFactory func() (OfflineTranscriber, error)

// STTInstaller is the port for the offline STT engine installer.
type STTInstaller interface {
	Status() contracts.STTInstallStatusResult
	Install(ctx context.Context, modelID string, report func(contracts.STTInstallProgressDTO)) error
}

// ---- video generation ----

// VideoGenerator produces short videos from a text prompt via the async
// submit/poll/download flow (OpenRouter-style POST /videos). Implementations
// block until the clip is downloaded or ctx is cancelled — generation takes
// tens of seconds to minutes, which is expected, not an error.
type VideoGenerator interface {
	Generate(ctx context.Context, req VideoGenRequest) (*VideoGenResult, error)
}

// VideoGenRequest is one generation call.
type VideoGenRequest struct {
	Model       string
	Prompt      string
	DurationSec int    // 0 = provider default; per-model minimums apply upstream
	Resolution  string // e.g. "480p"/"720p"; empty = provider default
	// References are optional source images for image-to-video generation.
	// The first reference becomes the first frame; subsequent references
	// are sent as additional input_references for style/identity guidance.
	// Models that only support text-to-video will reject the request
	// upstream — the picker badges i2i-capable models via Vision=true.
	References []ImageReference
}

// VideoGenResult is the downloaded clip plus metadata for persistence.
type VideoGenResult struct {
	Video     []byte
	MediaType string // "video/mp4"
	Ext       string // "mp4"
	Provider  string
	Model     string
	JobID     string
	CostUSD   float64 // reported by the provider when available
}

// VideoGeneratorFactory builds a VideoGenerator for a configured provider.
// Optional; nil = video generation unavailable.
type VideoGeneratorFactory func(p *domain.Provider, apiKey string) (VideoGenerator, error)

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	ImageGen     ImageGeneratorFactory
	SpeechSynth  SpeechSynthesizerFactory
	OfflineSynth OfflineSynthesizer
	SpeechSTT    SpeechTranscriberFactory
	OfflineSTT   OfflineTranscriberFactory
	VideoGen     VideoGeneratorFactory
	TTSInstaller TTSInstaller
	STTInstaller STTInstaller
	Attachments  Attachments
	Resolve      Resolve
	ProviderName ProviderName
	Describe     Describe
	Settings     Settings
	Log          Logger
	Bus          Bus
	Go           GoFunc
	RetryDelay   RetryDelay
	WaitRetry    WaitRetry

	// PrepareCodexAPIKey optionally remaps the credential used for an image
	// generation call (sticky Codex multi-account selection). Nil = use
	// apiKey as-is.
	PrepareCodexAPIKey func(conversationID string, provider *domain.Provider, apiKey string) (string, error)
	// FailoverCodexAPIKey optionally switches account after an image
	// generation failure (usage-limit 429, plain 429, or 403 entitlement).
	// retry=true means the caller rebuilds the generator with newAPIKey and
	// calls Generate once more. replacedErr, when non-nil, replaces the
	// error surfaced to the user (e.g. all accounts limited).
	FailoverCodexAPIKey func(ctx context.Context, conversationID string, provider *domain.Provider, apiKey string, genErr error) (newAPIKey string, retry bool, replacedErr error)
}
