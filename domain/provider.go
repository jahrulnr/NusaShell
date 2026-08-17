package domain

import "time"

// ProviderKind names the wire API shape, not a vendor: Messages (Anthropic
// Messages API), Responses (OpenAI Responses API), Chat (any OpenAI-compatible
// Chat Completions endpoint), Ollama (local, OpenAI-compatible chat + native
// embeddings), Codex (ChatGPT Codex backend, OAuth).
type ProviderKind string

const (
	ProviderMessages  ProviderKind = "messages"
	ProviderResponses ProviderKind = "responses"
	ProviderChat      ProviderKind = "chat"
	ProviderOllama    ProviderKind = "ollama"
	ProviderCodex     ProviderKind = "codex"
)

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
	KnowledgeCutoff  string // knowledge cutoff date (e.g. "2025-05")
}

type Provider struct {
	ID        string
	Kind      ProviderKind
	Name      string
	BaseURL   string
	Enabled   bool
	HasAPIKey bool
	Models    []Model
	UpdatedAt time.Time
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
