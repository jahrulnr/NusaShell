// Package application holds use cases, ports, and orchestration. It depends
// only on domain and contracts; I/O lives in infrastructure.
package application

import (
	"context"

	"nusashell/domain"
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
	// prefix. Used by Codex multi-account support to enumerate accounts
	// stored under "{providerID}:account:{accountID}" keys.
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

// LearningEdgeStore persists bitemporal edges between learning nodes.
type LearningEdgeStore interface {
	List() []*domain.LearningEdge
	Save(e *domain.LearningEdge) error
	Delete(id string) error
}

// ConversationTodoPort is the per-conversation todo checklist store. The
// model owns the list (full-replace via the `todo` tool); the user can
// delete items from the UI. The goal brief is set alongside items and
// survives compaction via hydration. Implementations must be safe for
// concurrent use.
type ConversationTodoPort interface {
	Get(conversationID string) []domain.TodoItem
	GetGoal(conversationID string) string
	Set(conversationID string, items []domain.TodoItem)
	SetGoal(conversationID string, goal string)
	Clear(conversationID string)
}

type LogStore interface {
	Append(e *domain.LogEntry)
	List(level string, limit int) []*domain.LogEntry
	Clear()
}

// CanvasArtifactStore persists interactive HTML/CSS/JS artifacts the agent
// produces via artifact_create / artifact_update. Artifacts are scoped per
// conversation. Implementations must be safe for concurrent use.
type CanvasArtifactStore interface {
	List(conversationID string) []*domain.CanvasArtifact
	Get(conversationID, id string) *domain.CanvasArtifact
	Save(conversationID string, a *domain.CanvasArtifact) error
	Delete(conversationID, id string) error
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

// CodexRuntime manages the official Codex CLI binary as a NusaShell-managed
// sidecar. The application layer uses this port to check runtime status and
// trigger downloads without depending on runtime package details.
type CodexRuntime interface {
	// Status returns the current runtime binary status: installed path +
	// version, or download progress/error if a download is in flight.
	Status() CodexRuntimeStatus
	// EnsureBinary returns the path to a usable Codex binary, downloading
	// it if necessary. If force is true, a re-download is triggered even
	// if a binary is already installed.
	EnsureBinary(ctx context.Context, force bool) (string, error)
}

// CodexRuntimeStatus is the runtime status snapshot returned by Status().
type CodexRuntimeStatus struct {
	Installed     bool
	Version       string
	Path          string
	Downloading   bool
	DownloadError string
}

// CodexOAuth performs the Codex ChatGPT OAuth PKCE login flow. The
// implementation opens a browser and blocks until the callback completes.
type CodexOAuth interface {
	Login(ctx context.Context) (CodexToken, error)
	// ExtractProfile decodes a stored token JSON and returns the email
	// and name. Used to enrich old tokens that lack email/name fields by
	// decoding the JWT access_token claims.
	ExtractProfile(tokenJSON string) (email, name string)
}

// CodexCLIAuthImporter reads the Codex CLI auth.json (~/.codex/auth.json)
// and returns the token in NusaShell's CodexToken shape. Used by the
// "Import from Codex CLI" flow so users who already logged in to the
// official Codex CLI don't need to re-login in NusaShell.
type CodexCLIAuthImporter interface {
	// ImportFromCodexCLI reads the Codex CLI auth.json and returns the
	// parsed token. Returns an error if the file is missing or invalid.
	ImportFromCodexCLI(ctx context.Context) (CodexToken, error)
}

// CodexUsage fetches the ChatGPT rate-limit usage for a stored OAuth token.
// The token JSON is the same string stored in CredentialStore.
type CodexUsage interface {
	FetchUsage(ctx context.Context, tokenJSON string) (CodexUsageResult, error)
}

// CodexUsageResult is the parsed usage snapshot returned by the Codex
// wham/usage endpoint.
type CodexUsageResult struct {
	Plan          string // "go", "plus", "pro", etc.
	LimitReached  bool
	PrimaryWindow *CodexUsageWindow
	// WeeklyWindow is the secondary window, if any (e.g. for review models).
	WeeklyWindow *CodexUsageWindow
	// ResetCreditsAvailable is the number of rate-limit reset credits
	// the user can spend to reset their usage window.
	ResetCreditsAvailable int
}

// CodexUsageWindow is one rate-limit window (session or weekly).
type CodexUsageWindow struct {
	UsedPercent       int   // 0-100
	ResetAt           int64 // unix seconds
	ResetAfterSeconds int64
}

// CodexToken is the result of a successful OAuth login.
type CodexToken struct {
	AccessToken  string
	RefreshToken string
	AccountID    string
	Email        string
	Name         string
	ExpiresAt    int64 // unix seconds, 0 = unknown
}

// CodexTokenJSON is the on-disk format for cached OAuth tokens, stored
// in CredentialStore as a JSON string. Matches codex.TokenJSON.
type CodexTokenJSON struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
	Email        string `json:"email,omitempty"`
	Name         string `json:"name,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

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
	Attachments []domain.Attachment // optional image attachments (read_image tool)
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
	// belongs to. Used by Codex adapter to set session-id and thread-id
	// headers, which enable server-side prompt cache hits across requests
	// in the same conversation.
	ConversationID string
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
// the window. For OpenAI-style providers InputTokens already includes cached
// tokens (CacheRead/CacheWrite stay 0); for Anthropic the cache fields are
// reported separately and must be added back.
func (u ChatUsage) ContextTokens() int {
	return u.InputTokens + u.CacheRead + u.CacheWrite + u.OutputTokens
}

type ChatResponse struct {
	Content    string
	Reasoning  string
	ToolCalls  []domain.ToolCall
	Usage      ChatUsage
	StopReason string
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

// ServerCompactor is an optional capability implemented by adapters that
// support server-side compaction (e.g. Codex /responses/compact). When the
// adapter implements this interface, compactConversation delegates to
// CompactServer instead of using the model to summarize client-side. If
// CompactServer returns an error (e.g. 404 for free accounts, network
// failure), the caller falls back to client-side compaction.
//
// The returned summary is a human-readable text summary for UI display and
// the compaction marker. The opaque blob (if any) is stored separately on
// the conversation via SetCompactionBlob so subsequent requests can pass it
// back to the server.
type ServerCompactor interface {
	CompactServer(ctx context.Context, c *domain.Conversation, model string, contextWindow int) (summary string, err error)
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

// SkillSearcher ranks the skill library for the skill_search tool.
// Implemented by App: BM25 + graph + recency with embedding forced off
// (per-call embedding cost in the agent loop is not justified; the Learning
// UI keeps the full hybrid path via LearningSearcher directly).
type SkillSearcher interface {
	SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error)
}

// Embedder is implemented by providers that can produce embedding vectors.
// This is an optional capability — not all AIProvider implementations support
// embeddings (e.g. Anthropic Messages, Codex OAuth). The learning layer uses
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
// embedding models endpoint (e.g. Codex OAuth). The factory is provider-kind
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
// Returns nil when the provider kind has no image-model catalog (Codex
// seeds gpt-image-2 at import/read time; Anthropic Messages has none).
type ImageModelListerFactory func(p *domain.Provider) ImageModelLister

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
	// TurnID is sent as x-codex-image-turn-id on Codex image requests
	// (official Codex CLI uses the tool-call turn id). Ignored by other backends.
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
// Returns an error when the provider kind has no image-generation API
// (Anthropic Messages, Ollama). Codex uses the ChatGPT plan image endpoints.
type ImageGeneratorFactory func(p *domain.Provider, apiKey string) (ImageGenerator, error)

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
