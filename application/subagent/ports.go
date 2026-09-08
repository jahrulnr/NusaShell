package subagent

import (
	"context"

	"nusashell/domain"
)

// AgentStore persists ACP agent registrations. jsonstore.AcpAgents implements this.
type AgentStore interface {
	List() []*domain.AcpAgent
	Get(id string) (*domain.AcpAgent, error)
	Save(a *domain.AcpAgent) error
	Delete(id string) error
}

// SpawnRequest is the input to Runtime.Spawn.
type SpawnRequest struct {
	Agent            *domain.AcpAgent
	ConversationID   string
	ParentToolCallID string
	Prompt           string
	Title            string
	Workspace        string
	ModeID           string
	ModelID          string
}

// PermissionDecision is reserved for permission-policy adapters.
type PermissionDecision struct {
	OptionID string
	Outcome  domain.PermissionOutcome
}

// Runtime is the ACP process/session surface. infrastructure/acpruntime implements this.
type Runtime interface {
	Probe(ctx context.Context, agent *domain.AcpAgent) (domain.AcpAgent, error)
	Authenticate(ctx context.Context, agent *domain.AcpAgent, methodID string) error
	RefreshCatalog(ctx context.Context, agent *domain.AcpAgent) (domain.AcpAgent, error)
	Spawn(ctx context.Context, req SpawnRequest) (*domain.AcpRun, error)
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

// ConversationLookup reads a conversation for workspace/model inheritance.
type ConversationLookup interface {
	Get(id string) (*domain.Conversation, error)
}

// TodoBriefs supplies the parent plan file and brief for ACP spawn handoff.
type TodoBriefs interface {
	GetBrief(conversationID string) string
	PlanPath(conversationID string) string
}

// Settings reads the configured internal-delegate model.
type Settings interface {
	Get() domain.Settings
}

// Emitter publishes ACP run and permission events. Root *Bus assigns.
type Emitter interface {
	Emit(typ string, v any)
}

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// GoFunc runs fire-and-forget work. App wires this to goSafe.
type GoFunc func(name string, fn func())

// TrackPending records a background run on the parent conversation.
type TrackPending func(conversationID, runID, tool string)

// DeliverRunDone queues or injects a finished background run. App implements
// this with deliverRunDone + pendingRunDone because that path needs TurnRun.
type DeliverRunDone func(conversationID, runID string, complete func(cid string) error)

// CompleteSubagent mutates the parent transcript under the turn lock.
type CompleteSubagent func(conversationID, toolCallID string, status domain.ToolCallStatus, run *domain.AcpRun, outputPath string) error

// CompleteDelegate mutates the parent transcript under the turn lock.
type CompleteDelegate func(conversationID, runID, toolCallID string, status domain.ToolCallStatus, output, runConvID string) error

// ResolveModel falls back to headless model resolution when no delegate
// model is configured and the parent conversation has none.
type ResolveModel func(parentConvID string) (string, error)

// ObservedHeadlessTurn runs one unattended agent step and reports the hidden
// conversation id as it is created. App wires AgentDelegate via
// runHeadlessTurnKindObserved. Duplicated so this package does not import
// tools or automation.
type ObservedHeadlessTurn func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(conversationID string)) (map[string]any, string, error)

// OnAgentsChanged invalidates cached subagent tool descriptions in every room.
type OnAgentsChanged func()

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Agents           AgentStore
	Runtime          Runtime
	RunStorage       domain.AcpRunStorage
	Conversations    ConversationLookup
	Todos            TodoBriefs
	Settings         Settings
	Log              Logger
	Bus              Emitter
	Go               GoFunc
	OnAgentsChanged  OnAgentsChanged
	TrackPending     TrackPending
	DeliverRunDone   DeliverRunDone
	CompleteSubagent CompleteSubagent
	CompleteDelegate CompleteDelegate
	ResolveModel     ResolveModel
	Headless         ObservedHeadlessTurn
}
