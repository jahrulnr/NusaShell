package contracts

import "encoding/json"

// Method roster. The frontend only speaks these methods; adding one here
// without a handler keeps the contract explicit for handler-level tests.

const (
	MethodAppInfo = "app.info"

	MethodConversationsList          = "agent.conversations.list"
	MethodConversationsCreate        = "agent.conversations.create"
	MethodConversationsGet           = "agent.conversations.get"
	MethodConversationsRename        = "agent.conversations.rename"
	MethodConversationsDelete        = "agent.conversations.delete"
	MethodConversationsPickWorkspace = "agent.conversations.pick-workspace"
	MethodConversationsChunk         = "agent.conversations.chunk"
	MethodTurnsStart                 = "agent.turns.start"
	MethodTurnsStop                  = "agent.turns.stop"
	MethodTurnsRetry                 = "agent.turns.retry"
	MethodTurnsSteer                 = "agent.turns.steer"
	MethodTurnsCancelSteer           = "agent.turns.cancel-steer"
	MethodTurnsActive                = "agent.turns.active"

	MethodProvidersList   = "ai.providers.list"
	MethodProvidersSave   = "ai.providers.save"
	MethodProvidersDelete = "ai.providers.delete"
	MethodProvidersTest   = "ai.providers.test"
	MethodProvidersImport = "ai.providers.import-models"
	MethodModelsList      = "ai.models.list"

	MethodSkillsList   = "skills.list"
	MethodSkillsRead   = "skills.read"
	MethodSkillsSave   = "skills.save"
	MethodSkillsDelete = "skills.delete"
	MethodSkillsRun    = "skills.run"

	MethodMCPServersList   = "mcp.servers.list"
	MethodMCPServersSave   = "mcp.servers.save"
	MethodMCPServersDelete = "mcp.servers.delete"
	MethodMCPServersTest   = "mcp.servers.test"
	MethodMCPToolsList     = "mcp.tools.list"

	MethodMemoryList   = "memory.list"
	MethodMemorySave   = "memory.save"
	MethodMemorySearch = "memory.search"
	MethodMemoryDelete = "memory.delete"

	MethodDocsList   = "docs.list"
	MethodDocsSearch = "docs.search"
	MethodDocsRead   = "docs.read"

	MethodLogsList  = "logs.list"
	MethodLogsClear = "logs.clear"

	MethodSettingsGet = "settings.get"
	MethodSettingsSet = "settings.set"
)

// Event types pushed over SSE (/events) and WebSocket (/ws).
const (
	EventTurnStarted    = "agent.turn.started"
	EventMessageDelta   = "agent.message.delta"
	EventReasoningDelta = "agent.reasoning.delta"
	EventToolStarted    = "agent.tool.started"
	EventToolCompleted  = "agent.tool.completed"
	EventTurnDone       = "agent.turn.done"
	EventTurnError      = "agent.turn.error"
	EventCompacted      = "agent.compacted"
	EventSteerQueued    = "agent.steer.queued"
	EventSteerApplied   = "agent.steer.applied"
	EventSteerCancelled = "agent.steer.cancelled"
	EventProviderRetry  = "agent.provider.retry"
	EventLogAppend      = "logs.append"
)

// ---- app ----

type AppInfoResult struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	DataDir    string   `json:"data_dir"`
	Transports []string `json:"transports"`
	Features   Features `json:"features"`
}

type Features struct {
	Tools         bool     `json:"tools"`
	MCP           bool     `json:"mcp"`
	Compaction    bool     `json:"compaction"`
	PromptCaching bool     `json:"prompt_caching"`
	Providers     []string `json:"providers"`
}

// ---- conversations ----

type ConversationDTO struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count"`
	Model        string `json:"model,omitempty"`
	Effort       string `json:"effort,omitempty"`
	Status       string `json:"status,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	ChunkCount   int    `json:"chunk_count,omitempty"`
}

type UsageDTO struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	CacheRead    int `json:"cache_read,omitempty"`
	CacheWrite   int `json:"cache_write,omitempty"`
}

type ToolCallDTO struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args,omitempty"`
	Status string          `json:"status,omitempty"`
	Output string          `json:"output,omitempty"`
}

type MessageStepDTO struct {
	Type      string        `json:"type"` // reasoning | text | tool_calls
	Content   string        `json:"content,omitempty"`
	ToolCalls []ToolCallDTO `json:"tool_calls,omitempty"`
}

type MessageDTO struct {
	ID          string           `json:"id"`
	Role        string           `json:"role"`
	Content     string           `json:"content"`
	Reasoning   string           `json:"reasoning,omitempty"`
	Steps       []MessageStepDTO `json:"steps,omitempty"`
	Model       string           `json:"model,omitempty"`
	Usage       *UsageDTO        `json:"usage,omitempty"`
	CreatedAt   string           `json:"created_at"`
	Status      string           `json:"status,omitempty"`
	Error       string           `json:"error,omitempty"`
	ToolCalls   []ToolCallDTO    `json:"tool_calls,omitempty"`
	Attachments []AttachmentDTO  `json:"attachments,omitempty"`
	Steer       bool             `json:"steer,omitempty"`
}

type AttachmentDTO struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Content   string `json:"content,omitempty"`
	DataURL   string `json:"data_url,omitempty"`
}

type ConversationsListResult struct {
	Conversations []ConversationDTO `json:"conversations"`
}

type ConversationCreateRequest struct {
	Title string `json:"title,omitempty"`
}

type ConversationGetResult struct {
	Conversation ConversationDTO `json:"conversation"`
	Messages     []MessageDTO    `json:"messages"`
}

type ConversationIDRequest struct {
	ID string `json:"id"`
}

// ConversationChunkRequest loads an archived pre-compaction chunk by index.
// Chunks are indexed from 0 (oldest) to ChunkCount-1 (newest). The frontend
// loads them in reverse order (newest chunk first) when the user scrolls up.
type ConversationChunkRequest struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
}

type ConversationChunkResult struct {
	Messages []MessageDTO `json:"messages"`
}

type ConversationRenameRequest struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ---- turns ----

type TurnStartRequest struct {
	ConversationID string          `json:"conversation_id"`
	Text           string          `json:"text"`
	Model          string          `json:"model"`
	Effort         string          `json:"effort,omitempty"`
	Attachments    []AttachmentDTO `json:"attachments,omitempty"`
}

// TurnRetryRequest re-runs the last failed assistant message in a conversation
// with a different model picked by the user. When the failed message has
// partial content (and no tool calls), the partial is frozen as a completed
// step and the new model is asked to continue from where it stopped; otherwise
// the failed message is re-run from scratch.
type TurnRetryRequest struct {
	ConversationID string `json:"conversation_id"`
	Model          string `json:"model"`
	Effort         string `json:"effort,omitempty"`
}

type TurnStartResult struct {
	RunID string `json:"run_id"`
}

type TurnStopRequest struct {
	RunID string `json:"run_id"`
}

type TurnSteerRequest struct {
	ConversationID string          `json:"conversation_id"`
	Text           string          `json:"text"`
	Attachments    []AttachmentDTO `json:"attachments,omitempty"`
}

type TurnCancelSteerRequest struct {
	ConversationID string `json:"conversation_id"`
}

// TurnActiveResult describes the currently running turn for a conversation.
// Returned by agent.turns.active so a refreshed frontend can re-attach its
// streaming UI and route new messages to steering instead of start.
type TurnActiveResult struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Active         bool   `json:"active"`
}

type TurnStartedEvent struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Round          int    `json:"round"`
}

type MessageDeltaEvent struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Text           string `json:"text"`
}

type ReasoningDeltaEvent struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Text           string `json:"text"`
}

type ToolStartedEvent struct {
	RunID          string          `json:"run_id"`
	ConversationID string          `json:"conversation_id"`
	ToolCallID     string          `json:"tool_call_id"`
	Name           string          `json:"name"`
	Args           json.RawMessage `json:"args,omitempty"`
}

type ToolCompletedEvent struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	ToolCallID     string `json:"tool_call_id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Output         string `json:"output,omitempty"`
}

type TurnDoneEvent struct {
	RunID          string    `json:"run_id"`
	ConversationID string    `json:"conversation_id"`
	MessageID      string    `json:"message_id"`
	Model          string    `json:"model,omitempty"`
	Usage          *UsageDTO `json:"usage,omitempty"`
	Error          string    `json:"error,omitempty"`
}

type TurnErrorEvent struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
}

type CompactedEvent struct {
	ConversationID string `json:"conversation_id"`
	Summary        string `json:"summary"`
}

type SteerEvent struct {
	ConversationID string `json:"conversation_id"`
	SteerID        string `json:"steer_id,omitempty"`
	Text           string `json:"text,omitempty"`
	Status         string `json:"status"`
}

// ProviderRetryEvent is emitted when the agent retries a provider request
// after a retryable error (429, 5xx, transient network). The frontend uses
// this to show a "Retrying (2/3)…" banner so the user knows the agent is
// not stuck.
type ProviderRetryEvent struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Attempt        int    `json:"attempt"`
	MaxAttempts    int    `json:"max_attempts"`
	DelayMS        int64  `json:"delay_ms"`
	Error          string `json:"error"`
}

// ---- providers / models ----

type ModelDTO struct {
	ID               string   `json:"id"`
	ProviderID       string   `json:"provider_id"`
	ProviderName     string   `json:"provider_name"`
	Context          int      `json:"context,omitempty"`
	MaxOutput        int      `json:"max_output,omitempty"`
	InputCost        float64  `json:"input_cost,omitempty"`
	Description      string   `json:"description,omitempty"`
	SupportedEfforts []string `json:"supported_efforts,omitempty"`
	DefaultEffort    string   `json:"default_effort,omitempty"`
}

type ProviderDTO struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"` // anthropic | openai | compatible
	Name       string     `json:"name"`
	BaseURL    string     `json:"base_url,omitempty"`
	Enabled    bool       `json:"enabled"`
	Configured bool       `json:"configured"`
	HasAPIKey  bool       `json:"has_api_key"`
	Models     []ModelDTO `json:"models,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type ProvidersListResult struct {
	Providers []ProviderDTO `json:"providers"`
}

type ProviderSaveRequest struct {
	ID      string `json:"id,omitempty"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	Enabled bool   `json:"enabled"`
}

type ProviderIDRequest struct {
	ID string `json:"id"`
}

type ImportModelsResult struct {
	Models []ModelDTO `json:"models"`
}

type ModelsListResult struct {
	Models []ModelDTO `json:"models"`
}

// ---- skills ----

type SkillDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

type SkillsListResult struct {
	Skills []SkillDTO `json:"skills"`
}

type SkillFull struct {
	SkillDTO
	Content string `json:"content"`
}

type SkillReadResult struct {
	Skill SkillFull `json:"skill"`
}

type SkillSaveRequest struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
}

type SkillIDRequest struct {
	ID string `json:"id"`
}

type SkillRunResult struct {
	ConversationID string `json:"conversation_id"`
}

// ---- mcp ----

type MCPToolDTO struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type MCPServerDTO struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
	Status  string            `json:"status,omitempty"`
	Tools   []MCPToolDTO      `json:"tools,omitempty"`
}

type MCPServersListResult struct {
	Servers []MCPServerDTO `json:"servers"`
}

type MCPSaveRequest struct {
	ID      string            `json:"id,omitempty"`
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

type MCPIDRequest struct {
	ID string `json:"id"`
}

type MCPTestResult struct {
	Tools []MCPToolDTO `json:"tools"`
}

type MCPToolsListResult struct {
	Tools []MCPToolDTO `json:"tools"`
}

// ---- memory ----

type MemoryEntryDTO struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
}

type MemoryListResult struct {
	Entries []MemoryEntryDTO `json:"entries"`
}

type MemorySaveRequest struct {
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
}

type MemorySearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type MemoryIDRequest struct {
	ID string `json:"id"`
}

// ---- docs ----

type DocDTO struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

type DocsListResult struct {
	Docs []DocDTO `json:"docs"`
}

type DocsSearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type DocsSearchResult struct {
	Results []DocHit `json:"results"`
}

type DocHit struct {
	DocDTO
	Snippet string `json:"snippet"`
}

type DocReadRequest struct {
	ID string `json:"id"`
}

type DocReadResult struct {
	Doc DocFull `json:"doc"`
}

type DocFull struct {
	DocDTO
	Content string `json:"content"`
}

// ---- logs ----

type LogEntryDTO struct {
	ID      string `json:"id"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

type LogsListRequest struct {
	Level string `json:"level,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type LogsListResult struct {
	Entries []LogEntryDTO `json:"entries"`
}

type LogAppendEvent struct {
	Entry LogEntryDTO `json:"entry"`
}

// ---- settings ----

type SettingsDTO struct {
	CompactionEnabled   bool `json:"compaction_enabled"`
	CompactionThreshold int  `json:"compaction_threshold"`
	PromptCaching       bool `json:"prompt_caching"`
	MaxToolRounds       int  `json:"max_tool_rounds"`
	MaxInputTokens      int  `json:"max_input_tokens"`
	MaxOutputTokens     int  `json:"max_output_tokens"`
}

type SettingsGetResult struct {
	Settings SettingsDTO `json:"settings"`
}

type SettingsSetRequest struct {
	CompactionEnabled   *bool `json:"compaction_enabled,omitempty"`
	CompactionThreshold *int  `json:"compaction_threshold,omitempty"`
	PromptCaching       *bool `json:"prompt_caching,omitempty"`
	MaxToolRounds       *int  `json:"max_tool_rounds,omitempty"`
	MaxInputTokens      *int  `json:"max_input_tokens,omitempty"`
	MaxOutputTokens     *int  `json:"max_output_tokens,omitempty"`
}
