package domain

import "time"

// ProviderKind names the wire API shape, not a vendor: Messages (Anthropic
// Messages API), Responses (OpenAI Responses API), Chat (any OpenAI-compatible
// Chat Completions endpoint, including OpenRouter hosts).
type ProviderKind string

const (
	ProviderMessages  ProviderKind = "messages"
	ProviderResponses ProviderKind = "responses"
	ProviderChat      ProviderKind = "chat"
)

// ProviderDriver selects the implementation package behind a provider.
// ProviderKind remains the wire API shape so a driver can expose more than
// one API kind.
type ProviderDriver string

const (
	ProviderDriverAuto       ProviderDriver = ""
	ProviderDriverAnthropic  ProviderDriver = "anthropic"
	ProviderDriverOpenAI     ProviderDriver = "openai"
	ProviderDriverOpenRouter ProviderDriver = "openrouter"
)

// ValidDriver reports whether driver is supported. The empty driver keeps the
// legacy host-detected routing for providers created before explicit drivers.
func ValidDriver(driver ProviderDriver) bool {
	switch driver {
	case ProviderDriverAuto, ProviderDriverAnthropic, ProviderDriverOpenAI, ProviderDriverOpenRouter:
		return true
	}
	return false
}

// KindCapabilities describes what a provider kind can do at the protocol
// level — independent of any specific model. This drives factory routing
// (image generation, TTS, STT, embeddings, video) and the requiresKey check
// without scattering switch statements across the codebase.
type KindCapabilities struct {
	// RequiresKey is true when the provider kind needs a user-supplied API
	// key. Local endpoints (LM Studio via chat kind) work without one.
	RequiresKey bool
	// HasModelListing is true when the provider kind exposes a GET /models
	// (or /v1/models) endpoint for chat model discovery.
	HasModelListing bool
	// HasEmbeddings is true when the provider kind may expose an
	// OpenAI-compatible /embeddings endpoint.
	HasEmbeddings bool
	// HasImageEndpoint is true when the provider kind may serve
	// /images/generations (OpenAI) or /images (OpenRouter).
	HasImageEndpoint bool
	// HasSpeechEndpoint is true when the provider kind may serve
	// POST /audio/speech (TTS).
	HasSpeechEndpoint bool
	// HasTranscriptionEndpoint is true when the provider kind may serve
	// POST /audio/transcriptions (STT).
	HasTranscriptionEndpoint bool
	// HasVideoEndpoint is true when the provider kind may serve
	// the async /videos API (OpenRouter).
	HasVideoEndpoint bool
	// PromptCacheStyle describes how prompt caching is expressed on the
	// wire: "anthropic" (cache_control blocks), "openai" (cached_tokens in
	// usage details), or "" (none).
	PromptCacheStyle string
	// CacheTTLs is the prompt-cache TTL enum captured from the provider's
	// OpenAPI spec (or hardcoded SDK fallback). Empty when the provider does
	// not support prompt caching. Examples: ["5m","1h"] (Anthropic) or
	// ["30m"] (OpenAI GPT-5.6+).
	CacheTTLs []string
}

var kindCaps = map[ProviderKind]KindCapabilities{
	ProviderMessages: {
		RequiresKey:              true,
		HasModelListing:          true,
		HasEmbeddings:            true,
		HasImageEndpoint:         false,
		HasSpeechEndpoint:        false,
		HasTranscriptionEndpoint: false,
		HasVideoEndpoint:         false,
		PromptCacheStyle:         "anthropic",
		CacheTTLs:                []string{"5m", "1h"},
	},
	ProviderResponses: {
		RequiresKey:              true,
		HasModelListing:          true,
		HasEmbeddings:            true,
		HasImageEndpoint:         true,
		HasSpeechEndpoint:        true,
		HasTranscriptionEndpoint: false,
		HasVideoEndpoint:         true,
		PromptCacheStyle:         "openai",
		CacheTTLs:                []string{"30m"},
	},
	ProviderChat: {
		RequiresKey:              false, // LM Studio and local endpoints work without a key
		HasModelListing:          true,
		HasEmbeddings:            true,
		HasImageEndpoint:         true,
		HasSpeechEndpoint:        true,
		HasTranscriptionEndpoint: true,
		HasVideoEndpoint:         true,
		PromptCacheStyle:         "openai",
		CacheTTLs:                []string{"5m", "1h", "30m"},
	},
}

// KindCapabilities returns the protocol-level capabilities for this provider
// kind. Never returns a zero-value struct — unknown kinds get the
// ProviderChat defaults (most permissive) so local/custom hosts keep working.
func (p *Provider) KindCapabilities() KindCapabilities {
	if caps, ok := kindCaps[p.Kind]; ok {
		return caps
	}
	return kindCaps[ProviderChat]
}

// ValidKind reports whether kind is one of the known provider kinds.
func ValidKind(kind ProviderKind) bool {
	switch kind {
	case ProviderMessages, ProviderResponses, ProviderChat:
		return true
	}
	return false
}

// KindCaps is a package-level helper for callers that have a
// ProviderKind but not a *Provider (e.g. requiresKey checks).
func KindCaps(kind ProviderKind) KindCapabilities {
	if caps, ok := kindCaps[kind]; ok {
		return caps
	}
	return kindCaps[ProviderChat]
}

// ModelKind categorizes what a model produces, used to filter the model
// picker so users don't accidentally select an image generator or TTS
// model for chat. Detected from the models.dev catalog (modality + name
// + description) during enrichment; defaults to ModelKindChat when
// unknown (preserves backward compatibility for providers not in catalog).
type ModelKind string

const (
	ModelKindChat      ModelKind = "chat"      // text/text+image input → text output (LLM)
	ModelKindEmbedding ModelKind = "embedding" // produces embedding vectors
	ModelKindImage     ModelKind = "image"     // text/image input → image output
	ModelKindVideo     ModelKind = "video"     // text/image input → video output
	ModelKindTTS       ModelKind = "tts"       // text input → audio output (speech synthesis)
	ModelKindSTT       ModelKind = "stt"       // audio input → text output (speech transcription)
)

type Model struct {
	ID               string
	DisplayName      string // human-readable name (e.g. "GPT-5.5"), from catalog
	Context          int
	MaxOutput        int     // max completion tokens, when known
	InputCost        float64 // USD per 1M input tokens, when known
	OutputCost       float64 // USD per 1M output tokens, when known
	CacheReadCost    float64 // USD per 1M cached input tokens, when known
	Description      string
	SupportedEfforts []string  // reasoning effort levels the provider advertises (e.g. "low","medium","high","xhigh"); empty when unsupported
	DefaultEffort    string    // provider-advertised default effort, "" when none
	Kind             ModelKind // what this model produces; "" = unknown (treat as chat)
	// Capability flags (enriched from models.dev catalog when available).
	ToolCall         bool   // supports function/tool calling
	StructuredOutput bool   // supports structured/JSON output
	Reasoning        bool   // supports reasoning/thinking mode
	Vision           bool   // supports image input (multimodal)
	Audio            bool   // supports audio input (multimodal)
	Video            bool   // supports video input (multimodal)
	Document         bool   // supports PDF/document input (multimodal)
	KnowledgeCutoff  string // knowledge cutoff date (e.g. "2025-05")
	// InterleavedField names the wire field that carries interleaved
	// reasoning on assistant messages — "reasoning_content" for DeepSeek
	// V4, GLM, Kimi thinking, etc. When set, the chat adapter must echo
	// reasoning_content back on every assistant message in subsequent
	// turns or the upstream 400s with "reasoning_content must be passed
	// back". Empty when the model does not use interleaved reasoning.
	InterleavedField string
}

type Provider struct {
	ID        string
	Driver    ProviderDriver
	Kind      ProviderKind
	Name      string
	BaseURL   string
	Enabled   bool
	HasAPIKey bool
	Models    []Model
	UpdatedAt time.Time
}

// EffectiveDriver returns the explicit driver when present. The stable
// built-in IDs also identify their driver for records created by older
// versions that predate the Driver field.
func (p *Provider) EffectiveDriver() ProviderDriver {
	if p == nil {
		return ProviderDriverAuto
	}
	if p.Driver != ProviderDriverAuto {
		return p.Driver
	}
	switch p.ID {
	case "anthropic":
		return ProviderDriverAnthropic
	case "openai":
		return ProviderDriverOpenAI
	case "openrouter":
		return ProviderDriverOpenRouter
	default:
		return ProviderDriverAuto
	}
}

func (p *Provider) HasModel(id string) bool {
	for _, m := range p.Models {
		if m.ID == id {
			return true
		}
	}
	return false
}

// FindModel returns the model metadata for the given ID, or nil if not
// found. Used by the agent runtime to check capabilities (e.g. Vision)
// before sending attachments that the model may not support.
func (p *Provider) FindModel(id string) *Model {
	for i := range p.Models {
		if p.Models[i].ID == id {
			return &p.Models[i]
		}
	}
	return nil
}
