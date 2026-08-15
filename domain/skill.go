package domain

import "time"

// SkillState controls the skill lifecycle: active skills are surfaced to
// the agent, stale skills are still searchable but de-prioritized, and
// archived skills are hidden from default listings.
type SkillState string

const (
	SkillStateActive   SkillState = "active"
	SkillStateStale    SkillState = "stale"
	SkillStateArchived SkillState = "archived"
)

// SkillOrigin records who created the skill so the curator can distinguish
// user-authored skills from agent-discovered ones.
type SkillOrigin string

const (
	SkillOriginUser    SkillOrigin = "user"
	SkillOriginAgent   SkillOrigin = "agent"
	SkillOriginBuiltin SkillOrigin = "builtin"
)

type Skill struct {
	ID          string
	Name        string
	Description string
	Content     string
	Category    string      // optional grouping (e.g. "git", "k8s")
	State       SkillState  // active | stale | archived (default active)
	Origin      SkillOrigin // user | agent | builtin
	Pinned      bool        // pinned skills bypass decay and always surface
	UsageCount  int         // incremented each time the skill is used in a turn
	LastUsedAt  time.Time   // zero = never used
	UpdatedAt   time.Time
}

type MemoryEntry struct {
	ID        string
	Content   string
	Tags      []string
	Source    string // "user" | "agent" | "system" (default "user")
	CreatedAt time.Time
}

// LearningEdgeType classifies the relationship between two learning nodes.
type LearningEdgeType string

const (
	EdgeRelated     LearningEdgeType = "related"      // generic semantic link
	EdgeUsedWith    LearningEdgeType = "used_with"    // co-occurred in a turn
	EdgeDerivedFrom LearningEdgeType = "derived_from" // target was source for source
)

// LearningEdge is a bitemporal edge between two learning nodes (skills or
// memory entries). ValidAt is when the relationship became true; InvalidAt
// is nil while the relationship is still current. Weight is strengthened
// on repeat observation via probability-union (CombineWeights).
type LearningEdge struct {
	ID        string
	SourceID  string           // skill or memory ID
	TargetID  string           // skill or memory ID
	Type      LearningEdgeType // related | used_with | derived_from
	Weight    float64          // [0, 1]
	ValidAt   time.Time        // when the relationship became true
	InvalidAt *time.Time       // nil = still valid
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
	// VisionFallback selects an auxiliary vision model used to describe
	// images when the active chat model does not support image input.
	// When VisionProviderID is empty, non-vision models receive a text
	// placeholder instead of an image description.
	VisionProviderID string `json:"vision_provider_id,omitempty"`
	VisionModelID    string `json:"vision_model_id,omitempty"`
	// WebAnswer configures the web_answer tool's answer provider. This is
	// separate from the chat providers — the user picks a searchwire-supported
	// vendor (brave, openrouter, openai, perplexity, anthropic, xai) and
	// supplies an API key manually. The API key is stored in the
	// CredentialStore under the "web_answer" key, not in settings JSON.
	// When WebAnswerProvider is empty, web_answer is not available.
	WebAnswerProvider string `json:"web_answer_provider,omitempty"`
	WebAnswerModel    string `json:"web_answer_model,omitempty"`
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
	// LearningReviewThreshold controls when the background learning
	// review fires. The review extracts observations (decisions,
	// preferences, errors, facts) from accumulated turns and writes
	// them to memory through the approval gate. Set to 0 to disable
	// turn-based review (compaction-triggered review still runs).
	// Default: 50 turns.
	LearningReviewThreshold int `json:"learning_review_threshold,omitempty"`
}

// DefaultSettings returns the factory defaults.
func DefaultSettings() Settings {
	return Settings{
		CompactionEnabled:       true,
		CompactionThreshold:     40000,
		PromptCaching:           true,
		MaxToolRounds:           8,
		MaxInputTokens:          200000,
		MaxOutputTokens:         65536,
		LearningReviewThreshold: 50,
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
