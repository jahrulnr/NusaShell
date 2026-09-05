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

// DefaultMaxParallelTools is the fallback concurrency bound for tool calls
// from a single assistant round when settings.MaxParallelTools is not set.
// The actual bound is settings.MaxParallelTools (range 1–64, configurable
// in Settings); this constant is the factory default.
const DefaultMaxParallelTools = 6

// SkillStatus is the promotion state machine. Generated skills start as
// candidate/experimental; Routable() skills (trusted and validated) appear
// in default hydration and search.
type SkillStatus string

const (
	SkillStatusCandidate    SkillStatus = "candidate"
	SkillStatusExperimental SkillStatus = "experimental"
	SkillStatusValidated    SkillStatus = "validated"
	SkillStatusTrusted      SkillStatus = "trusted"
	SkillStatusDeprecated   SkillStatus = "deprecated"
	SkillStatusRetired      SkillStatus = "retired"
)

// SkillOrigin records who provided the skill.
type SkillOrigin string

const (
	SkillOriginUser    SkillOrigin = "user"
	SkillOriginLearned SkillOrigin = "learned"
	SkillOriginBuiltin SkillOrigin = "builtin"
	SkillOriginPlugin  SkillOrigin = "plugin"
)

type Skill struct {
	ID            string
	Name          string
	Description   string
	Content       string
	Category      string      // optional grouping (e.g. "git", "k8s")
	Status        SkillStatus // candidate → … → trusted → deprecated → retired
	Version       int         // immutable snapshot number currently checked out
	ActiveVersion int         // pointer used for rollback; equals Version when live
	Origin        SkillOrigin // user | learned | builtin | plugin
	OwnedBy       string      // "user", "builtin", "plugin:<plugin-id>" — secondary key for disambiguation; "" defaults to Origin
	PluginDir     string      // mount source directory for plugin-owned skills (read-only); empty for user/builtin
	Path          string      // absolute path to the skill directory on disk; empty for embedded/in-memory skills
	Bundled       bool        // true when the skill directory has support files beyond SKILL.md (references/, templates/, scripts/, examples/)
	UsageCount    int         // incremented each time the skill is used in a turn
	LastUsedAt    time.Time   // zero = never used
	UpdatedAt     time.Time
}

// EffectiveOwnedBy returns OwnedBy if set, otherwise a stringified Origin.
// This is the value used for priority resolution and UI badges.
func (s *Skill) EffectiveOwnedBy() string {
	if s.OwnedBy != "" {
		return s.OwnedBy
	}
	return string(s.Origin)
}

// Touch stamps UpdatedAt with the given time.
func (s *Skill) Touch(now time.Time) {
	if s == nil {
		return
	}
	s.UpdatedAt = now
}

// SetOwner records the owning key (e.g. "user", "builtin",
// "plugin:<plugin-id>") and the mount source directory for plugin-owned
// skills. PluginDir is empty for user/builtin skills.
func (s *Skill) SetOwner(ownedBy, pluginDir string) {
	if s == nil {
		return
	}
	s.OwnedBy = ownedBy
	s.PluginDir = pluginDir
}

// EnsureStatusDefault fills Status and Version when empty. Learned skills
// default to experimental; curated (user/builtin/plugin) default to trusted.
func (s *Skill) EnsureStatusDefault() {
	if s == nil {
		return
	}
	if s.Origin == SkillOriginLearned && s.OwnedBy == "" {
		s.OwnedBy = string(SkillOriginLearned)
	}
	if s.Status == "" {
		if s.Origin == SkillOriginLearned {
			s.Status = SkillStatusExperimental
		} else {
			s.Status = SkillStatusTrusted
		}
	}
	if s.Version < 1 {
		s.Version = 1
	}
	if s.ActiveVersion < 1 {
		s.ActiveVersion = s.Version
	}
}

// Routable reports whether the skill may appear in default hydration/search.
func (s *Skill) Routable() bool {
	if s == nil {
		return false
	}
	return s.Status == SkillStatusTrusted || s.Status == SkillStatusValidated
}

// CanAgentMutate reports whether the conversational agent may save a new
// version. Trusted curated skills are not overwritten in place; learned
// experimental skills may grow a new version.
func (s *Skill) CanAgentMutate() bool {
	if s == nil {
		return false
	}
	if s.Origin == SkillOriginLearned {
		return s.Status == SkillStatusCandidate || s.Status == SkillStatusExperimental
	}
	return false
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
	// CompactionSummaryMaxTokens is the max_output_tokens budget for the
	// compaction summarization call. Reasoning models count reasoning
	// tokens against this budget, so the default (64000) leaves room for
	// both reasoning and the summary content. 0 = use the built-in default.
	CompactionSummaryMaxTokens int `json:"compaction_summary_max_tokens,omitempty"`
	// CompactionSummaryMinChars is the minimum summary length (in chars)
	// for the compaction quality guard. Summaries shorter than this trigger
	// a retry with doubled budget. 0 = use the built-in default (200).
	CompactionSummaryMinChars int `json:"compaction_summary_min_chars,omitempty"`
	PromptCaching             bool
	MaxToolRounds             int
	MaxInputTokens            int // global context window cap (default 200k)
	MaxOutputTokens           int // default max completion tokens (default 64k)
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
	// OfflineSTT selects the local whisper.cpp transcription used by
	// read_media when no cloud fallback is configured (or as the offline
	// rung of the degradation ladder). Fields: the ggml model file
	// (bare name, e.g. "ggml-small.bin") and preferred language hint
	// ("", "id", "en"); audio read_media picks whichever engine is
	// configured without changing its own logic. Install lives in
	// Settings → Speech-to-Text (offline).
	STTOfflineModel    string `json:"stt_offline_model,omitempty"`
	STTOfflineLanguage string `json:"stt_offline_language,omitempty"`
	// SpeechGeneration (TTS) selects the model used by the generate_speech
	// tool. Online: an OpenAI-compatible /audio/speech endpoint
	// (TTSProviderID + TTSModelID, resolved like the other fallbacks).
	// Offline: piper with voice files under <data>/models/tts/ — live as
	// soon as engine + voice are installed (one-click Settings installer
	// or manual PIPER_BIN/PATH setup). When both routes are unavailable,
	// generate_speech is not advertised.
	TTSProviderID string `json:"tts_provider_id,omitempty"`
	TTSModelID    string `json:"tts_model_id,omitempty"`
	// VideoFallback selects an auxiliary video-capable model used to
	// describe video when the active chat model does not support video
	// input. When empty, non-video models receive an error message
	// directing the user to configure a fallback or switch to a
	// video-capable model.
	VideoProviderID string `json:"video_provider_id,omitempty"`
	VideoModelID    string `json:"video_model_id,omitempty"`
	// ImageGeneration selects the auxiliary image-generation model used by
	// the generate_image tool. Format of ImageModelID is the bare model id
	// (the picker stores provider and model separately, same as Vision).
	// When ImageProviderID is empty, generate_image is not advertised.
	ImageProviderID string `json:"image_provider_id,omitempty"`
	ImageModelID    string `json:"image_model_id,omitempty"`
	// VideoGeneration selects the auxiliary video-generation model used by
	// the generate_video tool. Separate from VideoProviderID/VideoModelID
	// (which is the video fallback for reading video). When
	// VideoGenProviderID is empty, generate_video is not advertised.
	VideoGenProviderID string `json:"video_gen_provider_id,omitempty"`
	VideoGenModelID    string `json:"video_gen_model_id,omitempty"`
	// WebAnswer configures the web_answer tool's answer provider. This is
	// separate from the chat providers — the user picks a searchwire-supported
	// vendor (brave, openrouter, openai, perplexity, anthropic, xai) and
	// supplies an API key manually. The API key is stored in the
	// CredentialStore under the "web_answer" key, not in settings JSON.
	// When WebAnswerProvider is empty, web_answer is not available.
	WebAnswerProvider string `json:"web_answer_provider,omitempty"`
	WebAnswerModel    string `json:"web_answer_model,omitempty"`
	// WebSearchStrategy routes web_search queries across searchwire's
	// sources: empty/"auto" merges every registered source (default),
	// "round_robin" rotates one API-keyed provider per query, "random"
	// picks one at random, and a bare source name (brave, serper, tavily,
	// startpage, wikipedia, github) pins the query to that source. The
	// per-provider API keys are stored in the CredentialStore under
	// web_search_brave / web_search_serper / web_search_tavily, not in
	// settings JSON.
	WebSearchStrategy string `json:"web_search_strategy,omitempty"`
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
	// ReviewModel selects the model used by background learning agents.
	// When empty, the conversation's active model is used. Format:
	// "providerID:modelID" (same as CompactionModel). Useful for routing
	// reviews to a cheaper/faster model; reviews re-send the transcript
	// tail, so the model choice directly affects background cost.
	ReviewModel string `json:"review_model,omitempty"`
	// LearnerNudgeInterval is the Hermes-style periodic spawn gate: how
	// many unreviewed user turns or tool-loop iterations enqueue the
	// learner without a structural signal. nil = product default (10).
	// 0 = disabled (structural signals only). Pointer so a missing JSON
	// field stays "use default" instead of collapsing to disabled.
	LearnerNudgeInterval *int `json:"learner_nudge_interval,omitempty"`
	// DelegateModel selects the model used by the internal delegate agent.
	// When empty, the delegate inherits the parent conversation's active model;
	// if the parent has no model, normal headless model resolution applies.
	// Format: "providerID:modelID".
	DelegateModel string `json:"delegate_model,omitempty"`
	// MaxAutoContinues is the outer multi-turn auto-continue budget.
	// After a successful sealed turn, if the conversation todo list
	// still has open items (pending or in_progress), the agent runner
	// starts the next turn without a user message, injecting the
	// `announcement` tool result with the continuation guidance. The chain stops when:
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
	// SlowDown is an artificial per-round delay in seconds applied before
	// every round of every conversation agent run (interactive turns,
	// auto-continue chains, and headless pipeline steps). Its purpose is
	// pacing: a slower agent is easier to step away from, and an
	// unattended run finishes closer to the prompt-cache TTL instead of
	// blowing through it instantly. The live setting is re-read on every
	// wait tick, so saving a new value applies immediately to running
	// conversations. 0 = off (default), max 60.
	SlowDown int `json:"slow_down,omitempty"`
	// UserPrompt is custom instructions the user wants injected into every
	// agent turn's system prompt. Placed after the cache-stable prefix
	// (system.md + tools.md) but before per-conversation system messages,
	// so changing it breaks the prompt cache for all subsequent turns until
	// a new cache shard stabilizes. Empty = no injection.
	UserPrompt string `json:"user_prompt,omitempty"`
	// PluginContractMode controls how mcp_call treats plugins that declare a
	// contract (contract.entry in manifest.json). "off" never gates; "hint"
	// attaches an advisory note to the first call per conversation; "require"
	// is an opt-in strict mode that rejects calls until contract_read ran for
	// that plugin in the same conversation. Empty = follow the factory
	// default (currently hint) — resolved at runtime by contractMode(); it
	// must stay empty in storage so future default changes reach saved
	// configs (anti-stamping).
	PluginContractMode string `json:"plugin_contract_mode,omitempty"`
	// ProjectMemoryBase is an absolute directory that holds {key}/{kind}.md
	// project-memory files. Empty uses {dataDir}/memory_project. Set to
	// ~/.memory (expanded on save) to share with other agents using the
	// automation-learning on-disk format.
	ProjectMemoryBase string `json:"project_memory_base,omitempty"`
}

// DefaultSettings returns the factory defaults. PluginContractMode is
// deliberately left empty here: empty means "follow the factory default" and
// keeps storage free of a stamped value, so changing the default later reaches
// every config that never chose explicitly.
func DefaultSettings() Settings {
	return Settings{
		CompactionEnabled:          true,
		CompactionThreshold:        0, // 0 = auto (80% of model context window)
		CompactionSummaryMaxTokens: 0, // 0 = use built-in default (64000)
		CompactionSummaryMinChars:  0, // 0 = use built-in default (200)
		PromptCaching:              true,
		MaxToolRounds:              8,
		MaxInputTokens:             200000,
		MaxOutputTokens:            65536,
		MaxParallelTools:           DefaultMaxParallelTools,
		MaxAutoContinues:           DefaultMaxAutoContinues,
		SoundNotifications:         true,
		RepeatedToolLimit:          3,
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
	// CompactionSummaryMaxTokens: 0 = use built-in default. Negative or
	// unset (-1 from JSON omitempty on older files) needs the default.
	// Clamp to a sane range so a typo can't starve or blow up the budget.
	if settings.CompactionSummaryMaxTokens < 0 {
		settings.CompactionSummaryMaxTokens = 0
	} else if settings.CompactionSummaryMaxTokens > 100000 {
		settings.CompactionSummaryMaxTokens = 100000
	}
	// CompactionSummaryMinChars: 0 = use built-in default. Negative or
	// unset needs the default. Clamp to a sane range.
	if settings.CompactionSummaryMinChars < 0 {
		settings.CompactionSummaryMinChars = 0
	} else if settings.CompactionSummaryMinChars > 100000 {
		settings.CompactionSummaryMinChars = 100000
	}
	// MaxAutoContinues: 0 is a valid sentinel (unlimited). Only negative
	// or unset (-1 from JSON omitempty on older files) needs the default.
	if settings.MaxAutoContinues < 0 {
		settings.MaxAutoContinues = DefaultSettings().MaxAutoContinues
	}
	if settings.LearnerNudgeInterval != nil {
		v := EffectiveLearnerNudgeInterval(settings.LearnerNudgeInterval)
		settings.LearnerNudgeInterval = &v
	}
	// RepeatedToolLimit: 0 is a valid sentinel (disabled). Negative or
	// unset (-1 from JSON omitempty on older files) needs the default.
	if settings.RepeatedToolLimit < 0 {
		settings.RepeatedToolLimit = DefaultSettings().RepeatedToolLimit
	}
	// SlowDown: 0 = off (default). Negative (stale -1 from JSON omitempty)
	// resets to off; clamp to the 60s cap so a hand-edit typo cannot stall
	// a turn for minutes.
	if settings.SlowDown < 0 {
		settings.SlowDown = 0
	} else if settings.SlowDown > 60 {
		settings.SlowDown = 60
	}
	// PluginContractMode: anti-stamping — empty means "follow the factory
	// default" and must STAY empty so future default changes reach saved
	// configs (a config stamped "require" once survived a factory-default
	// change in the field). Unknown values also reset to empty, never to a
	// concrete mode; contractMode() resolves the effective value at runtime.
	switch settings.PluginContractMode {
	case PluginContractOff, PluginContractHint, PluginContractRequire:
	default:
		settings.PluginContractMode = ""
	}
	return settings
}
