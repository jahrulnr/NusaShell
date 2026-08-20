package domain

import (
	"strings"
	"time"
)

// MaxAutoContinuesCap is the absolute ceiling for a finite MaxAutoContinues
// value (matches MaxToolRounds). Values above this are clamped.
const MaxAutoContinuesCap = 10000

// DefaultMaxAutoContinues is the product default for the outer multi-turn
// auto-continue budget when the user has not configured one.
const DefaultMaxAutoContinues = 10

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
	OwnedBy     string      // "user", "builtin", "plugin:<plugin-id>" — secondary key for disambiguation; "" defaults to Origin
	PluginDir   string      // mount source directory for plugin-owned skills (read-only); empty for user/builtin
	Pinned      bool        // pinned skills bypass decay and always surface
	UsageCount  int         // incremented each time the skill is used in a turn
	LastUsedAt  time.Time   // zero = never used
	UpdatedAt   time.Time
}

// EffectiveOwnedBy returns OwnedBy if set, otherwise a stringified Origin.
// This is the value used for priority resolution and UI badges.
func (s *Skill) EffectiveOwnedBy() string {
	if s.OwnedBy != "" {
		return s.OwnedBy
	}
	return string(s.Origin)
}

// SkillOwnerPriority returns the resolution priority for an owner.
// Lower = higher priority. User wins, then builtin, then plugin (alpha).
func SkillOwnerPriority(ownedBy string) int {
	switch {
	case ownedBy == "user" || ownedBy == string(SkillOriginUser):
		return 0
	case ownedBy == "builtin" || ownedBy == string(SkillOriginBuiltin):
		return 1
	case strings.HasPrefix(ownedBy, "plugin:"):
		return 2
	default:
		return 3
	}
}

// SkillFile is a read of one file inside a skill package.
type SkillFile struct {
	SkillID    string
	Path       string // normalized relative path (posix)
	Content    string // text content when editable text
	SizeBytes  int64
	Editable   bool
	Truncated  bool // content was clipped by maxChars
	NextOffset int  // set when truncated (continue reading from here)
}

// SkillFileEntry describes one entry in a skill directory tree.
type SkillFileEntry struct {
	Path      string // posix relative path
	Type      string // "file" | "directory"
	SizeBytes int64
	Editable  bool
}

type MemoryEntry struct {
	ID        string
	Target    string // "memory" (project notes) or "user" (profile facts); default "memory"
	Content   string
	Tags      []string
	Source    string // "user" | "agent" | "system" (default "user")
	CreatedAt time.Time
}

// Memory target constants and per-target character limits.
const (
	MemoryTargetMemory = "memory" // project/task notes
	MemoryTargetUser   = "user"   // user-profile facts (preferences, habits)
	MemoryLimitMemory  = 2200     // chars across all "memory" entries
	MemoryLimitUser    = 1375     // chars across all "user" entries
)

// MemoryLimit returns the total character budget for a target.
func MemoryLimit(target string) int {
	if target == MemoryTargetUser {
		return MemoryLimitUser
	}
	return MemoryLimitMemory
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
	// CompactionModel selects the model used for context compaction
	// summarization. When empty, the conversation's active model is used.
	// Format: "providerID:modelID" (same as VisionModelID etc). Useful for
	// routing compaction to a cheaper/faster model while keeping the chat
	// model for the actual conversation.
	CompactionModel string `json:"compaction_model,omitempty"`
	PromptCaching   bool
	MaxToolRounds   int
	MaxInputTokens  int // global context window cap (default 200k)
	MaxOutputTokens int // default max completion tokens (default 64k)
	// MaxParallelTools bounds how many tool calls from a single assistant
	// round run concurrently. The model often emits several independent
	// tool calls at once; running them in parallel cuts wall-clock latency
	// without adding provider round-trips. Default 6, range 1–64.
	MaxParallelTools int `json:"max_parallel_tools,omitempty"`
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
	// AudioFallback selects an auxiliary audio-capable model used to
	// transcribe/describe audio when the active chat model does not
	// support audio input. When empty, non-audio models receive an
	// error message directing the user to configure a fallback or switch
	// to an audio-capable model.
	AudioProviderID string `json:"audio_provider_id,omitempty"`
	AudioModelID    string `json:"audio_model_id,omitempty"`
	// VideoFallback selects an auxiliary video-capable model used to
	// describe video when the active chat model does not support video
	// input. When empty, non-video models receive an error message
	// directing the user to configure a fallback or switch to a
	// video-capable model.
	VideoProviderID string `json:"video_provider_id,omitempty"`
	VideoModelID    string `json:"video_model_id,omitempty"`
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
	// Default: 10 turns.
	LearningReviewThreshold int `json:"learning_review_threshold,omitempty"`
	// ReviewModel selects the model used by the background review agent.
	// When empty, the conversation's active model is used. Format:
	// "providerID:modelID" (same as CompactionModel). Useful for routing
	// reviews to a cheaper/faster model; reviews re-send the transcript
	// tail, so the model choice directly affects background cost.
	ReviewModel string `json:"review_model,omitempty"`
	// MaxAutoContinues is the outer multi-turn auto-continue budget.
	// After a successful sealed turn, if the conversation todo list
	// still has open items (pending or in_progress), the agent runner
	// starts the next turn without a user message, injecting the
	// continue.md steering prompt. The chain stops when:
	//   - no open todos remain
	//   - the chain budget is exhausted
	//   - the last assistant turn ended with a question
	//   - the user stops the turn, sends a message, or switches
	//     conversations
	// 0 = unlimited (escape hatch for long unattended runs).
	// Negative or unset = product default (10).
	// Default: 10.
	MaxAutoContinues int `json:"max_auto_continues,omitempty"`
	// SoundNotifications controls whether the UI plays a sound when an
	// agent turn completes or fails. Default true. The frontend reads
	// this from settings.get and gates audio playback on it.
	SoundNotifications bool `json:"sound_notifications,omitempty"`
	// RepeatedToolLimit is the max number of consecutive identical
	// single-tool calls (same name + args, no text) before the agent
	// strips tools for one round to break the loop. 0 = disabled.
	// Default: 3.
	RepeatedToolLimit int `json:"repeated_tool_limit,omitempty"`
	// UserPrompt is custom instructions the user wants injected into every
	// agent turn's system prompt. Placed after the cache-stable prefix
	// (system.md + tools.md) but before per-conversation system messages,
	// so changing it breaks the prompt cache for all subsequent turns until
	// a new cache shard stabilizes. Empty = no injection.
	UserPrompt string `json:"user_prompt,omitempty"`
}

// DefaultSettings returns the factory defaults.
func DefaultSettings() Settings {
	return Settings{
		CompactionEnabled:       true,
		CompactionThreshold:     0, // 0 = auto (80% of model context window)
		PromptCaching:           true,
		MaxToolRounds:           8,
		MaxInputTokens:          200000,
		MaxOutputTokens:         65536,
		MaxParallelTools:        6,
		LearningReviewThreshold: 10,
		MaxAutoContinues:        DefaultMaxAutoContinues,
		SoundNotifications:      true,
		RepeatedToolLimit:       3,
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
	// MaxParallelTools: 0/negative = default (6); clamp to 1–64.
	if settings.MaxParallelTools < 1 {
		settings.MaxParallelTools = DefaultSettings().MaxParallelTools
	} else if settings.MaxParallelTools > 64 {
		settings.MaxParallelTools = 64
	}
	// Migrate the old flat CompactionThreshold default (40000) to 0 (auto).
	// The old default was a flat token count that didn't scale with the
	// model's context window — a 1M-context model would compact at 40k,
	// wasting 96% of its context. 0 means "auto = 80% of model context".
	if settings.CompactionThreshold == 40000 {
		settings.CompactionThreshold = 0
	}
	// MaxAutoContinues: 0 is a valid sentinel (unlimited). Only negative
	// or unset (-1 from JSON omitempty on older files) needs the default.
	if settings.MaxAutoContinues < 0 {
		settings.MaxAutoContinues = DefaultSettings().MaxAutoContinues
	}
	// RepeatedToolLimit: 0 is a valid sentinel (disabled). Negative or
	// unset (-1 from JSON omitempty on older files) needs the default.
	if settings.RepeatedToolLimit < 0 {
		settings.RepeatedToolLimit = DefaultSettings().RepeatedToolLimit
	}
	return settings
}
