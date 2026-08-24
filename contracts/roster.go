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
	MethodAskAnswer                  = "agent.ask.answer"
	MethodAskCancel                  = "agent.ask.cancel"
	MethodAskPending                 = "agent.ask.pending"

	MethodProvidersList   = "ai.providers.list"
	MethodProvidersSave   = "ai.providers.save"
	MethodProvidersDelete = "ai.providers.delete"
	MethodProvidersTest   = "ai.providers.test"
	MethodProvidersImport = "ai.providers.import-models"
	MethodModelsList      = "ai.models.list"

	// Codex-specific methods. Codex uses OAuth (not API keys), has a
	// managed runtime binary (auto-downloaded), and supports multiple
	// ChatGPT accounts per provider.
	MethodCodexLogin           = "ai.codex.login"
	MethodCodexLogout          = "ai.codex.logout"
	MethodCodexImport          = "ai.codex.import"
	MethodCodexAccountsList    = "ai.codex.accounts.list"
	MethodCodexAccountsSwitch  = "ai.codex.accounts.switch"
	MethodCodexRefreshCircuits = "ai.codex.refresh-circuits"
	MethodCodexRuntimeStatus   = "ai.codex.runtime.status"
	MethodCodexRuntimeDownload = "ai.codex.runtime.download"
	MethodCodexUsage           = "ai.codex.usage"

	MethodSkillsList     = "skills.list"
	MethodSkillsRead     = "skills.read"
	MethodSkillsSave     = "skills.save"
	MethodSkillsDelete   = "skills.delete"
	MethodSkillsFileRead = "skills.file.read"
	MethodSkillsInstall  = "skills.install"

	MethodPluginList          = "plugin.list"
	MethodPluginSave          = "plugin.save"
	MethodPluginDelete        = "plugin.delete"
	MethodPluginTest          = "plugin.test"
	MethodPluginStop          = "plugin.stop"
	MethodPluginToolsList     = "plugin.tools.list"
	MethodPluginUninstall     = "plugin.uninstall"
	MethodPluginCatalog       = "plugin.catalog"
	MethodPluginInstall       = "plugin.install"
	MethodPluginCheckUpdates  = "plugin.check_updates"
	MethodPluginUpdate        = "plugin.update"
	MethodPluginSetAutoUpdate = "plugin.set_autoupdate"
	MethodPluginSetAutoStart  = "plugin.set_autostart"

	MethodMemoryList   = "memory.list"
	MethodMemorySave   = "memory.save"
	MethodMemorySearch = "memory.search"
	MethodMemoryDelete = "memory.delete"

	MethodTodosGet    = "agent.todos.get"
	MethodTodosDelete = "agent.todos.delete"

	MethodDocsList   = "docs.list"
	MethodDocsSearch = "docs.search"
	MethodDocsRead   = "docs.read"

	MethodLogsList  = "logs.list"
	MethodLogsClear = "logs.clear"

	MethodSettingsGet = "settings.get"
	MethodSettingsSet = "settings.set"

	// Offline TTS one-click install (piper binary + voice models).
	MethodSettingsTTSInstallStatus = "settings.tts_install_status"
	MethodSettingsTTSInstallStart  = "settings.tts_install_start"

	// Offline STT one-click install (whisper.cpp engine + ggml models).
	MethodSettingsSTTInstallStatus = "settings.stt_install_status"
	MethodSettingsSTTInstallStart  = "settings.stt_install_start"
	MethodSettingsSTTInstallCancel = "settings.stt_install_cancel"

	// ACP agents are spawn-only subagents (not user chat providers).
	MethodAcpAgentsList           = "acp.agents.list"
	MethodAcpAgentsSave           = "acp.agents.save"
	MethodAcpAgentsDelete         = "acp.agents.delete"
	MethodAcpAgentsProbe          = "acp.agents.probe"
	MethodAcpAgentsAuthenticate   = "acp.agents.authenticate"
	MethodAcpAgentsRefreshCatalog = "acp.agents.refresh-catalog"
	MethodAcpRunsList             = "acp.runs.list"
	MethodAcpRunsGet              = "acp.runs.get"
	MethodAcpRunsSteer            = "acp.runs.steer"
	MethodAcpRunsStop             = "acp.runs.stop"
	MethodAcpRunsWait             = "acp.runs.wait"
	MethodAcpRunsPromote          = "acp.runs.promote"
	MethodAcpRunsSetMode          = "acp.runs.set-mode"
	MethodAcpPermissionDecide     = "acp.permission.decide"
)

// Event types pushed over WebSocket (/ws).
const (
	EventTurnStarted      = "agent.turn.started"
	EventMessageDelta     = "agent.message.delta"
	EventContextEstimate  = "agent.context.estimate"
	EventReasoningDelta   = "agent.reasoning.delta"
	EventToolStarted      = "agent.tool.started"
	EventToolCompleted    = "agent.tool.completed"
	EventTurnDone         = "agent.turn.done"
	EventTurnError        = "agent.turn.error"
	EventCompacted        = "agent.compacted"
	EventCompactionFailed = "agent.compaction.failed"
	EventSteerQueued      = "agent.steer.queued"
	EventSteerApplied     = "agent.steer.applied"
	EventSteerCancelled   = "agent.steer.cancelled"
	EventProviderRetry    = "agent.provider.retry"
	EventLogAppend        = "logs.append"
	EventTodoUpdated      = "agent.todo.updated"
	EventAutoContinue     = "agent.auto_continue"
	EventAskPending       = "agent.ask.pending"
	EventAskAnswered      = "agent.ask.answered"
	EventAskCancelled     = "agent.ask.cancelled"

	EventLearningReviewStarted = "learning.review.started"
	EventLearningReviewDone    = "learning.review.done"
	EventLearningReviewError   = "learning.review.error"

	EventMemoryUpdated = "memory.updated"
	EventSkillUpdated  = "skill.updated"

	// Offline TTS install progress (settings.tts_install_start runs in the
	// background; these events drive the install dialog).
	EventTTSInstallProgress = "tts.install.progress"
	EventTTSInstallDone     = "tts.install.done"
	EventTTSInstallError    = "tts.install.error"

	EventSTTInstallProgress = "stt.install.progress"
	EventSTTInstallDone     = "stt.install.done"
	EventSTTInstallError    = "stt.install.error"

	EventAcpRunStarted          = "acp.run.started"
	EventAcpRunUpdated          = "acp.run.updated"
	EventAcpRunDone             = "acp.run.done"
	EventAcpPermissionRequested = "acp.permission.requested"
	EventAcpPermissionDecided   = "acp.permission.decided"
	EventAcpSessionModeChanged  = "acp.session.mode_changed"
)

// ---- app ----

type AppInfoResult struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	DataDir  string   `json:"data_dir"`
	Features Features `json:"features"`
}

type Features struct {
	Tools         bool     `json:"tools"`
	MCP           bool     `json:"mcp"`
	Compaction    bool     `json:"compaction"`
	PromptCaching bool     `json:"prompt_caching"`
	Automation    bool     `json:"automation"`
	Providers     []string `json:"providers"`
}

// ---- conversations ----

type ConversationDTO struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	MessageCount    int    `json:"message_count"`
	Model           string `json:"model,omitempty"`
	Effort          string `json:"effort,omitempty"`
	Status          string `json:"status,omitempty"`
	Workspace       string `json:"workspace,omitempty"`
	ChunkCount      int    `json:"chunk_count,omitempty"`
	EstimatedTokens int64  `json:"estimated_tokens,omitempty"`
	// ContextTokens is the authoritative provider-measured context fill after
	// the last completed turn; the UI's idle context badge prefers it over the
	// EstimatedTokens heuristic.
	ContextTokens int64 `json:"context_tokens,omitempty"`
}

type UsageDTO struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	CacheRead    int `json:"cache_read,omitempty"`
	CacheWrite   int `json:"cache_write,omitempty"`
}

type ToolCallDTO struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Args              json.RawMessage `json:"args,omitempty"`
	Status            string          `json:"status,omitempty"`
	Output            string          `json:"output,omitempty"`
	OutputAttachments []AttachmentDTO `json:"output_attachments,omitempty"`
}

type MessageStepDTO struct {
	Type      string        `json:"type"` // reasoning | text | tool_calls
	Content   string        `json:"content,omitempty"`
	ToolCalls []ToolCallDTO `json:"tool_calls,omitempty"`
}

type MessageDTO struct {
	ID             string           `json:"id"`
	Role           string           `json:"role"`
	Content        string           `json:"content"`
	Reasoning      string           `json:"reasoning,omitempty"`
	Steps          []MessageStepDTO `json:"steps,omitempty"`
	Model          string           `json:"model,omitempty"`
	ProviderID     string           `json:"provider_id,omitempty"`
	Usage          *UsageDTO        `json:"usage,omitempty"`
	CreatedAt      string           `json:"created_at"`
	Status         string           `json:"status,omitempty"`
	Error          string           `json:"error,omitempty"`
	ToolCalls      []ToolCallDTO    `json:"tool_calls,omitempty"`
	Attachments    []AttachmentDTO  `json:"attachments,omitempty"`
	Steer          bool             `json:"steer,omitempty"`
	AutoContinue   bool             `json:"auto_continue,omitempty"`
	ContextUpdated bool             `json:"context_updated,omitempty"`
}

type AttachmentDTO struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Content   string `json:"content,omitempty"`
	DataURL   string `json:"data_url,omitempty"`
	FilePath  string `json:"file_path,omitempty"` // absolute path for folder/file references (desktop only)
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

// ContextEstimateEvent carries a lightweight server-side estimate of the
// actual request payload (system prompt + messages + tool definitions) so
// the UI badge reflects the tokens really sent to the provider.
type ContextEstimateEvent struct {
	RunID           string `json:"run_id"`
	ConversationID  string `json:"conversation_id"`
	MessageID       string `json:"message_id"`
	EstimatedTokens int64  `json:"estimated_tokens"`
	SystemTokens    int64  `json:"system_tokens,omitempty"`
	MessagesTokens  int64  `json:"messages_tokens,omitempty"`
	ToolsTokens     int64  `json:"tools_tokens,omitempty"`
}

type ToolStartedEvent struct {
	RunID          string          `json:"run_id"`
	ConversationID string          `json:"conversation_id"`
	ToolCallID     string          `json:"tool_call_id"`
	Name           string          `json:"name"`
	Args           json.RawMessage `json:"args,omitempty"`
}

type ToolCompletedEvent struct {
	RunID          string          `json:"run_id"`
	ConversationID string          `json:"conversation_id"`
	ToolCallID     string          `json:"tool_call_id"`
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	Output         string          `json:"output,omitempty"`
	Attachments    []AttachmentDTO `json:"attachments,omitempty"`
}

type TodoUpdatedEvent struct {
	ConversationID string         `json:"conversation_id"`
	Items          []TodoItemDTO  `json:"items"`
	Summary        TodoSummaryDTO `json:"summary"`
	Brief          string         `json:"brief,omitempty"`
}

type TurnDoneEvent struct {
	RunID          string    `json:"run_id"`
	ConversationID string    `json:"conversation_id"`
	MessageID      string    `json:"message_id"`
	Model          string    `json:"model,omitempty"`
	Usage          *UsageDTO `json:"usage,omitempty"`
	// ContextTokens is the authoritative context fill after this turn (last
	// round input + cached input + output). The UI badge uses it as the
	// source of truth, unlike Usage which sums per-round tokens for display.
	ContextTokens int              `json:"context_tokens,omitempty"`
	Error         string           `json:"error,omitempty"`
	AutoContinue  *AutoContinueDTO `json:"auto_continue,omitempty"`
}

// AutoContinueDTO mirrors domain.AutoContinueDecision for the wire. When
// ShouldContinue is true, the agent runner will start the next turn without
// a user message, injecting the continue.md steering prompt.
type AutoContinueDTO struct {
	ShouldContinue   bool   `json:"should_continue"`
	OpenTodoCount    int    `json:"open_todo_count"`
	ContinuesUsed    int    `json:"continues_used"`
	MaxAutoContinues int    `json:"max_auto_continues"`
	Reason           string `json:"reason"`
}

// AutoContinueEvent is emitted at each auto-continue chain step so the UI
// can show "Continuing tasks… (N/M)" and update the strip. ContinueText
// carries the synthetic user message content (continue.md) so the UI can
// insert it into the transcript as a user message.
type AutoContinueEvent struct {
	ConversationID string          `json:"conversation_id"`
	RunID          string          `json:"run_id"`
	Decision       AutoContinueDTO `json:"decision"`
	ContinueText   string          `json:"continue_text,omitempty"`
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

// CompactionFailedEvent is emitted when context compaction fails so the UI
// can toast the user. The turn continues with the un-compacted conversation;
// this is a warning, not a turn-fatal error. Emergency compaction failures
// (context overflow) still go through EventTurnError since they fail the turn.
type CompactionFailedEvent struct {
	ConversationID string `json:"conversation_id"`
	Error          string `json:"error"`
}

// LearningReviewEvent is emitted when the background learning review
// (autolearn) starts, completes, or errors so the UI can toast the
// user. The review is fire-and-forget; Status is "started", "done", or
// "error". When Status is "error", Error carries the failure reason.
// Cooldown skips are recorded in the learning trajectory rather than emitted
// as lifecycle events, so the UI does not show a false start/done pair.
type LearningReviewEvent struct {
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status"`           // "started" | "done" | "error"
	Reason         string `json:"reason,omitempty"` // "threshold" | "compaction"
	Error          string `json:"error,omitempty"`  // failure message when status="error"
}

type SteerEvent struct {
	ConversationID string `json:"conversation_id"`
	SteerID        string `json:"steer_id,omitempty"`
	Text           string `json:"text,omitempty"`
	Status         string `json:"status"`
}

// ---- ask_question ----

// AskOptionDTO mirrors domain.AskQuestionOption for the wire.
type AskOptionDTO struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Image       string `json:"image,omitempty"`
}

// AskPendingEvent is emitted when the model calls ask_question and the tool
// is waiting for the user's answer. The UI renders a question card with the
// options and optional free-text input.
type AskPendingEvent struct {
	ConversationID string         `json:"conversation_id"`
	RunID          string         `json:"run_id"`
	ToolCallID     string         `json:"tool_call_id"`
	Question       string         `json:"question"`
	Options        []AskOptionDTO `json:"options"`
	AllowFreeText  bool           `json:"allow_free_text"`
	MultiSelect    bool           `json:"multi_select"`
}

// AskPendingListRequest lists in-flight ask_question calls for a conversation
// so the UI can rebuild interactive cards after a room switch or reload.
type AskPendingListRequest struct {
	ConversationID string `json:"conversation_id"`
}

// AskPendingListResult carries the pending asks (same shape as the live event).
type AskPendingListResult struct {
	Asks []AskPendingEvent `json:"asks"`
}

// AskAnswerRequest is the RPC payload for agent.ask.answer. The UI sends the
// user's selected option IDs and/or free text.
type AskAnswerRequest struct {
	RunID      string   `json:"run_id"`
	ToolCallID string   `json:"tool_call_id"`
	Via        string   `json:"via"` // "option" or "text"
	OptionIDs  []string `json:"option_ids,omitempty"`
	Text       string   `json:"text,omitempty"`
}

// AskAnswerResult is the RPC response for agent.ask.answer.
type AskAnswerResult struct {
	OK        bool     `json:"ok"`
	Answer    string   `json:"answer"`
	Via       string   `json:"via"`
	OptionIDs []string `json:"option_ids,omitempty"`
}

// AskCancelRequest is the RPC payload for agent.ask.cancel.
type AskCancelRequest struct {
	RunID      string `json:"run_id"`
	ToolCallID string `json:"tool_call_id"`
	Reason     string `json:"reason,omitempty"`
}

// AskAnsweredEvent is emitted when the user answers an ask_question, so other
// UI surfaces (e.g. the tool card) can update.
type AskAnsweredEvent struct {
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	ToolCallID     string `json:"tool_call_id"`
	Answer         string `json:"answer"`
	Via            string `json:"via"`
}

// AskCancelledEvent is emitted when an ask_question is cancelled (by the user
// or because the turn ended).
type AskCancelledEvent struct {
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	ToolCallID     string `json:"tool_call_id"`
	Reason         string `json:"reason,omitempty"`
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
	// Kind classifies the failure mode (http_status, connect, sse_transport,
	// idle_timeout) so the frontend can show a specific retry reason.
	Kind   string `json:"kind,omitempty"`
	Status int    `json:"status,omitempty"`
}

// ---- providers / models ----

type ModelDTO struct {
	ID               string   `json:"id"`
	ProviderID       string   `json:"provider_id"`
	ProviderName     string   `json:"provider_name"`
	DisplayName      string   `json:"display_name,omitempty"`
	Context          int      `json:"context,omitempty"`
	MaxOutput        int      `json:"max_output,omitempty"`
	InputCost        float64  `json:"input_cost,omitempty"`
	OutputCost       float64  `json:"output_cost,omitempty"`
	CacheReadCost    float64  `json:"cache_read_cost,omitempty"`
	Description      string   `json:"description,omitempty"`
	SupportedEfforts []string `json:"supported_efforts,omitempty"`
	DefaultEffort    string   `json:"default_effort,omitempty"`
	Kind             string   `json:"kind,omitempty"`
	ToolCall         bool     `json:"tool_call,omitempty"`
	StructuredOutput bool     `json:"structured_output,omitempty"`
	Reasoning        bool     `json:"reasoning,omitempty"`
	Vision           bool     `json:"vision,omitempty"`
	Audio            bool     `json:"audio,omitempty"`
	Video            bool     `json:"video,omitempty"`
	TTS              bool     `json:"tts,omitempty"`
	VideoGen         bool     `json:"video_gen,omitempty"`
	KnowledgeCutoff  string   `json:"knowledge_cutoff,omitempty"`
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

// ---- codex ----

// CodexLoginRequest triggers the OAuth PKCE flow for a Codex provider.
// The browser opens the ChatGPT auth page; after callback, the token
// is stored in CredentialStore and returned (without the refresh token).
type CodexLoginRequest struct {
	ProviderID string `json:"provider_id"`
}

type CodexLoginResult struct {
	AccountID string `json:"account_id,omitempty"`
	Email     string `json:"email,omitempty"`
}

// CodexImportRequest imports a token from the Codex CLI auth.json
// (~/.codex/auth.json) into NusaShell's CredentialStore. If the account
// is already stored, the import is skipped (idempotent).
type CodexImportRequest struct {
	ProviderID string `json:"provider_id"`
}

// CodexImportResult reports the outcome of a Codex CLI import.
type CodexImportResult struct {
	AccountID string `json:"account_id,omitempty"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
	// Skipped is true when the account was already present in the store.
	Skipped bool `json:"skipped,omitempty"`
}

// CodexLogoutRequest removes a stored OAuth token for a specific account.
// If AccountID is empty, the active account is removed.
type CodexLogoutRequest struct {
	ProviderID string `json:"provider_id"`
	AccountID  string `json:"account_id,omitempty"`
}

// CodexAccountDTO describes one stored ChatGPT account.
type CodexAccountDTO struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
	Active    bool   `json:"active"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	// CircuitOpen is true when the account's usage quota is exhausted
	// and the circuit breaker is open. The account will be skipped by
	// the router until CircuitOpenUntil.
	CircuitOpen      bool  `json:"circuit_open,omitempty"`
	CircuitOpenUntil int64 `json:"circuit_open_until,omitempty"` // unix seconds
}

type CodexAccountsListRequest struct {
	ProviderID string `json:"provider_id"`
}

type CodexAccountsListResult struct {
	Accounts []CodexAccountDTO `json:"accounts"`
}

// CodexAccountsSwitchRequest sets a different account as the active one.
type CodexAccountsSwitchRequest struct {
	ProviderID string `json:"provider_id"`
	AccountID  string `json:"account_id"`
}

// CodexRuntimeStatusResult reports the managed Codex binary state.
type CodexRuntimeStatusResult struct {
	Installed     bool   `json:"installed"`
	Version       string `json:"version,omitempty"`
	Path          string `json:"path,omitempty"`
	Downloading   bool   `json:"downloading,omitempty"`
	DownloadError string `json:"download_error,omitempty"`
}

type CodexRuntimeDownloadRequest struct {
	// Force re-download even if a binary is already installed.
	Force bool `json:"force,omitempty"`
}

type CodexRuntimeDownloadResult struct {
	Version string `json:"version"`
	Path    string `json:"path"`
}

// ---- codex usage ----

type CodexUsageRequest struct {
	ProviderID string `json:"provider_id"`
}

// CodexUsageWindowDTO is one rate-limit window (session or weekly).
type CodexUsageWindowDTO struct {
	UsedPercent       int   `json:"used_percent"`
	RemainingPercent  int   `json:"remaining_percent"`
	ResetAt           int64 `json:"reset_at,omitempty"` // unix seconds
	ResetAfterSeconds int64 `json:"reset_after_seconds,omitempty"`
}

type CodexUsageResult struct {
	Plan                  string               `json:"plan,omitempty"`
	LimitReached          bool                 `json:"limit_reached"`
	PrimaryWindow         *CodexUsageWindowDTO `json:"primary_window,omitempty"`
	WeeklyWindow          *CodexUsageWindowDTO `json:"weekly_window,omitempty"`
	ResetCreditsAvailable int                  `json:"reset_credits_available"`
}

// CodexAccountUsage is the usage snapshot for a single account, combined
// with its identity and circuit-breaker status. Used by the unified
// accounts+usage view in the frontend.
type CodexAccountUsage struct {
	AccountID   string `json:"account_id"`
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
	Active      bool   `json:"active"`
	CircuitOpen bool   `json:"circuit_open,omitempty"`
	// CircuitOpenUntil is the unix timestamp at which the circuit breaker
	// will close (usage reset). 0 if the circuit is closed or the reset
	// time is unknown.
	CircuitOpenUntil int64 `json:"circuit_open_until,omitempty"`
	// Usage fields — empty if fetch failed for this account
	Plan                  string               `json:"plan,omitempty"`
	LimitReached          bool                 `json:"limit_reached"`
	PrimaryWindow         *CodexUsageWindowDTO `json:"primary_window,omitempty"`
	WeeklyWindow          *CodexUsageWindowDTO `json:"weekly_window,omitempty"`
	ResetCreditsAvailable int                  `json:"reset_credits_available"`
	// Error is set when usage fetch failed for this account
	Error string `json:"error,omitempty"`
}

// CodexAccountsUsageResult is the response for ai.codex.usage when
// returning usage for all accounts (not just the active one).
type CodexAccountsUsageResult struct {
	Accounts []CodexAccountUsage `json:"accounts"`
}

// ---- skills ----

type SkillDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	State       string `json:"state,omitempty"`
	Origin      string `json:"origin,omitempty"`
	OwnedBy     string `json:"owned_by,omitempty"`
	Shadowed    bool   `json:"shadowed,omitempty"`
	Pinned      bool   `json:"pinned"`
	UsageCount  int    `json:"usage_count,omitempty"`
	LastUsedAt  string `json:"last_used_at,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

type SkillsListResult struct {
	Skills []SkillDTO `json:"skills"`
}

type SkillFull struct {
	SkillDTO
	Content string         `json:"content"`
	Files   []SkillFileDTO `json:"files,omitempty"`
}

type SkillFileDTO struct {
	Path      string `json:"path"`
	Type      string `json:"type"` // "file" | "directory"
	SizeBytes int64  `json:"sizeBytes"`
	Editable  bool   `json:"editable"`
}

type SkillReadResult struct {
	Skill SkillFull `json:"skill"`
}

type SkillFileReadRequest struct {
	ID       string `json:"id"`
	OwnedBy  string `json:"owned_by,omitempty"`
	Path     string `json:"path"`
	Offset   int    `json:"offset,omitempty"`
	MaxChars int    `json:"maxChars,omitempty"`
}

type SkillFileReadResult struct {
	Content    string `json:"content"`
	SizeBytes  int64  `json:"sizeBytes"`
	Truncated  bool   `json:"truncated"`
	NextOffset int    `json:"nextOffset,omitempty"`
}

type SkillInstallRequest struct {
	// Data is the base64-encoded .skill (zip) archive content.
	Data string `json:"data"`
	// Filename is the original filename (e.g. "my-skill.skill") used for
	// error messages and logging only.
	Filename string `json:"filename,omitempty"`
}

type SkillInstallResult struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SkillSaveRequest struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path,omitempty"`
	Content     string `json:"content"`
}

type SkillIDRequest struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
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
	Plugin  bool              `json:"plugin,omitempty"`
	HasUI   bool              `json:"hasUI,omitempty"`
	// Plugin metadata (populated only when Plugin == true) so the MCP
	// drawer can render icon, version, category, and install path without
	// a separate plugin.detail call.
	Version     string `json:"version,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Category    string `json:"category,omitempty"`
	InstallPath string `json:"installPath,omitempty"`
}

// PluginDTO is the wire representation of an installed plugin.
type PluginDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Plugin distinguishes an installed plugin (catalog/GitHub/ZIP or any
	// entry exposing a UI) from a plain manual stdio MCP server the user
	// added by hand. The UI uses it to pick badges and drawer actions.
	Plugin bool `json:"plugin"`
	// Catalog is true when this plugin's id exists in the curated catalog,
	// so it can be updated (manual "Update") and auto-updated. GitHub/ZIP
	// and manual MCP servers are not catalog-managed.
	Catalog         bool         `json:"catalog"`
	Version         string       `json:"version"`
	Icon            string       `json:"icon"`
	Category        string       `json:"category,omitempty"`
	HasUI           bool         `json:"hasUI"`
	InstallPath     string       `json:"installPath"`
	Status          string       `json:"status,omitempty"` // idle | connected
	Tools           []MCPToolDTO `json:"tools,omitempty"`
	AutoUpdate      bool         `json:"autoUpdate"`
	UpdateAvailable string       `json:"updateAvailable,omitempty"` // catalog version when newer
	Autostart       bool         `json:"autostart"`
	// ContractEntry is the plugin-relative usage-contract file declared in
	// the manifest (contract.entry). Empty when the plugin declares none.
	ContractEntry string             `json:"contractEntry,omitempty"`
	Manifest      *PluginManifestDTO `json:"manifest,omitempty"`
}

type PluginManifestDTO struct {
	ID   string       `json:"id"`
	Name string       `json:"name"`
	UI   *PluginUIDTO `json:"ui,omitempty"`
	MCP  PluginMCPDTO `json:"mcp"`
}

type PluginUIDTO struct {
	Entry  string          `json:"entry"`
	Window PluginWindowDTO `json:"window,omitempty"`
}

type PluginWindowDTO struct {
	Mode        string `json:"mode,omitempty"`
	DefaultSize struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"defaultSize,omitempty"`
	Resizable bool `json:"resizable,omitempty"`
}

type PluginMCPDTO struct {
	Transport string            `json:"transport"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Autostart bool              `json:"autostart,omitempty"`
	KeepAlive bool              `json:"keepAliveOnClose,omitempty"`
}

type PluginListResult struct {
	Plugins []PluginDTO `json:"plugins"`
}

type PluginSetFlagRequest struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type PluginIDRequest struct {
	ID string `json:"id"`
}

type PluginCatalogEntry struct {
	ID          string `json:"id"`
	PluginID    string `json:"pluginId"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Tag         string `json:"tag"`
	ReleasedAt  string `json:"releasedAt,omitempty"`
}

type PluginCatalogResult struct {
	Plugins []PluginCatalogEntry `json:"plugins"`
}

type PluginInstallRequest struct {
	Source string `json:"source"`
	ID     string `json:"id,omitempty"`
	URL    string `json:"url,omitempty"`
	Subdir string `json:"subdir,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Data   string `json:"data,omitempty"` // base64-encoded ZIP
}

type PluginInstallResult struct {
	Plugin *PluginDTO `json:"plugin,omitempty"`
}

// PluginSaveRequest creates or updates a manual MCP-server plugin. The
// plugin is persisted as <datadir>/plugins/<id>/manifest.json like any
// installed plugin.
type PluginSaveRequest struct {
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Autostart bool              `json:"autostart,omitempty"`
}

type PluginTestResult struct {
	Tools []MCPToolDTO `json:"tools"`
}

type PluginToolsListResult struct {
	Tools []MCPToolDTO `json:"tools"`
}

// ---- memory ----

type MemoryEntryDTO struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags,omitempty"`
	Source    string   `json:"source,omitempty"`
	CreatedAt string   `json:"created_at"`
	// Fragment metadata (populated when reading from the fragments store).
	Category string `json:"category,omitempty"`
	Project  string `json:"project,omitempty"`
	Task     string `json:"task,omitempty"`
	// Tier marks the memory source: "primary" (always-injected) or
	// "fragment" (searchable archive). Empty for legacy entries.
	Tier string `json:"tier,omitempty"`
}

type MemoryListResult struct {
	Entries []MemoryEntryDTO `json:"entries"`
}

type MemorySaveRequest struct {
	Content  string   `json:"content"`
	Tags     []string `json:"tags,omitempty"`
	Category string   `json:"category,omitempty"`
	Project  string   `json:"project,omitempty"`
	Task     string   `json:"task,omitempty"`
}

type MemorySearchRequest struct {
	Query    string   `json:"query"`
	Limit    int      `json:"limit,omitempty"`
	Category string   `json:"category,omitempty"`
	Project  string   `json:"project,omitempty"`
	Task     string   `json:"task,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

type MemoryIDRequest struct {
	ID string `json:"id"`
}

// ---- todos ----

type TodoItemDTO struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type TodoSummaryDTO struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
}

type TodosGetRequest struct {
	ConversationID string `json:"conversation_id"`
}

type TodosGetResult struct {
	ConversationID string         `json:"conversation_id"`
	Items          []TodoItemDTO  `json:"items"`
	Summary        TodoSummaryDTO `json:"summary"`
	Brief          string         `json:"brief,omitempty"`
}

type TodosDeleteRequest struct {
	ConversationID string   `json:"conversation_id"`
	IDs            []string `json:"ids"`
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
	CompactionEnabled          bool     `json:"compaction_enabled"`
	CompactionThreshold        int      `json:"compaction_threshold"`
	CompactionModel            string   `json:"compaction_model,omitempty"`
	CompactionSummaryMaxTokens int      `json:"compaction_summary_max_tokens,omitempty"`
	CompactionSummaryMinChars  int      `json:"compaction_summary_min_chars,omitempty"`
	PromptCaching              bool     `json:"prompt_caching"`
	MaxToolRounds              int      `json:"max_tool_rounds"`
	RepeatedToolLimit          int      `json:"repeated_tool_limit,omitempty"`
	MaxInputTokens             int      `json:"max_input_tokens"`
	MaxOutputTokens            int      `json:"max_output_tokens"`
	MaxParallelTools           int      `json:"max_parallel_tools,omitempty"`
	ReviewModel                string   `json:"review_model,omitempty"`
	EmbeddingProviderID        string   `json:"embedding_provider_id,omitempty"`
	EmbeddingModelID           string   `json:"embedding_model_id,omitempty"`
	VisionProviderID           string   `json:"vision_provider_id,omitempty"`
	VisionModelID              string   `json:"vision_model_id,omitempty"`
	AudioProviderID            string   `json:"audio_provider_id,omitempty"`
	AudioModelID               string   `json:"audio_model_id,omitempty"`
	STTOfflineModel            string   `json:"stt_offline_model,omitempty"`
	STTOfflineLanguage         string   `json:"stt_offline_language,omitempty"`
	VideoProviderID            string   `json:"video_provider_id,omitempty"`
	VideoModelID               string   `json:"video_model_id,omitempty"`
	TTSProviderID              string   `json:"tts_provider_id,omitempty"`
	TTSModelID                 string   `json:"tts_model_id,omitempty"`
	TTSOfflineEnabled          bool     `json:"tts_offline_enabled,omitempty"`
	ImageProviderID            string   `json:"image_provider_id,omitempty"`
	ImageModelID               string   `json:"image_model_id,omitempty"`
	WebAnswerProvider          string   `json:"web_answer_provider,omitempty"`
	WebAnswerModel             string   `json:"web_answer_model,omitempty"`
	Temperature                *float64 `json:"temperature,omitempty"`
	TopP                       *float64 `json:"top_p,omitempty"`
	TopK                       *int     `json:"top_k,omitempty"`
	FrequencyPenalty           *float64 `json:"frequency_penalty,omitempty"`
	PresencePenalty            *float64 `json:"presence_penalty,omitempty"`
	LearningReviewThreshold    int      `json:"learning_review_threshold,omitempty"`
	SkillNudgeInterval         int      `json:"skill_nudge_interval,omitempty"`
	MaxAutoContinues           int      `json:"max_auto_continues,omitempty"`
	SoundNotifications         bool     `json:"sound_notifications"`
	UserPrompt                 string   `json:"user_prompt,omitempty"`
	PluginContractMode         string   `json:"plugin_contract_mode,omitempty"`
}

type SettingsGetResult struct {
	Settings SettingsDTO `json:"settings"`
}

type SettingsSetRequest struct {
	CompactionEnabled          *bool           `json:"compaction_enabled,omitempty"`
	CompactionThreshold        *int            `json:"compaction_threshold,omitempty"`
	CompactionModel            *string         `json:"compaction_model,omitempty"`
	CompactionSummaryMaxTokens *int            `json:"compaction_summary_max_tokens,omitempty"`
	CompactionSummaryMinChars  *int            `json:"compaction_summary_min_chars,omitempty"`
	PromptCaching              *bool           `json:"prompt_caching,omitempty"`
	MaxToolRounds              *int            `json:"max_tool_rounds,omitempty"`
	RepeatedToolLimit          *int            `json:"repeated_tool_limit,omitempty"`
	MaxInputTokens             *int            `json:"max_input_tokens,omitempty"`
	MaxOutputTokens            *int            `json:"max_output_tokens,omitempty"`
	MaxParallelTools           *int            `json:"max_parallel_tools,omitempty"`
	ReviewModel                *string         `json:"review_model,omitempty"`
	EmbeddingProviderID        *string         `json:"embedding_provider_id,omitempty"`
	EmbeddingModelID           *string         `json:"embedding_model_id,omitempty"`
	VisionProviderID           *string         `json:"vision_provider_id,omitempty"`
	VisionModelID              *string         `json:"vision_model_id,omitempty"`
	AudioProviderID            *string         `json:"audio_provider_id,omitempty"`
	AudioModelID               *string         `json:"audio_model_id,omitempty"`
	STTOfflineModel            *string         `json:"stt_offline_model,omitempty"`
	STTOfflineLanguage         *string         `json:"stt_offline_language,omitempty"`
	VideoProviderID            *string         `json:"video_provider_id,omitempty"`
	VideoModelID               *string         `json:"video_model_id,omitempty"`
	TTSProviderID              *string         `json:"tts_provider_id,omitempty"`
	TTSModelID                 *string         `json:"tts_model_id,omitempty"`
	TTSOfflineEnabled          *bool           `json:"tts_offline_enabled,omitempty"`
	ImageProviderID            *string         `json:"image_provider_id,omitempty"`
	ImageModelID               *string         `json:"image_model_id,omitempty"`
	WebAnswerProvider          *string         `json:"web_answer_provider,omitempty"`
	WebAnswerModel             *string         `json:"web_answer_model,omitempty"`
	WebAnswerAPIKey            *string         `json:"web_answer_api_key,omitempty"`
	Temperature                json.RawMessage `json:"temperature,omitempty"`
	TopP                       json.RawMessage `json:"top_p,omitempty"`
	TopK                       json.RawMessage `json:"top_k,omitempty"`
	FrequencyPenalty           json.RawMessage `json:"frequency_penalty,omitempty"`
	PresencePenalty            json.RawMessage `json:"presence_penalty,omitempty"`
	LearningReviewThreshold    *int            `json:"learning_review_threshold,omitempty"`
	SkillNudgeInterval         *int            `json:"skill_nudge_interval,omitempty"`
	MaxAutoContinues           *int            `json:"max_auto_continues,omitempty"`
	SoundNotifications         *bool           `json:"sound_notifications,omitempty"`
	UserPrompt                 *string         `json:"user_prompt,omitempty"`
	PluginContractMode         *string         `json:"plugin_contract_mode,omitempty"`
}

// ---- offline TTS install ----

// TTSVoiceDTO is one installable (or installed) offline voice.
type TTSVoiceDTO struct {
	ID        string `json:"id"`         // stable catalog id ("id_ID-news_tts-medium")
	Label     string `json:"label"`      // human-readable ("Bahasa Indonesia — news_tts (medium)")
	Language  string `json:"language"`   // BCP-47-ish ("id_ID")
	SizeBytes int64  `json:"size_bytes"` // onnx model size, for the dialog
	Installed bool   `json:"installed"`  // voice files present on disk
}

// TTSInstallProgressDTO rides the tts.install.* events.
type TTSInstallProgressDTO struct {
	VoiceID      string `json:"voice_id"`
	Phase        string `json:"phase"`                   // binary | voice | verify
	BytesFetched int64  `json:"bytes_fetched,omitempty"` // running counter within the phase
	BytesTotal   int64  `json:"bytes_total,omitempty"`   // 0 = unknown (indeterminate)
	Message      string `json:"message,omitempty"`       // short human-readable line
}

type TTSInstallStatusResult struct {
	BinaryInstalled bool          `json:"binary_installed"`
	Voices          []TTSVoiceDTO `json:"voices"`
	Running         bool          `json:"running"`
	// Ready mirrors the runtime gate used by the generate_speech tool:
	// a usable piper engine (managed, PATH, or PIPER_BIN) can serve AND at
	// least one voice is installed. When true, speech works immediately —
	// no settings flag involved.
	Ready bool `json:"ready"`
}

type TTSInstallStartRequest struct {
	VoiceID string `json:"voice_id"`
}

type TTSInstallStartResult struct {
	Started bool   `json:"started"`
	Running bool   `json:"running"` // true when an install is already in flight
	Message string `json:"message,omitempty"`
}

// OfflineTTSVoiceIDs mirrors the installer catalog for application-layer
// validation without importing infrastructure.
var OfflineTTSVoiceIDs = []string{"id_ID-news_tts-medium", "en_US-lessac-high"}

// OfflineSTTModelIDs mirrors the sttinstall catalog the same way.
var OfflineSTTModelIDs = []string{
	"ggml-tiny",
	"ggml-base",
	"ggml-small-q5_1",
	"ggml-small",
	"ggml-large-v3-turbo-q5_0",
	"ggml-large-v3-turbo",
}

// ---- learning ----

const (
	MethodLearningSearch           = "learning.search"
	MethodLearningGraph            = "learning.graph"
	MethodLearningLog              = "learning.log"
	MethodLearningReviewTranscript = "learning.review.transcript"
)

type LearningSearchRequest struct {
	Query string `json:"query"`
	Kind  string `json:"kind,omitempty"`  // "skills" | "memory" | "" (both)
	Limit int    `json:"limit,omitempty"` // default 10, max 50
}

type LearningSearchResultItem struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`           // "skill" | "memory"
	Tier    string  `json:"tier,omitempty"` // "primary" | "fragment" (memory only)
	Name    string  `json:"name,omitempty"`
	Content string  `json:"content,omitempty"`
	Score   float32 `json:"score"`
}

type LearningSearchResult struct {
	Items []LearningSearchResultItem `json:"items"`
}

// LearningGraphResult is the full graph payload for the frontend graph view.
type LearningGraphResult struct {
	Nodes []LearningGraphNode `json:"nodes"`
	Edges []LearningGraphEdge `json:"edges"`
}

type LearningGraphNode struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // "skill" | "memory"
	Name string `json:"name,omitempty"`
}

type LearningGraphEdge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Type   string  `json:"type"`   // "related" | "used_with" | "derived_from"
	Weight float64 `json:"weight"` // [0, 1]
}

// ---- learning log (autolearn trajectory) ----

type LearningLogRequest struct {
	Limit int `json:"limit,omitempty"` // default 100, max 500
}

type LearningLogMutationDTO struct {
	Kind    string `json:"kind"`              // "memory" | "skills"
	Tool    string `json:"tool,omitempty"`    // tool that produced the mutation
	Snippet string `json:"snippet,omitempty"` // trimmed content/name saved
}

type LearningLogEntryDTO struct {
	TS                string                     `json:"ts"`
	Type              string                     `json:"type"` // review|extract|edge_build|consolidate|decay|prune
	ConversationID    string                     `json:"conversation_id,omitempty"`
	ConversationTitle string                     `json:"conversation_title,omitempty"`
	ReviewID          string                     `json:"review_id,omitempty"`
	Status            string                     `json:"status,omitempty"` // done|error|skipped (review only; skipped reasons are in detail)
	Error             string                     `json:"error,omitempty"`  // failure message (review, status=error)
	Mutations         []LearningLogMutationDTO   `json:"mutations,omitempty"`
	Detail            map[string]json.RawMessage `json:"detail,omitempty"`
}

type LearningLogResult struct {
	Entries []LearningLogEntryDTO `json:"entries"`
}

// ---- review transcript ----

type LearningReviewTranscriptRequest struct {
	ID string `json:"id"`
}

type ToolResultDTO struct {
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content,omitempty"`
}

type LearningReviewTranscriptMessageDTO struct {
	Role       string         `json:"role"` // user | assistant | tool
	Content    string         `json:"content,omitempty"`
	Reasoning  string         `json:"reasoning,omitempty"`
	ToolCalls  []ToolCallDTO  `json:"tool_calls,omitempty"`
	ToolResult *ToolResultDTO `json:"tool_result,omitempty"`
}

type LearningReviewTranscriptResult struct {
	ID             string                               `json:"id"`
	ConversationID string                               `json:"conversation_id"`
	Model          string                               `json:"model"`
	CreatedAt      string                               `json:"created_at"`
	Messages       []LearningReviewTranscriptMessageDTO `json:"messages"`
}

// Settings watcher events: config/settings.json changed outside the app.
// Applied = valid JSON, normalized, and swapped in-memory (no write-back).
// Rejected = invalid content; the previous settings stay active.
const (
	EventSettingsApplied  = "settings.applied"
	EventSettingsRejected = "settings.rejected"
)

// SettingsAppliedEvent announces a successful external settings reload.
// RestartNeeded lists setting keys that need an app or UI reload to take
// full effect; empty means everything is live immediately.
type SettingsAppliedEvent struct {
	RestartNeeded []string `json:"restart_needed,omitempty"`
}

// SettingsRejectedEvent announces skipped external settings changes.
// Reason carries why (parse error, normalize panic guard, etc.).
type SettingsRejectedEvent struct {
	Reason string `json:"reason"`
}
