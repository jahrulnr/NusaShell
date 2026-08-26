// Package application holds use cases, ports, and orchestration. It depends
// only on domain and contracts; I/O lives in infrastructure.
package application

import (
	"context"

	"nusashell/domain"
	"nusashell/infrastructure/ai/modelcatalog"
)

// ---- persistence ports ----

type ConversationStore interface {
	List() []*domain.Conversation
	Get(id string) (*domain.Conversation, error)
	Save(c *domain.Conversation) error
	Delete(id string) error
	// ArchiveChunk persists a slice of messages as an archived pre-compaction
	// chunk for later scroll-back retrieval. The chunk index is assigned by
	// the store (sequential, starting from 0).
	ArchiveChunk(id string, messages []domain.Message) (int, error)
	// GetChunk retrieves an archived chunk by index. Returns ErrNotFound if
	// the chunk does not exist.
	GetChunk(id string, index int) ([]domain.Message, error)
}

type ProviderStore interface {
	List() []*domain.Provider
	Get(id string) (*domain.Provider, error)
	Save(p *domain.Provider) error
	Delete(id string) error
}

// CredentialStore keeps API keys out of the JSON/JSONL files.
type CredentialStore interface {
	Get(providerID string) (string, bool, error)
	Set(providerID, key string) error
	Delete(providerID string) error
	// ListByPrefix returns all provider IDs that start with the given
	// prefix.
	ListByPrefix(prefix string) ([]string, error)
}

type SkillStore interface {
	List() []*domain.Skill
	// Get returns a skill by ID. If ownedBy is empty, priority resolution
	// picks user > builtin > plugin. If ownedBy is set (e.g. "plugin:acme"),
	// returns the exact skill with that owner or not-found.
	Get(id, ownedBy string) (*domain.Skill, error)
	Save(s *domain.Skill) error
	// Delete removes a skill. If ownedBy is empty, deletes the highest-
	// priority skill with that ID. Plugin-owned skills cannot be deleted
	// directly — uninstall the plugin instead.
	Delete(id, ownedBy string) error
	// ReadFile reads any text file inside a skill directory (default
	// SKILL.md) with offset/maxChars pagination. ownedBy resolution is
	// the same as Get.
	ReadFile(id, ownedBy, path string, offset, maxChars int) (*domain.SkillFile, error)
	// WriteFile writes content to a file inside a skill directory (default
	// SKILL.md). Parent directories (references/, templates/, scripts/)
	// are created as needed. The skill must already exist; plugin-owned
	// skills are read-only. Used by the background review agent to add
	// support files and patch existing ones for skill self-improvement.
	WriteFile(id, ownedBy, path, content string) error
	// Files lists the nested directory tree of a skill folder (path,
	// type, sizeBytes, editable), sorted as in the Electron shell.
	// ownedBy resolution is the same as Get.
	Files(id, ownedBy string) ([]domain.SkillFileEntry, error)
	// Install extracts a .skill (zip) archive into the skill root and
	// registers the skill metadata. The archive must contain a top-level
	// directory with a SKILL.md file. Returns the installed skill ID.
	Install(zipData []byte) (string, error)
	// MountPluginSkills scans a plugin's skills/ directory and registers
	// all skill packages found there with owned_by="plugin:<pluginID>".
	// File content is read from the plugin directory (mount, no copy).
	MountPluginSkills(pluginID, pluginSkillsDir string) error
	// UnmountPluginSkills removes all skills owned by plugin:<pluginID>
	// from the metadata catalog. Files in the plugin directory are not
	// touched (the plugin uninstaller handles those).
	UnmountPluginSkills(pluginID string) error
}

// PluginStore is the single source of truth for plugins (MCP servers and
// MCP + UI plugins). A plugin is installed from the catalog, a GitHub repo,
// a ZIP archive, or created manually; its manifest carries the MCP server
// connection config plus optional UI metadata.
type PluginStore interface {
	List() ([]*domain.Plugin, error)
	Get(id string) (*domain.Plugin, error)
	Install(sourceDir string) (*domain.Plugin, error)
	Uninstall(id string) error
	Save(p *domain.Plugin) error
	Delete(id string) error
}

// PluginInstaller fetches plugins from the curated catalog, a GitHub
// repository, or a local ZIP archive and installs them.
type PluginInstaller interface {
	Catalog(ctx context.Context) ([]domain.PluginCatalogEntry, error)
	Install(ctx context.Context, req domain.PluginInstallRequest) (*domain.Plugin, error)
	// CheckUpdates returns catalog entries newer than their installed match.
	CheckUpdates(ctx context.Context, installed []*domain.Plugin) ([]domain.PluginCatalogEntry, error)
	// Update reinstalls a catalog plugin at its latest version.
	Update(ctx context.Context, pluginID string) (*domain.Plugin, error)
}

type MemoryStore interface {
	List() []*domain.MemoryEntry
	Save(e *domain.MemoryEntry) error
	Delete(id string) error
	Replace(target, oldText, content string) error
}

// PrimaryStore is the always-injected primary memory document backed by a
// single primary.md file. The document body is free-form prose that the
// agent edits in place via Replace (substring match) or Update (rewrite
// the entire body). There is no per-entry create/delete — the agent
// maintains the document like a README.
type PrimaryStore interface {
	Load() *domain.PrimaryMemory
	Update(entries []domain.PrimaryEntry) error
	Replace(oldText, content string) error // substring match update
}

// FragmentStore is the unlimited, searchable memory archive backed by
// one markdown file per entry under memory/fragments/. Foreground
// agents create, update, delete, and search fragments.
type FragmentStore interface {
	List(filter domain.FragmentSearchFilter) []*domain.MemoryFragment
	Get(id string) *domain.MemoryFragment
	Save(f *domain.MemoryFragment) error
	Delete(id string) error
	Search(filter domain.FragmentSearchFilter) []domain.FragmentSearchHit
}

// FragmentSaveIfAbsent is an optional capability for fragment stores that can
// atomically enforce exact-content idempotency. Callers should fall back to a
// deterministic List scan when a store does not implement it.
type FragmentSaveIfAbsent interface {
	SaveIfAbsent(f *domain.MemoryFragment) (existing *domain.MemoryFragment, saved bool, err error)
}

// LearningEdgeStore persists bitemporal edges between learning nodes.
type LearningEdgeStore interface {
	List() []*domain.LearningEdge
	Save(e *domain.LearningEdge) error
	Delete(id string) error
}

// LearnedParamStore persists the dynamic 400-learning registry (unsupported
// params to strip + required fields to inject, per provider+model). The
// store backs learning/provider_params.json so adaptations survive process
// restarts. Implementations must be safe for concurrent use.
type LearnedParamStore interface {
	// Load returns the current registry, or an empty registry when no
	// learning file exists yet. Never returns nil.
	Load() *domain.LearnedParamRegistry
	// Save persists the registry atomically. Callers may pass the same
	// pointer they got from Load after mutating it.
	Save(r *domain.LearnedParamRegistry) error
}

// ConversationTodoPort is the per-conversation todo checklist store. The
// model owns the list (full-replace via the `todo` tool, or patch by ID
// via mode:"patch"); the user can delete items from the UI. The brief (a
// living planning document) is set alongside items and survives compaction
// via hydration. Implementations must be safe for concurrent use.
type ConversationTodoPort interface {
	Get(conversationID string) []domain.TodoItem
	GetBrief(conversationID string) string
	Set(conversationID string, items []domain.TodoItem)
	SetBrief(conversationID string, brief string)
	Clear(conversationID string)
	// Patch merges items by ID into the existing list. Items with an
	// existing ID update their status (always) and content (only when
	// non-empty). Items with a new ID are appended. Items not in the
	// patch are kept unchanged. This is the backend for the todo tool's
	// mode:"patch" — it lets the model update a single item's status
	// without re-emitting the full list.
	Patch(conversationID string, items []domain.TodoItem)
}

type LogStore interface {
	Append(e *domain.LogEntry)
	List(level string, limit int) []*domain.LogEntry
	Clear()
}

type SettingsStore interface {
	Get() domain.Settings
	Set(s domain.Settings) error
}

// AttachmentStore saves image/file attachments to disk so file-based tools
// (shell, python, etc.) can access them by absolute path. Text attachments
// are not saved (they stay inline).
type AttachmentStore interface {
	// Save writes the attachment data to disk under <root>/<conversationID>/
	// and returns the absolute path. Only image and file attachments are
	// saved; text attachments are skipped (returns "").
	Save(conversationID string, att domain.Attachment) (string, error)
	// WriteBytes writes raw bytes under <root>/<conversationID>/<name> and
	// returns the absolute path. Used for generated images that never had a
	// DataURL in conversation JSON.
	WriteBytes(conversationID, name string, data []byte) (string, error)
	// ReadFile returns the bytes of a previously saved attachment. The path
	// must live under the store root; paths outside it are rejected.
	ReadFile(absPath string) ([]byte, error)
}

// WorkspacePicker is the host-native directory chooser. It lives behind an
// application port because a browser cannot disclose an absolute local path to
// the Go process safely, while an Electron-equivalent workspace needs one.
type WorkspacePicker interface {
	Choose(ctx context.Context) (string, error)
}

type WorkspacePickerFunc func(ctx context.Context) (string, error)

func (f WorkspacePickerFunc) Choose(ctx context.Context) (string, error) { return f(ctx) }

// ---- AI provider port ----

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type ChatMessage struct {
	Role        string // user | assistant | system | tool
	Content     string
	Reasoning   string              // assistant thinking text (persisted, not replayed)
	ToolCalls   []domain.ToolCall   // assistant
	ToolResult  *ToolResult         // tool
	Attachments []domain.Attachment // user only
}

type ToolResult struct {
	ToolCallID  string
	Name        string
	Content     string
	Attachments []domain.Attachment // optional image attachments (read_media tool)
}

type ChatRequest struct {
	Model         string
	System        string
	Messages      []ChatMessage
	Tools         []ToolDef
	PromptCaching bool
	MaxTokens     int
	Effort        string // reasoning effort: "auto" (omit) or a level from the model's SupportedEfforts
	// Sampling parameters. nil = use provider default. Set from
	// domain.Settings at turn start.
	Temperature      *float64
	TopP             *float64
	TopK             *int
	FrequencyPenalty *float64
	PresencePenalty  *float64
	// PromptCache controls provider-side prompt caching. When non-nil and
	// PromptCaching is true, adapters translate the policy to their native
	// wire format (prompt_cache_key for OpenAI, cache_control for Anthropic).
	PromptCache *PromptCachePolicy
	// ConversationID is the stable ID of the conversation this request
	// belongs to. Adapters may use it for server-side prompt cache routing
	// across requests in the same conversation.
	ConversationID string
	// ReasoningReplay is true when the target upstream requires
	// reasoning_content (Chat Completions) or reasoning items (Responses
	// API) to be echoed back on every assistant message in subsequent
	// turns. Resolved from the model's InterleavedField catalog signal
	// (preferred) or a provider/model pattern fallback, or upgraded at
	// runtime by the dynamic 400-learning classifier. When false, the
	// field is omitted — providers that ignore it (OpenAI, Anthropic)
	// are unaffected.
	ReasoningReplay bool
	// StripParams is the list of request fields the dynamic 400-learning
	// classifier has marked as unsupported for this provider+model. Each
	// entry names a ChatRequest field ("reasoning_effort", "temperature",
	// "top_p", "top_k", "frequency_penalty", "presence_penalty"). The
	// adapter omits the field from the wire request when listed here.
	StripParams []string
}

// PromptCachePolicy is the provider-neutral cache intent. Adapters translate
// it to their native wire format. Mirrors the TS AgentPromptCachePolicy.
type PromptCachePolicy struct {
	// Mode: "auto" (default) or "off". "off" disables caching even when
	// the provider supports it.
	Mode string
	// TTL: "5m" (default) or "1h". Anthropic supports 1h; OpenAI ignores it.
	TTL string
	// Key is a stable routing key sent to OpenAI-compatible providers as
	// prompt_cache_key so they can dedup cache entries across requests in
	// the same conversation. Format: "pc_<sha256>".
	Key string
	// StableSystemMessages is the number of leading system messages that
	// are cache-stable. Anthropic marks only the last of these with
	// cache_control instead of the entire system block, so volatile tail
	// content (user prompt, memory, todos) doesn't break cache hits.
	StableSystemMessages int
}

type ChatUsage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
}

// ContextTokens is the authoritative context fill for a single
// request/response round: the full prompt (uncached input plus any cached or
// cache-written input) plus the generated output. This is what actually
// occupies the model's context window after the round.
//
// Use the LAST round's ContextTokens as the conversation's context usage, not
// the sum of per-round usage: each tool round re-sends the growing history, so
// summing InputTokens across rounds double counts the prompt and can exceed
// the window.
//
// After Option A normalization, InputTokens is the UNCACHED input for all
// providers: OpenAI-style adapters (chat-completion, responses) subtract
// cached_tokens from prompt_tokens at the handler boundary; Anthropic reports
// input_tokens as uncached already. ContextTokens therefore sums
// InputTokens + CacheRead + CacheWrite + OutputTokens uniformly — no
// per-provider branching needed.
func (u ChatUsage) ContextTokens() int {
	return u.InputTokens + u.CacheRead + u.CacheWrite + u.OutputTokens
}

type ChatResponse struct {
	Content    string
	Reasoning  string
	ToolCalls  []domain.ToolCall
	Usage      ChatUsage
	StopReason string
	// Warnings carries provider-level notices (dropped unsupported content
	// blocks, malformed tool arguments, strict-tool omissions) that would
	// otherwise be silently lost. Empty when the provider reported none.
	Warnings []string
}

// AIProvider is the streaming/non-streaming chat port implemented by the
// Anthropic, Responses and OpenAI-compatible adapters.
type AIProvider interface {
	Kind() domain.ProviderKind
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
	// Stream reports text deltas and, when the provider emits thinking
	// content, reasoning deltas.
	Stream(ctx context.Context, req ChatRequest, onDelta, onReasoning func(text string)) (ChatResponse, error)
}

// ModelLister is implemented by providers that can enumerate models.
type ModelLister interface {
	ListModels(ctx context.Context, apiKey string) ([]domain.Model, error)
}

// EmbeddingModelLister is implemented by providers that can enumerate
// embedding models separately from chat models. Some AI gateways expose
// embedding models on a dedicated /embeddings/models endpoint rather than
// the standard /models endpoint. This interface lets the application layer
// fetch embedding models from any provider kind (chat, responses, messages)
// without coupling the embedding concern to a specific chat adapter.
type EmbeddingModelLister interface {
	ListEmbeddingModels(ctx context.Context, apiKey string) ([]string, error)
}

// SkillSearcher ranks the skill library for the skill tool (op=search).
// Implemented by App: BM25 + graph + recency with embedding forced off
// (per-call embedding cost in the agent loop is not justified; the Learning
// UI keeps the full hybrid path via LearningSearcher directly).
type SkillSearcher interface {
	SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error)
}

// Embedder is implemented by providers that can produce embedding vectors.
// This is an optional capability — not all AIProvider implementations support
// embeddings (e.g. Anthropic Messages). The learning layer uses
// this to build a vector index for semantic skill/memory search. When no
// configured provider implements Embedder, the learning layer falls back to
// BM25-only keyword search.
type Embedder interface {
	// Embed returns a vector for a single text input.
	Embed(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch returns vectors for multiple inputs in one call.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	// Dim returns the embedding dimensionality. Must be stable across calls.
	Dim() int
}

// EmbedderFactory builds an Embedder for a given provider, if the provider
// supports embeddings. Returns nil, nil if the provider kind does not support
// embeddings (caller falls back to BM25). Returns an error only on auth or
// connectivity failure.
type EmbedderFactory func(p *domain.Provider, apiKey string) (Embedder, error)

// EmbeddingModelListerFactory builds an EmbeddingModelLister for a given
// provider. Returns nil if the provider kind does not expose a separate
// embedding models endpoint. The factory is provider-kind
// agnostic — AI gateways often support multiple chat APIs while exposing
// embeddings on a single OpenAI-compatible endpoint, so the same lister
// works for chat, responses, and messages kinds.
type EmbeddingModelListerFactory func(p *domain.Provider) EmbeddingModelLister

// ImageModelLister enumerates image-generation models from a dedicated
// /images/models endpoint (OpenRouter). OpenAI-compatible hosts that lack
// the endpoint return an empty list; the importer still tags known ids
// from /models (gpt-image-1, dall-e-3, …).
type ImageModelLister interface {
	ListImageModels(ctx context.Context, apiKey string) ([]string, error)
}

// ImageModelListerFactory builds an ImageModelLister for a given provider.
// Returns nil when the provider kind has no image-model catalog (Anthropic
// seeds gpt-image-2 at import/read time; Anthropic Messages has none).
type ImageModelListerFactory func(p *domain.Provider) ImageModelLister

// SpeechModelLister enumerates speech-generation models via the
// output_modalities=speech filter on /models (OpenRouter's documented TTS
// discovery route). Hosts that lack the filter return an empty list; known
// TTS ids from plain /models are still tagged via the models.dev catalog
// and the config allowlist.
type SpeechModelLister interface {
	ListSpeechModels(ctx context.Context, apiKey string) ([]string, error)
}

// SpeechModelListerFactory builds a SpeechModelLister for a given provider.
// Returns nil when the provider kind cannot expose a speech catalog (Anthropic
// OAuth; Anthropic Messages).
type SpeechModelListerFactory func(p *domain.Provider) SpeechModelLister

// ---- video generation port ----

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

// VideoModelLister enumerates video-generation models via the dedicated
// /videos/models endpoint (OpenRouter). Hosts without it return empty.
type VideoModelLister interface {
	ListVideoModels(ctx context.Context, apiKey string) ([]string, error)
}

// VideoModelListerFactory builds a VideoModelLister. Returns nil when the
// provider kind cannot expose a video catalog.
type VideoModelListerFactory func(p *domain.Provider) VideoModelLister

// ---- image generation port ----

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
	// TurnID is sent as a turn correlation header by image backends that
	// support it (currently unused; reserved for future attribution).
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
	Provider    string // "openai" | "openrouter"
	Model       string
	UsageTokens int
	CostUSD     float64
}

// ImageGeneratorFactory builds an ImageGenerator for a configured provider.
// Returns an error when the provider kind has no image-generation API
// (Anthropic Messages has none). OpenAI and OpenRouter hosts serve image
// generation directly.
type ImageGeneratorFactory func(p *domain.Provider, apiKey string) (ImageGenerator, error)

// ---- speech transcription port ----

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

// ---- agent tools port ----

type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type ToolExecutor interface {
	ListTools() []ToolInfo
	Execute(ctx context.Context, name string, argsJSON []byte) (string, error)
}

// ---- ACP subagent ports ----

type AcpAgentStore interface {
	List() []*domain.AcpAgent
	Get(id string) (*domain.AcpAgent, error)
	Save(a *domain.AcpAgent) error
	Delete(id string) error
}

type AcpSpawnRequest struct {
	Agent            *domain.AcpAgent
	ConversationID   string
	ParentToolCallID string
	Prompt           string
	Workspace        string
	ModeID           string
	ModelID          string
}

type AcpPermissionDecision struct {
	OptionID string
	Outcome  domain.PermissionOutcome
}

type AcpRuntime interface {
	Probe(ctx context.Context, agent *domain.AcpAgent) (domain.AcpAgent, error)
	Authenticate(ctx context.Context, agent *domain.AcpAgent, methodID string) error
	RefreshCatalog(ctx context.Context, agent *domain.AcpAgent) (domain.AcpAgent, error)
	Spawn(ctx context.Context, req AcpSpawnRequest) (*domain.AcpRun, error)
	Steer(runID, text string) error
	Stop(runID string) error
	Wait(ctx context.Context, runID string) (*domain.AcpRun, error)
	Get(runID string) (*domain.AcpRun, bool)
	List(conversationID string) []*domain.AcpRun
	DecidePermission(runID, requestID, optionID string, outcome domain.PermissionOutcome) error
	PromoteRisk(runID string, tier domain.RiskTier) error
	SetMode(ctx context.Context, runID, modeID string) error
	Close()
}

// ModelCataloger is the read-only capability source used to enrich
// provider models (context window, pricing, reasoning, vision, ...). It
// never writes models: the /models API (and endpoint-specific listers) are
// the only writers of the provider model list, and model IDs are kept
// verbatim. Implemented by *modelcatalog.Catalog.
type ModelCataloger interface {
	EnsureLoaded(ctx context.Context) error
	Loaded() bool
	Lookup(providerHint, modelID string) *modelcatalog.ModelMetadata
}
