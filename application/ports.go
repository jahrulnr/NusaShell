// Package application holds use cases, ports, and orchestration. It depends
// only on domain and contracts; I/O lives in infrastructure.
package application

import (
	"context"

	"nusashell/application/conversation"
	"nusashell/application/learn"
	"nusashell/application/memory"
	"nusashell/application/provider"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/modelcatalog"
)

// ---- persistence ports ----

// ConversationStore is the persistence adapter for conversation JSON files.
// Transcript writes from application code should go through
// ConversationRepository (NewConversation, Add, Save) so formed messages
// stay append-only. Compaction and new chats create a new conversation.
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

// ConversationFileLocator exposes the persisted JSON path for a conversation
// to bounded background inspection tools. It is optional so in-memory stores
// used by tests and alternate adapters need not pretend to have a file.
type ConversationFileLocator interface {
	ConversationPath(id string) string
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
	// skills are read-only. Used by the background learning agent to add
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
	// Promote sets Status=trusted. Only experimental or validated skills
	// can be promoted. This is the persistence primitive for a later
	// human RPC; CreatorMayPromote is always false so learners never
	// call this themselves.
	Promote(id, ownedBy string) (*domain.Skill, error)
	// Rollback checks out an immutable snapshot: ActiveVersion=version
	// and copies versions/<n>/SKILL.md over the root SKILL.md.
	Rollback(id, ownedBy string, version int) (*domain.Skill, error)
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

type ExperienceStore interface {
	List() []*domain.Experience
	Get(id string) (*domain.Experience, error)
	Save(e *domain.Experience) error
	ListByConversation(conversationID string) []*domain.Experience
	// Delete removes an experience. Experiences deleted by the user are
	// gone for good: they feed the learning pipeline, but the user owns
	// the catalog.
	Delete(id string) error
}

type MemoryRecordStore = memory.RecordStore

type LearningJobStore interface {
	List() []*domain.LearningJob
	Get(id string) (*domain.LearningJob, error)
	Save(j *domain.LearningJob) error
	// Delete removes a job row (e.g. when its learning log entry is
	// removed by the user).
	Delete(id string) error
}

type LearningOpStore = memory.OpStore

// MemoryDocumentStore is the shared contract for a single-file memory
// document (user.md and soul.md). Humans edit these via Learning UI RPCs.
// Agents write the files with file_*; typed learner JSON never writes them.
type MemoryDocumentStore interface {
	Load() *domain.MemoryDocument
	Update(entries []domain.DocumentEntry) error
	Replace(oldText, content string) error // substring match update
	// Path returns the absolute filesystem path of the document file.
	Path() string
}

type ProjectMemoryStore interface {
	Query(workspace string, q domain.ProjectMemoryQuery) ([]domain.ProjectMemoryHit, error)
	List(workspace string) ([]string, error)
	Read(workspace, kind, id string) (string, error)
	Admit(workspace, kind, id, content string) (domain.ProjectMemoryAdmitResult, error)
	Archive(workspace, id string) error
	Lint(workspace string, kinds ...string) ([]domain.ProjectMemoryLintProblem, error)
	IndexExtract(workspace string) (domain.ProjectIndexExtract, bool, error)
	Audit(workspace string) (string, error)
	Gate(workspace, reason string) (string, error)
	TrackPatterns(workspace, kind string) (string, error)
	Path(workspace, kind string, create bool) (string, error)
	ScriptPath(workspace, name string, create bool) (string, error)
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

// ModelOverrideStore persists the manual model-override registry
// (field-level corrections of catalog metadata, per provider+model). The
// store backs learning/model_overrides.json so corrections survive process
// restarts and catalog re-imports. Implementations must be safe for
// concurrent use.
type ModelOverrideStore interface {
	// Load returns the current registry, or an empty registry when no
	// override file exists yet. Never returns nil.
	Load() *domain.ModelOverrideRegistry
	// Save persists the registry atomically. Callers may pass the same
	// pointer they got from Load after mutating it.
	Save(r *domain.ModelOverrideRegistry) error
}

// ConversationMessenger enables inter-agent messaging and transcript
// inspection via the `conversation` tool.
type ConversationMessenger interface {
	List(currentConvID string, limit, offset int) (count int, items []ConversationSummaryDTO, err error)
	Search(currentConvID, query string, limit, offset int) (count int, items []ConversationSummaryDTO, err error)
	Send(currentConvID, targetConvID, content string) error
	Info(id string, chunk *int) (ConversationInfoDTO, error)
	Read(id string, chunk *int, start, end *int) (ConversationReadResult, error)
	SearchMessages(id, query string, limit, offset int) (count int, items []ConversationMessageHitDTO, err error)
}

// ConversationSummaryDTO is the compact room card used by the conversation tool.
type ConversationSummaryDTO = conversation.SummaryDTO

// ConversationInfoDTO is conversation(op=info) metadata.
type ConversationInfoDTO = conversation.InfoDTO

// ConversationReadResult is conversation(op=read) meta + messages.
type ConversationReadResult = conversation.ReadResult

// ConversationMessageHitDTO is a scoped conversation(op=search) hit.
type ConversationMessageHitDTO = conversation.MessageHitDTO

// ConversationTodoPort is the per-conversation todo checklist store. The
// model owns the list (full-replace via todo mode "new", add/replace/delete
// by ID); the user can delete items from the UI. The brief (a
// living planning document) is set alongside items and survives compaction
// via hydration. Implementations must be safe for concurrent use.
type ConversationTodoPort interface {
	Get(conversationID string) []domain.TodoItem
	GetBrief(conversationID string) string
	Set(conversationID string, items []domain.TodoItem)
	SetBrief(conversationID string, brief string)
	Clear(conversationID string)
	// ClearBrief removes the brief for the given conversation and deletes
	// the mirror plan file, leaving items intact. Persists to disk.
	ClearBrief(conversationID string) error
	// PlanPath returns the absolute path to the conversation's plan file
	// mirror (<datadir>/conversations/<id>/plan.md). Returns empty string
	// when the brief is empty or the path cannot be resolved.
	PlanPath(conversationID string) string
	// Patch merges items by ID into the existing list. Items with an
	// existing ID update their status (always) and content (only when
	// non-empty). Items with a new ID are appended. Items not in the
	// patch are kept unchanged. This is the backend for the todo tool's
	// add (append) and replace (update existing) modes.
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
	// Remove deletes the entire directory of attachments for a conversation
	// (<root>/<conversationID>/). Missing directories are a no-op success so
	// retried deletes stay safe. Unsafe conversation IDs (empty, separators,
	// "..", NUL) are rejected before any filesystem call. Implementations
	// that maintain no per-conversation directory may no-op.
	Remove(conversationID string) error
}

// DirectoryBrowser reads the host filesystem for the in-app workspace
// picker. The browser cannot see the server's folders, so the application
// exposes a bounded directory listing over RPC and validates the selected
// path before it is persisted as a conversation workspace.
type DirectoryBrowser interface {
	// ListDirs returns the subdirectories of path, sorted by name and
	// capped. An empty path means the host home directory; the resolved
	// absolute path and its parent are returned so the frontend breadcrumb
	// stays in sync.
	ListDirs(ctx context.Context, path string) (DirListing, error)
	// EnsureDir confirms the candidate workspace path exists and is a
	// directory.
	EnsureDir(ctx context.Context, path string) error
}

// DirListing is the DirectoryBrowser result. Entries is never nil.
type DirListing struct {
	Path      string
	Parent    string
	Entries   []contracts.WorkspaceDirEntry
	Truncated bool
}

// ---- Codex ports ----

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

// ---- AI provider port (owned by application/provider) ----

type ToolDef = provider.ToolDef
type ChatMessage = provider.ChatMessage
type ToolResult = provider.ToolResult
type ChatRequest = provider.ChatRequest
type PromptCachePolicy = provider.PromptCachePolicy
type ChatUsage = provider.ChatUsage
type ChatResponse = provider.ChatResponse
type AIProvider = provider.AIProvider
type ProviderContext = provider.Context
type ModelLister = provider.ModelLister
type ModelEndpointsLister = provider.ModelEndpointsLister
type EmbeddingModelLister = provider.EmbeddingModelLister

// SkillSearcher ranks the skill library for the skill tool (op=search).
// Implemented by App: BM25 + graph + recency with embedding forced off
// (per-call embedding cost in the agent loop is not justified; the Learning
// UI keeps the full hybrid path via LearningSearcher directly).
type SkillSearcher interface {
	SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error)
}

// Embedder / EmbedderFactory are owned by application/learn; root aliases
// keep infrastructure factories and App.EmbedderFactory compiling.
type (
	Embedder        = learn.Embedder
	EmbedderFactory = learn.EmbedderFactory
)

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

// Video generation request types live in application/media and are
// re-exported from media_wrappers.go so infrastructure keeps compiling
// against application.VideoGenRequest etc.

// VideoModelLister enumerates video-generation models via the dedicated
// /videos/models endpoint (OpenRouter). Hosts without it return empty.
type VideoModelLister interface {
	ListVideoModels(ctx context.Context, apiKey string) ([]string, error)
}

// VideoModelListerFactory builds a VideoModelLister. Returns nil when the
// provider kind cannot expose a video catalog.
type VideoModelListerFactory func(p *domain.Provider) VideoModelLister

// Image generation and speech transcription request types live in
// application/media and are re-exported from media_wrappers.go.

// ToolInfo / ToolExecutor live in application/tools and are re-exported
// from tools_wrappers.go so infrastructure keeps compiling against
// application.ToolInfo.

// ACP ports live in application/subagent and are re-exported from
// subagent_wrappers.go so infrastructure keeps compiling against
// application.AcpSpawnRequest / AcpRuntime / AcpAgentStore.

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

// PluginUIPort is the read-only plugin store surface needed by the plugin
// UI HTTP handler. It decouples transport from infrastructure/pluginfs so
// transport only depends on application + contracts. Implemented by
// *pluginfs.Store.
type PluginUIPort interface {
	List() ([]*domain.Plugin, error)
	Get(id string) (*domain.Plugin, error)
	UIDir(p *domain.Plugin) string
	// ToUIEntry resolves presentation values such as local file icons for the
	// browser-facing /plugins list. The adapter owns filesystem resolution;
	// transport stays independent from infrastructure packages.
	ToUIEntry(p *domain.Plugin) contracts.PluginUIEntryDTO
}

// PluginRuntimePort is the plugin runtime surface needed by the plugin UI
// HTTP handler. It decouples transport from infrastructure/pluginruntime.
// Implemented by *pluginruntime.Manager.
type PluginRuntimePort interface {
	EnsureStarted(ctx context.Context, pluginID string) ([]contracts.MCPToolDTO, error)
	ListTools(pluginID string) []contracts.MCPToolDTO
	CallTool(ctx context.Context, pluginID, toolName string, args map[string]any) (*contracts.PluginToolResult, error)
}
