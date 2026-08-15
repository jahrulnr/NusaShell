package domain

import "time"

type Skill struct {
	ID          string
	Name        string
	Description string
	Content     string
	UpdatedAt   time.Time
}

type MemoryEntry struct {
	ID        string
	Content   string
	Tags      []string
	CreatedAt time.Time
}

type MCPServer struct {
	ID      string
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Enabled bool
}

type LogEntry struct {
	ID      string
	Time    time.Time
	Level   string // debug | info | warn | error
	Source  string
	Message string
}

type Settings struct {
	CompactionEnabled   bool
	CompactionThreshold int
	PromptCaching       bool
	MaxToolRounds       int
	MaxInputTokens      int // global context window cap (default 200k)
	MaxOutputTokens     int // default max completion tokens (default 64k)
	// EmbeddingModel selects the global embedding model for the learning
	// search layer. When EmbeddingProviderID is empty, the learning layer
	// auto-detects the first enabled provider with an embedding model.
	EmbeddingProviderID string `json:"embedding_provider_id,omitempty"`
	EmbeddingModelID    string `json:"embedding_model_id,omitempty"`
	// Sampling parameters. nil = use provider default (do not send the
	// field). Non-nil overrides the provider default for every turn.
	// Ranges: temperature 0–2 (OpenAI) / 0–1 (Anthropic), top_p 0–1,
	// top_k > 0 (Anthropic only), frequency_penalty -2..2 (OpenAI only),
	// presence_penalty -2..2 (OpenAI only).
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	TopK             *int     `json:"top_k,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
}

// DefaultSettings returns the factory defaults.
func DefaultSettings() Settings {
	return Settings{
		CompactionEnabled:   true,
		CompactionThreshold: 40000,
		PromptCaching:       true,
		MaxToolRounds:       8,
		MaxInputTokens:      200000,
		MaxOutputTokens:     65536,
	}
}

// NormalizeSettings fills values introduced after an existing local settings
// file was written. It preserves intentional false values for toggles.
func NormalizeSettings(settings Settings) Settings {
	if settings.MaxToolRounds < 1 {
		settings.MaxToolRounds = DefaultSettings().MaxToolRounds
	}
	if settings.MaxInputTokens < 1000 {
		settings.MaxInputTokens = DefaultSettings().MaxInputTokens
	}
	if settings.MaxOutputTokens < 256 {
		settings.MaxOutputTokens = DefaultSettings().MaxOutputTokens
	}
	return settings
}
