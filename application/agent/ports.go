package agent

import (
	"context"
	"time"

	"nusashell/application/provider"
	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/application/tools"
	"nusashell/contracts"
	"nusashell/domain"
)

// ConversationStore is the persistence adapter the turn loop reads and
// writes through conversation.Bind / ArchiveChunk.
type ConversationStore interface {
	List() []*domain.Conversation
	Get(id string) (*domain.Conversation, error)
	Save(c *domain.Conversation) error
	Delete(id string) error
	ArchiveChunk(id string, messages []domain.Message) (int, error)
	GetChunk(id string, index int) ([]domain.Message, error)
}

// ProviderStore lists configured chat providers for headless fallback.
type ProviderStore interface {
	List() []*domain.Provider
	Get(id string) (*domain.Provider, error)
}

// CredentialStore reads API keys for the resolved provider.
type CredentialStore interface {
	Get(providerID string) (string, bool, error)
}

// SettingsStore reads live settings for a turn.
type SettingsStore interface {
	Get() domain.Settings
}

// AttachmentStore persists uploaded turn attachments to disk.
type AttachmentStore interface {
	Save(conversationID string, att domain.Attachment) (string, error)
}

// TodoPort is the per-conversation checklist the turn loop and hydration read.
type TodoPort interface {
	Get(conversationID string) []domain.TodoItem
	GetBrief(conversationID string) string
	Set(conversationID string, items []domain.TodoItem)
	SetBrief(conversationID string, brief string)
	Clear(conversationID string)
	ClearBrief(conversationID string) error
	PlanPath(conversationID string) string
	Patch(conversationID string, items []domain.TodoItem)
}

// ConversationTodoPort is the hydration/todo name used by HydrationSource.
type ConversationTodoPort = TodoPort

// ProjectMemoryStore is the per-workspace project-memory surface hydration reads.
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

// MemoryRecordList lists typed memory records for the hydration apply-block.
type MemoryRecordList interface {
	List() []*domain.MemoryRecord
}

// DocumentPath is the filesystem path of user.md / soul.md.
type DocumentPath interface {
	Path() string
}

// PluginStore is the subset CapabilityRegistry needs to resolve MCP plugins.
type PluginStore interface {
	List() ([]*domain.Plugin, error)
	Get(id string) (*domain.Plugin, error)
}

// MCPToolbox is the subset CapabilityRegistry needs to list and start MCP tools.
type MCPToolbox interface {
	ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
	Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error)
}

// MCPToolCaller executes one MCP tool for a capability binding.
type MCPToolCaller interface {
	CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error)
}

// ProviderStateStore records explicit user disable of a capability provider.
type ProviderStateStore interface {
	Get(ctx context.Context, providerID string) (disabled bool, ok bool, err error)
	SetDisabled(ctx context.Context, providerID string, disabled bool) error
}

// WorkflowStore lists automation definitions so capability dependents can be found.
type WorkflowStore interface {
	List(ctx context.Context) ([]*domain.WorkflowDefinition, error)
}

// Emitter publishes turn, tool, and announcement events.
type Emitter interface {
	Emit(typ string, v any)
}

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// GoFunc runs fire-and-forget work. App wires this to goSafe.
type GoFunc func(name string, fn func())

// ResolveModel maps a model id to provider + API key. App implements this.
type ResolveModel func(model string) (p *domain.Provider, bare, apiKey string, rpcErr *contracts.RPCError)

// WaitRetry sleeps for a provider retry delay.
type WaitRetry func(ctx context.Context, delay time.Duration) error

// ChatMessagesForProvider hydrates attachment data URLs onto chat messages.
type ChatMessagesForProvider func(c *domain.Conversation, pendingMsgID string, caps ModelCapabilities) []ChatMessage

// EnrichConversation describes media attachments before a provider round.
type EnrichConversation func(ctx context.Context, conversation *domain.Conversation, pendingMsgID string, settings domain.Settings) *domain.Conversation

// MediaExec runs a generate_* / read_media tool. App implements these.
type MediaExec func(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error)

// GenerateMediaExec runs generate_media / generate_image / generate_speech / generate_video.
type GenerateMediaExec func(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error)

// LearningNodeIDs extracts memory/skill ids from a successful tool result.
type LearningNodeIDs func(toolCall domain.ToolCall, output string) []string

// RecordTurnPairs records used_with edges for ids observed in a turn.
type RecordTurnPairs func(allIDs, newIDs []string)

// AcknowledgeLearner validates a learn() tool payload.
type AcknowledgeLearner func(args string) (string, error)

// SkillCreatorReference returns the skill-creator SKILL.md path and body.
type SkillCreatorReference func() (path, content string)

// DelegateSnapshot looks up an in-flight internal delegate run.
type DelegateSnapshot func(runID string) (*domain.AcpRun, bool)

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Conversations    ConversationStore
	Providers        ProviderStore
	Credentials      CredentialStore
	Settings         SettingsStore
	Factory          provider.Factory
	Toolbox          tools.ToolExecutor
	Attachments      AttachmentStore
	Todos            TodoPort
	User             DocumentPath
	Agent            DocumentPath
	ProjectMemory    ProjectMemoryStore
	MemoryRecords    MemoryRecordList
	Bus              Emitter
	Log              Logger
	Go               GoFunc
	AskQuestions     *AskQuestionService
	RoundStreams     *RoundStreamRegistry
	LearnedParams    *learnedparams.Cache
	ModelOverrides   *modeloverrides.Cache
	Runs             map[string]*TurnRun
	PendingRuns      map[string]map[string]string
	DataDir          string
	DefaultWorkspace string
	StartedAt        time.Time

	ResolveModel            ResolveModel
	WaitRetry               WaitRetry
	WaitSlowDown            func(ctx context.Context)
	ProviderName            func(providerID string) string
	EffectiveWorkspace      func(workspace string) string
	ChatMessages            ChatMessagesForProvider
	EnrichVision            EnrichConversation
	EnrichAudio             EnrichConversation
	EnrichVideo             EnrichConversation
	ExecuteReadImage        MediaExec
	ExecuteReadAudio        MediaExec
	ExecuteReadVideo        MediaExec
	ExecuteReadDocument     MediaExec
	ExecuteGenerateMedia    GenerateMediaExec
	LearningNodeIDs         LearningNodeIDs
	RecordTurnPairs         RecordTurnPairs
	AcknowledgeLearner      AcknowledgeLearner
	SkillCreatorRef         SkillCreatorReference
	DelegateSnapshot        DelegateSnapshot
	DecorateRateLimit       func(providerID string, err error) error
	RecordExperience        func(conv *domain.Conversation, headless bool)
	MaybeAnnounceTaskMemory func(conversationID string, conversation *domain.Conversation)
}
