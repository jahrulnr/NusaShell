package application

import (
	"context"
	"encoding/json"
	"strings"

	"nusashell/application/agent"
	"nusashell/contracts"
	"nusashell/domain"
)

type (
	TurnRun                = agent.TurnRun
	SteerEntry             = agent.SteerEntry
	pendingRunDone         = agent.PendingRunDone
	AgentEngine            = agent.AgentEngine
	AgentRules             = agent.AgentRules
	RoundState             = agent.RoundState
	ToolOutcome            = agent.ToolOutcome
	CapabilityRegistry     = agent.CapabilityRegistry
	BuiltinCapability      = agent.BuiltinCapability
	AskQuestionService     = agent.AskQuestionService
	PendingAsk             = agent.PendingAsk
	RoundStreamRegistry    = agent.RoundStreamRegistry
	RoundStream            = agent.RoundStream
	RoundStreamSub         = agent.RoundStreamSub
	ModelCapabilities      = agent.ModelCapabilities
	Announcement           = agent.Announcement
	HydrationSource        = agent.HydrationSource
	HydrationBuilder       = agent.HydrationBuilder
	HydrationResult        = agent.HydrationResult
	RuntimeContextSnapshot = agent.RuntimeContextSnapshot
	StreamedTurnRound      = agent.StreamedTurnRound
	streamedTurnRound      = agent.StreamedTurnRound
	ToolExecResult         = agent.ToolExecResult
	toolExecResult         = agent.ToolExecResult
	conversationRules      = agent.ConversationRules
	repeatedToolGuard      = agent.RepeatedToolGuard
	AgentLifecycleEvent    = agent.AgentLifecycleEvent
	AgentObserver          = agent.AgentObserver
	AgentObserverFunc      = agent.AgentObserverFunc
)

var (
	ErrRoundStreamNotFound     = agent.ErrRoundStreamNotFound
	NewRoundStreamRegistry     = agent.NewRoundStreamRegistry
	NewAskQuestionService      = agent.NewAskQuestionService
	NewCapabilityRegistry      = agent.NewCapabilityRegistry
	NewHydrationBuilder        = agent.NewHydrationBuilder
	DefaultRuntimeContext      = agent.DefaultRuntimeContext
	ConversationWakeCapability = agent.ConversationWakeCapability
)

const userNudgeText = domain.UserNudgeText

func AcpDelegationDescription(agents []*domain.AcpAgent) string {
	return agent.AcpDelegationDescription(agents)
}

const (
	promptCacheConversationPrefix = agent.PromptCacheConversationPrefix
	promptCacheBackgroundPrefix   = agent.PromptCacheBackgroundPrefix
	maxToolErrorLen               = agent.MaxToolErrorLen
	compactionSummaryMaxOut       = agent.CompactionSummaryMaxOut
	compactionSummaryMinChars     = agent.CompactionSummaryMinChars
	compactionSummaryMaxRetries   = agent.CompactionSummaryMaxRetries
	compactionSystemReserve       = agent.CompactionSystemReserve
	maxPendingAnnouncements       = agent.MaxPendingAnnouncements
	hydrationFileListMaxBytes     = agent.HydrationFileListMaxBytes
	roundStreamFrameCap           = agent.RoundStreamFrameCap
	roundStreamSubBuf             = agent.RoundStreamSubBuf
)

func (a *App) agentService() *agent.Service {
	if a == nil {
		return agent.New(agent.Deps{})
	}
	a.agentMu.Lock()
	defer a.agentMu.Unlock()
	if a.agentSvc != nil {
		return a.agentSvc
	}
	a.agentSvc = agent.New(a.agentDeps())
	return a.agentSvc
}

// RegisterAgentObserver registers a headless automation lifecycle observer
// on the shared agent service (FIFO, side-effect only).
func (a *App) RegisterAgentObserver(o AgentObserver) {
	a.agentService().RegisterAgentObserver(o)
}

func (a *App) agentDeps() agent.Deps {
	if a.runs == nil {
		a.runs = map[string]*TurnRun{}
	}
	if a.pendingRuns == nil {
		a.pendingRuns = map[string]map[string]string{}
	}
	return agent.Deps{
		Conversations:      a.Conversations,
		Providers:          a.Providers,
		Credentials:        a.Credentials,
		Settings:           a.Settings,
		Factory:            a.Factory,
		Toolbox:            a.Toolbox,
		Attachments:        a.Attachments,
		Todos:              a.Todos,
		User:               a.User,
		Agent:              a.Agent,
		ProjectMemory:      a.ProjectMemory,
		MemoryRecords:      a.MemoryRecords,
		Bus:                a.Bus,
		Log:                a.log,
		Go:                 func(name string, fn func()) { a.goSafe(name, fn) },
		AskQuestions:       a.AskQuestions,
		RoundStreams:       a.RoundStreams,
		LearnedParams:      a.learnedParams,
		ModelOverrides:     a.modelOverrides,
		Runs:               a.runs,
		PendingRuns:        a.pendingRuns,
		DataDir:            a.DataDir,
		DefaultWorkspace:   a.defaultWorkspace,
		GlobalAgentsMDPath: a.globalAgentsMDPath,
		StartedAt:          a.startedAt,
		TurnPatches:        a.TurnPatches,
		ResolveModel:       a.resolveModel,
		WaitRetry:          a.waitForRetry,
		WaitSlowDown:       a.waitSlowDown,
		ProviderName:       a.providerNameByID,
		ResolveSkillName:   a.skillName,
		EffectiveWorkspace: a.effectiveWorkspace,
		ValidateWorkspace: func(ctx context.Context, workspace string) error {
			if a.DirectoryBrowser == nil {
				return nil
			}
			return a.DirectoryBrowser.EnsureDir(ctx, workspace)
		},
		ChatMessages:         a.chatMessagesForProvider,
		EnrichVision:         a.enrichWithVisionDescriptions,
		EnrichAudio:          a.enrichWithAudioDescriptions,
		EnrichVideo:          a.enrichWithVideoDescriptions,
		ExecuteReadImage:     a.executeReadImage,
		ExecuteReadVideo:     a.executeReadVideo,
		ExecuteReadAudio:     a.executeReadAudio,
		ExecuteReadDocument:  a.executeReadDocument,
		ExecuteGenerateMedia: a.executeGenerateMedia,
		LearningNodeIDs: func(toolCall domain.ToolCall, output string) []string {
			return learningNodeIDsFromTool(a, toolCall, output)
		},
		RecordTurnPairs: func(allIDs, newIDs []string) {
			a.learnService().RecordTurnPairs(allIDs, newIDs)
		},
		AcknowledgeLearner:         acknowledgeLearnerResult,
		DelegateSnapshot:           a.delegateRunSnapshot,
		DecorateRateLimit:          a.decorateRateLimitError,
		RecordExperience:           a.recordExperience,
		MaybeAnnounceTaskMemory:    a.maybeAnnounceTaskMemory,
		PrefetchTaskMemorySemantic: a.prefetchTaskMemorySemantic,
		PrepareTurnAPIKey:          a.prepareCodexTurnAPIKey,
		FailoverOnStreamError:      a.failoverCodexOnStreamError,
	}
}

func (a *App) dispatchAgent(ctx context.Context, method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch {
	case strings.HasPrefix(method, "agent.conversations."),
		method == contracts.MethodWorkspaceListDirs,
		strings.HasPrefix(method, "agent.todos."):
		return a.conversationService().Dispatch(method, payload)
	case method == contracts.MethodToolContracts:
		var req contracts.ToolContractsRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleToolContracts(req)
	}
	return a.agentService().Dispatch(ctx, method, payload)
}

func (a *App) RunHeadlessTurn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, conversationID string, onUpdate func(conversationID string)) (map[string]any, string, error) {
	return a.agentService().RunHeadlessTurnKindObserved(ctx, prompt, model, trust, schema, AgentAutomation, conversationID, onUpdate, nil)
}
func (a *App) SteerHeadlessTurn(conversationID, text string) error {
	return a.agentService().SteerHeadlessTurn(conversationID, text)
}

// DeliverAutomationWake queues an automation_event announcement for one
// conversation and wakes the room when it is idle. Wired into the
// `conversation.wake` builtin capability at the composition root.
func (a *App) DeliverAutomationWake(targetID, source, content string) error {
	return a.agentService().DeliverAutomationWake(targetID, source, content)
}

// completeSubagentRunLocked stays an App method: tests and the ACP ports wire
// it as a function value (CompleteSubagent).
func (a *App) completeSubagentRunLocked(conversationID, toolCallID string, status domain.ToolCallStatus, run *domain.AcpRun, outputPath string) error {
	return a.agentService().CompleteSubagentRunLocked(conversationID, toolCallID, status, run, outputPath)
}

// trackPendingRun stays an App method: it is wired as a function value into
// subagent completion ports (TrackPending).
func (a *App) trackPendingRun(conversationID, runID, tool string) {
	a.agentService().TrackPendingRun(conversationID, runID, tool)
}
