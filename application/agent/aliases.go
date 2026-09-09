package agent

import (
	"context"
	"time"

	"nusashell/application/conversation"
	"nusashell/application/provider"
	"nusashell/application/tools"
	"nusashell/contracts"
	"nusashell/domain"
)

type (
	ChatRequest            = provider.ChatRequest
	ChatResponse           = provider.ChatResponse
	ChatMessage            = provider.ChatMessage
	ChatUsage              = provider.ChatUsage
	ToolDef                = provider.ToolDef
	ToolResult             = provider.ToolResult
	ProviderContext        = provider.Context
	PromptCachePolicy      = provider.PromptCachePolicy
	AIProvider             = provider.AIProvider
	AgentKind              = tools.AgentKind
	ToolExecutor           = tools.ToolExecutor
	ToolInfo               = tools.ToolInfo
	ConversationRepository = conversation.Repository
)

const (
	AgentConversation         = tools.AgentConversation
	AgentAutomation           = tools.AgentAutomation
	AgentCompaction           = tools.AgentCompaction
	AgentDelegate             = tools.AgentDelegate
	AgentLearner              = tools.AgentLearner
	AgentMemoryConsolidator   = tools.AgentMemoryConsolidator
	AgentSkillEvolver         = tools.AgentSkillEvolver
	AgentSkillEvaluator       = tools.AgentSkillEvaluator
	learnerResultToolName     = tools.LearnerResultToolName
	compactionSummaryToolName = tools.CompactionSummaryToolName
	maxProviderAttempts       = domain.MaxProviderAttempts
	retryBaseDelay            = domain.RetryBaseDelay
	timeRFC3339               = "2006-01-02T15:04:05Z07:00"
)

var (
	NewProviderContext       = provider.NewProviderContext
	isRetryableProviderError = provider.IsRetryableError
	providerRetryDelay       = provider.RetryDelay
	describeProviderError    = provider.DescribeError
	isContextOverflowError   = provider.IsContextOverflow
	contextLimitFromError    = provider.ContextLimit
	shouldEmergencyCompact   = provider.ShouldEmergencyCompact
	isPrematureStreamEnd     = provider.IsPrematureStreamEnd
	buildPromptCachePolicy   = provider.BuildPromptCachePolicy
	// Same functions application/context.go wraps; do not redeclare ctxKey.
	WithConversationID   = tools.WithConversationID
	WithWorkspace        = tools.WithWorkspace
	WithRunID            = tools.WithRunID
	WithToolCallID       = tools.WithToolCallID
	WithProviderID       = tools.WithProviderID
	WithModel            = tools.WithModel
	WorkspaceFromContext = tools.WorkspaceFromContext
)

func isLearnerKind(kind AgentKind) bool {
	return tools.IsLearnerKind(kind)
}

func bindConversation(store ConversationStore, c *domain.Conversation) *conversation.Repository {
	if store == nil || c == nil {
		return nil
	}
	return conversation.Bind(store, c)
}

func NewConversation(store ConversationStore, title string) *conversation.Repository {
	return conversation.NewConversation(store, title)
}

func listInstructionFiles(workspace string) []string {
	return conversation.ListInstructionFiles(workspace)
}

func toToolDef(t tools.ToolInfo) ToolDef {
	return ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
}

func toToolDefs(infos []tools.ToolInfo) []ToolDef {
	if infos == nil {
		return nil
	}
	out := make([]ToolDef, len(infos))
	for i, t := range infos {
		out[i] = toToolDef(t)
	}
	return out
}

type (
	streamedTurnRound = StreamedTurnRound
	toolExecResult    = ToolExecResult
)

func cloneConversation(c *domain.Conversation) *domain.Conversation {
	return conversation.Clone(c)
}

func OpArg(argsJSON []byte) string {
	return tools.OpArg(argsJSON)
}

func rpcInternal(err error) *contracts.RPCError {
	return &contracts.RPCError{Code: contracts.CodeInternal, Message: err.Error()}
}

func (a *Service) applyModelOverrides(p *domain.Provider, m *domain.Model) {
	if m == nil {
		return
	}
	if a.learnedParams != nil && a.learnedParams.OverrideModel(m, p.ID, m.ID) {
		a.log("info", "learning", "applied learned overrides to %s/%s (context=%d vision=%v)", p.ID, m.ID, m.Context, m.Vision)
	}
	if a.modelOverrides != nil && a.modelOverrides.Apply(m, p.ID, m.ID) {
		a.log("info", "learning", "applied manual overrides to %s/%s (context=%d vision=%v)", p.ID, m.ID, m.Context, m.Vision)
	}
}

func (a *Service) TurnToolDefs(run *TurnRun) []ToolDef {
	if a == nil || a.Toolbox == nil {
		return nil
	}
	kind := AgentConversation
	if run.Headless {
		kind = AgentAutomation
	}
	if run.ToolKind != "" {
		kind = run.ToolKind
	}
	return toToolDefs(a.toolFactory().Get(kind, run.Workspace))
}

func (a *Service) toolFactory() *tools.ToolFactory {
	var toolbox func() []tools.ToolInfo
	if a != nil && a.Toolbox != nil {
		toolbox = a.Toolbox.ListTools
	}
	return &tools.ToolFactory{Toolbox: toolbox, Dispatchers: tools.FilterDispatcherToolInfos}
}

func (a *Service) waitForRetry(ctx context.Context, delay time.Duration) error {
	if a != nil && a.deps.WaitRetry != nil {
		return a.deps.WaitRetry(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *Service) waitSlowDown(ctx context.Context) {
	if a != nil && a.deps.WaitSlowDown != nil {
		a.deps.WaitSlowDown(ctx)
	}
}

func (a *Service) providerNameByID(providerID string) string {
	if a != nil && a.deps.ProviderName != nil {
		return a.deps.ProviderName(providerID)
	}
	return providerID
}

func (a *Service) effectiveWorkspace(workspace string) string {
	if a != nil && a.deps.EffectiveWorkspace != nil {
		return a.deps.EffectiveWorkspace(workspace)
	}
	if workspace != "" {
		return workspace
	}
	return a.defaultWorkspace
}

func (a *Service) resolveModel(model string) (*domain.Provider, string, string, *contracts.RPCError) {
	if a == nil || a.deps.ResolveModel == nil {
		return nil, "", "", nil
	}
	return a.deps.ResolveModel(model)
}
