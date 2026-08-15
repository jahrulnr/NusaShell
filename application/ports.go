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
}

type SkillStore interface {
	List() []*domain.Skill
	Get(id string) (*domain.Skill, error)
	Save(s *domain.Skill) error
	Delete(id string) error
}

type MemoryStore interface {
	List() []*domain.MemoryEntry
	Save(e *domain.MemoryEntry) error
	Delete(id string) error
}

// ConversationTodoPort is the per-conversation todo checklist store. The
// model owns the list (full-replace via the `todo` tool); the user can
// delete items from the UI. Implementations must be safe for concurrent use.
type ConversationTodoPort interface {
	Get(conversationID string) []domain.TodoItem
	Set(conversationID string, items []domain.TodoItem)
	Clear(conversationID string)
}

type MCPServerStore interface {
	List() []*domain.MCPServer
	Get(id string) (*domain.MCPServer, error)
	Save(s *domain.MCPServer) error
	Delete(id string) error
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
	ToolCallID string
	Name       string
	Content    string
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

// ModelLister is implemented by providers that can enumerate models.
type ModelLister interface {
	ListModels(ctx context.Context, apiKey string) ([]domain.Model, error)
}

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
