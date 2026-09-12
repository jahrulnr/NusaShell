package application

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"nusashell/application/agent"
	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/domain/turndiff"
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
	ErrRoundStreamNotFound = agent.ErrRoundStreamNotFound
	NewRoundStreamRegistry = agent.NewRoundStreamRegistry
	NewAskQuestionService  = agent.NewAskQuestionService
	NewCapabilityRegistry  = agent.NewCapabilityRegistry
	NewHydrationBuilder    = agent.NewHydrationBuilder
	DefaultRuntimeContext  = agent.DefaultRuntimeContext
)

const userNudgeText = domain.UserNudgeText

func chatMessages(c *domain.Conversation, pendingMsgID string, caps ModelCapabilities) []ChatMessage {
	return agent.ChatMessages(c, pendingMsgID, caps)
}
func isTPMDominatedRequest(err error) bool { return agent.IsTPMDominatedRequest(err) }
func tpmContextCap(limit, maxOutput int) int {
	return agent.TPMContextCap(limit, maxOutput)
}
func extractErrBody(err error) string { return agent.ExtractErrBody(err) }
func isLearnable400(err error) bool   { return agent.IsLearnable400(err) }
func buildSystemPrompt(c *domain.Conversation, userPrompt string) string {
	return agent.BuildSystemPrompt(c, userPrompt)
}
func buildSystemPromptForRun(run *TurnRun, c *domain.Conversation, userPrompt string) string {
	return agent.BuildSystemPromptForRun(run, c, userPrompt)
}
func newAnnouncement(typ, args, message string) Announcement {
	return agent.NewAnnouncement(typ, args, message)
}
func newSteerEntry(text string, attachments []domain.Attachment) *SteerEntry {
	return agent.NewSteerEntry(text, attachments)
}
func shouldAnnounceRestart(c *domain.Conversation, startedAt time.Time) bool {
	return agent.ShouldAnnounceRestart(c, startedAt)
}
func modelCapabilitiesWithLearned(provider *domain.Provider, model string, cache *learnedparams.Cache, manual *modeloverrides.Cache) ModelCapabilities {
	return agent.ModelCapabilitiesWithLearned(provider, model, cache, manual)
}
func applyStreamRound(message *domain.Message, model string, round streamedTurnRound) {
	agent.ApplyStreamRound(message, model, round)
}
func askPendingEvent(conversationID, runID, callID string, req domain.AskQuestionRequest) contracts.AskPendingEvent {
	return agent.AskPendingEvent(conversationID, runID, callID, req)
}

func estimateRequestTokens(system string, messages []ChatMessage, tools []ToolDef) int64 {
	return agent.EstimateRequestTokens(system, messages, tools)
}

func promptCachePrefixForRun(run *TurnRun) string {
	return agent.PromptCachePrefixForRun(run)
}

func truncateToolError(msg string) string {
	return agent.TruncateToolError(msg)
}

func hasUserMessage(messages []ChatMessage) bool {
	return agent.HasUserMessage(messages)
}

func needsUserMessageAtEnd(messages []ChatMessage) bool {
	return agent.NeedsUserMessageAtEnd(messages)
}

func appendContinuationTool(messages []ChatMessage) []ChatMessage {
	return agent.AppendContinuationTool(messages)
}

func lastFailedAssistantIndex(msgs []domain.Message) int {
	return agent.LastFailedAssistantIndex(msgs)
}

func toolRoundSignature(calls []domain.ToolCall) string {
	return agent.ToolRoundSignature(calls)
}

func compactionPassAvailable(contextWindow int, runningSummary string, summaryMaxOut int) int {
	return agent.CompactionPassAvailable(contextWindow, runningSummary, summaryMaxOut)
}

func extractCompactionSummary(resp ChatResponse) string {
	return agent.ExtractCompactionSummary(resp)
}

func appendCompactionHandoffUser(msgs []ChatMessage) []ChatMessage {
	return agent.AppendCompactionHandoffUser(msgs)
}

func compactionToolChoice(kind domain.ProviderKind) any {
	return agent.CompactionToolChoice(kind)
}

func compactionSummaryEchoesAssistant(summary string, msgs []ChatMessage) bool {
	return agent.CompactionSummaryEchoesAssistant(summary, msgs)
}

func reasoningDeltaVisible(accumulated string) bool {
	return agent.ReasoningDeltaVisible(accumulated)
}

func filterToolAttachmentsByCaps(atts []domain.Attachment, content string, caps ModelCapabilities) ([]domain.Attachment, string) {
	return agent.FilterToolAttachmentsByCaps(atts, content, caps)
}

func mergeUsage(a, b ChatUsage) ChatUsage {
	return agent.MergeUsage(a, b)
}

func finalHeadlessAssistantMessage(messages []domain.Message, finalMessageID string) (domain.Message, bool) {
	return agent.FinalHeadlessAssistantMessage(messages, finalMessageID)
}

func headlessWorkspace(ctxWorkspace string, kind AgentKind, fallbackDataDir string) string {
	return agent.HeadlessWorkspace(ctxWorkspace, kind, fallbackDataDir)
}

func headlessConversationType(kind AgentKind) domain.ConversationType {
	return agent.HeadlessConversationType(kind)
}

func headlessTurnTitle(kind AgentKind, prompt string) string {
	return agent.HeadlessTurnTitle(kind, prompt)
}

func serverCompactionContextManagement(model string) []map[string]any {
	return agent.ServerCompactionContextManagement(model)
}

func serverCompactionContextManagementForKind(model string, kind domain.ProviderKind) []map[string]any {
	return agent.ServerCompactionContextManagementForKind(model, kind)
}

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
		Conversations:        a.Conversations,
		Providers:            a.Providers,
		Credentials:          a.Credentials,
		Settings:             a.Settings,
		Factory:              a.Factory,
		Toolbox:              a.Toolbox,
		Attachments:          a.Attachments,
		Todos:                a.Todos,
		User:                 a.User,
		Agent:                a.Agent,
		ProjectMemory:        a.ProjectMemory,
		MemoryRecords:        a.MemoryRecords,
		Bus:                  a.Bus,
		Log:                  a.log,
		Go:                   func(name string, fn func()) { a.goSafe(name, fn) },
		AskQuestions:         a.AskQuestions,
		RoundStreams:         a.RoundStreams,
		LearnedParams:        a.learnedParams,
		ModelOverrides:       a.modelOverrides,
		Runs:                 a.runs,
		PendingRuns:          a.pendingRuns,
		DataDir:              a.DataDir,
		DefaultWorkspace:     a.defaultWorkspace,
		StartedAt:            a.startedAt,
		ResolveModel:         a.resolveModel,
		WaitRetry:            a.waitForRetry,
		WaitSlowDown:         a.waitSlowDown,
		ProviderName:         a.providerNameByID,
		EffectiveWorkspace:   a.effectiveWorkspace,
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
		AcknowledgeLearner: acknowledgeLearnerResult,
		SkillCreatorRef: func() (string, string) {
			return a.learnService().LearnerSkillCreatorReference()
		},
		DelegateSnapshot:        a.delegateRunSnapshot,
		DecorateRateLimit:       a.decorateRateLimitError,
		RecordExperience:        a.recordExperience,
		MaybeAnnounceTaskMemory: a.maybeAnnounceTaskMemory,
		PrepareTurnAPIKey:       a.prepareCodexTurnAPIKey,
		FailoverOnStreamError:   a.failoverCodexOnStreamError,
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

func (a *App) runTurn(run *TurnRun, provider *domain.Provider, apiKey, model, effort, asstMsgID string, initialContinuation bool, caps ModelCapabilities) {
	a.agentService().RunTurn(run, provider, apiKey, model, effort, asstMsgID, initialContinuation, caps)
}
func (a *App) persistHydration(c *domain.Conversation, msgs []ChatMessage) *domain.Conversation {
	return a.agentService().PersistHydration(c, msgs)
}
func (a *App) buildHydration(c *domain.Conversation) []ChatMessage {
	return a.agentService().BuildHydration(c)
}
func (a *App) executeTurnTools(run *TurnRun, messageID string, toolCalls []domain.ToolCall, caps ModelCapabilities, settings domain.Settings, round int) error {
	return a.agentService().ExecuteTurnTools(run, messageID, toolCalls, caps, settings, round)
}
func (a *App) runOneTool(run *TurnRun, messageID string, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings, round int) toolExecResult {
	return a.agentService().RunOneTool(run, messageID, toolCall, caps, settings, round)
}
func (a *App) compactConversation(ctx context.Context, adapter ProviderContext, c *domain.Conversation, model string, contextWindow int, settings domain.Settings, trigger domain.CompactionTrigger) (string, error) {
	return a.agentService().CompactConversation(ctx, adapter, c, model, contextWindow, settings, trigger)
}
func (a *App) compactConversationWithCache(ctx context.Context, adapter ProviderContext, c *domain.Conversation, model string, contextWindow int, settings domain.Settings, trigger domain.CompactionTrigger, promptCache *PromptCachePolicy) (string, error) {
	return a.agentService().CompactConversationWithCache(ctx, adapter, c, model, contextWindow, settings, trigger, promptCache)
}
func (a *App) publishAnnouncement(convID string, ev Announcement) {
	a.agentService().PublishAnnouncement(convID, ev)
}
func (a *App) publishAnnouncementToAll(ev Announcement, skipConvID string) {
	a.agentService().PublishAnnouncementToAll(ev, skipConvID)
}
func (a *App) drainAnnouncements(run *TurnRun) (bool, error) {
	return a.agentService().DrainAnnouncements(run)
}
func (a *App) RunHeadlessTurn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, conversationID string, onUpdate func(conversationID string)) (map[string]any, string, error) {
	return a.agentService().RunHeadlessTurnKindObserved(ctx, prompt, model, trust, schema, AgentAutomation, conversationID, onUpdate)
}
func (a *App) RunHeadlessTurnIn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, conversationID string) (map[string]any, string, error) {
	return a.agentService().RunHeadlessTurnIn(ctx, prompt, model, trust, schema, conversationID)
}
func (a *App) runHeadlessTurnKind(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, kind AgentKind) (map[string]any, string, error) {
	return a.agentService().RunHeadlessTurnKind(ctx, prompt, model, trust, schema, kind)
}
func (a *App) runHeadlessTurnKindObserved(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, kind AgentKind, onUpdate func(conversationID string)) (map[string]any, string, error) {
	return a.agentService().RunHeadlessTurnKindObserved(ctx, prompt, model, trust, schema, kind, "", onUpdate)
}
func (a *App) SteerHeadlessTurn(conversationID, text string) error {
	return a.agentService().SteerHeadlessTurn(conversationID, text)
}
func (a *App) activeRunForConversation(convID string) *TurnRun {
	return a.agentService().ActiveRunForConversation(convID)
}
func (a *App) activeRunForConversationLocked(convID string) *TurnRun {
	return a.agentService().ActiveRunForConversationLocked(convID)
}
func (a *App) deliverRunDone(conversationID string, pending pendingRunDone) {
	a.agentService().DeliverRunDone(conversationID, pending)
}
func (a *App) completeSubagentRunLocked(conversationID, toolCallID string, status domain.ToolCallStatus, run *domain.AcpRun, outputPath string) error {
	return a.agentService().CompleteSubagentRunLocked(conversationID, toolCallID, status, run, outputPath)
}
func (a *App) triggerBackgroundCompletionTurn(conversationID string) {
	a.agentService().TriggerBackgroundCompletionTurn(conversationID)
}
func (a *App) resolveConversationProvider(conv *domain.Conversation) (*domain.Provider, string, string, string, error) {
	return a.agentService().ResolveConversationProvider(conv)
}
func (a *App) handleTurnsStart(ctx context.Context, req contracts.TurnStartRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleTurnsStart(ctx, req)
}
func (a *App) handleTurnsRetry(ctx context.Context, req contracts.TurnRetryRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleTurnsRetry(ctx, req)
}
func (a *App) handleTurnsStop(req contracts.TurnStopRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleTurnsStop(req)
}
func (a *App) handleToolStop(req contracts.ToolStopRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleToolStop(req)
}
func (a *App) handleTurnsActive(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleTurnsActive(req)
}
func (a *App) handleAskPendingList(req contracts.AskPendingListRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleAskPendingList(req)
}
func (a *App) handleAskAnswer(req contracts.AskAnswerRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleAskAnswer(req)
}
func (a *App) handleAskCancel(req contracts.AskCancelRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleAskCancel(req)
}
func (a *App) handleTurnsSteer(ctx context.Context, req contracts.TurnSteerRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleTurnsSteer(ctx, req)
}
func (a *App) handleTurnsCancelSteer(req contracts.TurnCancelSteerRequest) (any, *contracts.RPCError) {
	return a.agentService().HandleTurnsCancelSteer(req)
}
func (a *App) addTurnMessages(c *domain.Conversation, userMsg, asstMsg domain.Message) {
	a.agentService().AddTurnMessages(c, userMsg, asstMsg)
}
func (a *App) takeWorkspaceSwitchNotice(c *domain.Conversation) *domain.Message {
	return a.agentService().TakeWorkspaceSwitchNotice(c)
}
func (a *App) conversationTurnLock(conversationID string) *sync.Mutex {
	return a.agentService().ConversationTurnLock(conversationID)
}
func (a *App) trackPendingRun(conversationID, runID, tool string) {
	a.agentService().TrackPendingRun(conversationID, runID, tool)
}
func (a *App) untrackPendingRun(conversationID, runID string) bool {
	return a.agentService().UntrackPendingRun(conversationID, runID)
}
func (a *App) hasPendingRuns(conversationID string) bool {
	return a.agentService().HasPendingRuns(conversationID)
}
func (a *App) pendingBackgroundRuns(conversationID string) []domain.BackgroundRunInfo {
	return a.agentService().PendingBackgroundRuns(conversationID)
}
func (a *App) learnTPMContextCap(run *TurnRun, model string, err error, maxOutput int) bool {
	return a.agentService().LearnTPMContextCap(run, model, err, maxOutput)
}
func (a *App) turnToolDefs(run *TurnRun) []ToolDef {
	return a.agentService().TurnToolDefs(run)
}
func (a *App) recordLearningTurnNodes(run *TurnRun, ids []string) {
	a.agentService().RecordLearningTurnNodes(run, ids)
}
func (a *App) interruptTurn(run *TurnRun, msgID string, round streamedTurnRound, usage ChatUsage, contextTokens int, model string) {
	a.agentService().InterruptTurn(run, msgID, round, usage, contextTokens, model)
}
func (a *App) newConversationRules(run *TurnRun, adapter ProviderContext, conversation *domain.Conversation, settings domain.Settings, provider *domain.Provider, model, effort, asstMsgID string, caps ModelCapabilities, toolDefs []ToolDef, maxTokens int, promptCache *PromptCachePolicy, initialContinuation bool) *conversationRules {
	return a.agentService().NewConversationRules(run, adapter, conversation, settings, provider, model, effort, asstMsgID, caps, toolDefs, maxTokens, promptCache, initialContinuation, "")
}
func (a *App) conversationRulesForTest(run *TurnRun, adapter ProviderContext, conv *domain.Conversation, settings domain.Settings, provider *domain.Provider, model, currentMsgID string, round int) *conversationRules {
	return a.agentService().ConversationRulesForTest(run, adapter, conv, settings, provider, model, currentMsgID, round)
}
func (a *App) updateToolResult(c *domain.Conversation, msgID, callID string, status domain.ToolCallStatus, output string, outputAttachments []domain.Attachment) *domain.Conversation {
	return a.agentService().UpdateToolResult(c, msgID, callID, status, output, outputAttachments)
}
func (a *App) saveAttachmentsToDisk(conversationID string, attachments []domain.Attachment) {
	a.agentService().SaveAttachmentsToDisk(conversationID, attachments)
}
func (a *App) resolveHeadlessModel(modelID string) (*domain.Provider, string, string, error) {
	return a.agentService().ResolveHeadlessModel(modelID)
}

func (a *App) trackTurnDiff(run *TurnRun, delta turndiff.Delta) {
	a.agentService().TrackTurnDiff(run, delta)
}
func (a *App) emitFinalTurnDiff(run *TurnRun) {
	a.agentService().EmitFinalTurnDiff(run)
}
func (a *App) failTurn(run *TurnRun, msgID string, err error) {
	a.agentService().FailTurn(run, msgID, err)
}
func (a *App) healOrphanedRunningConversation(c *domain.Conversation) bool {
	return a.agentService().HealOrphanedRunningConversation(c)
}
func (a *App) emitInteractiveTurnEvent(run *TurnRun, typ string, payload any) {
	a.agentService().EmitInteractiveTurnEvent(run, typ, payload)
}
func (a *App) completeWithRetry(ctx context.Context, adapter ProviderContext, request ChatRequest) (ChatResponse, error) {
	return a.agentService().CompleteWithRetry(ctx, adapter, request)
}
func (a *App) updateMessage(c *domain.Conversation, msgID string, fn func(*domain.Message)) {
	a.agentService().UpdateMessage(c, msgID, fn)
}
func (a *App) applyQueuedRunResults(run *TurnRun) (bool, error) {
	return a.agentService().ApplyQueuedRunResults(run)
}
func (a *App) applyQueuedSteer(run *TurnRun) (bool, error) {
	return a.agentService().ApplyQueuedSteer(run)
}
func (a *App) streamTurnRound(run *TurnRun, adapter ProviderContext, conversation *domain.Conversation, messageID, model, effort string, tools []ToolDef, settings domain.Settings, continuation bool, maxTokens int, promptCache *PromptCachePolicy, caps ModelCapabilities, round int) (StreamedTurnRound, error) {
	return a.agentService().StreamTurnRound(run, adapter, conversation, messageID, model, effort, tools, settings, continuation, maxTokens, promptCache, caps, round)
}
func (a *App) streamTurnRoundOnce(run *TurnRun, adapter ProviderContext, conversation *domain.Conversation, messageID, model, effort string, tools []ToolDef, settings domain.Settings, continuation bool, partial *StreamedTurnRound, maxTokens int, promptCache *PromptCachePolicy, caps ModelCapabilities, round int) (StreamedTurnRound, error) {
	return a.agentService().StreamTurnRoundOnce(run, adapter, conversation, messageID, model, effort, tools, settings, continuation, partial, maxTokens, promptCache, caps, round)
}
func (a *App) persistCompactedConversation(c *domain.Conversation, summary string, keepBudget int) error {
	return a.agentService().PersistCompactedConversation(c, summary, keepBudget)
}
func (a *App) resolveCompactionAdapter(ctx context.Context, defaultAdapter ProviderContext, defaultModel string, defaultWindow int, settings domain.Settings) (ProviderContext, string, int) {
	return a.agentService().ResolveCompactionAdapter(ctx, defaultAdapter, defaultModel, defaultWindow, settings)
}
func (a *App) resolveContextWindow(provider *domain.Provider, model string, settings domain.Settings) int {
	return a.agentService().ResolveContextWindow(provider, model, settings)
}
func (a *App) recoverOrphanedTurn(run *TurnRun) {
	a.agentService().RecoverOrphanedTurn(run)
}
func (a *App) restartAnnouncement() domain.Message {
	return a.agentService().RestartAnnouncement()
}
func (a *App) autoContinueAnnouncement(decision domain.AutoContinueDecision) domain.Message {
	return a.agentService().AutoContinueAnnouncement(decision)
}
func (a *App) subagentResultMessage(run *domain.AcpRun, outputPath string, status domain.ToolCallStatus) domain.Message {
	return a.agentService().SubagentResultMessage(run, outputPath, status)
}
