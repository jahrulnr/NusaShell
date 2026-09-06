package agent

import (
	"context"
	"fmt"
	"strings"

	"nusashell/application/service/toolpresentation"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/nonce"
	clock "nusashell/pkg/time"
)

// deliverRunDone delivers a finished background run to its parent
// conversation: queued at the next tool-round boundary when the parent
// turn is live (the conversation lock is held for the whole turn), or
// injected immediately — under the conversation turn lock — plus a new
// turn when the parent is idle.
func (a *Service) DeliverRunDone(conversationID string, pending PendingRunDone) {
	if conversationID == "" || pending.Complete == nil {
		return
	}
	a.runsMu.Lock()
	if parent := a.ActiveRunForConversationLocked(conversationID); parent != nil {
		parent.QueueRunDone(pending)
		a.runsMu.Unlock()
		return
	}
	a.runsMu.Unlock()

	turnLock := a.ConversationTurnLock(conversationID)
	turnLock.Lock()
	err := pending.Complete(conversationID)
	turnLock.Unlock()
	if err != nil {
		a.log("error", "agent", "deliver background run %s: %v", pending.RunID, err)
		return
	}
	if a.UntrackPendingRun(conversationID, pending.RunID) {
		a.TriggerBackgroundCompletionTurn(conversationID)
	}
}

// completeSubagentRunLocked updates the original `subagent` tool call and
// injects subagent_result. Caller holds the conversation turn lock.
func (a *Service) CompleteSubagentRunLocked(conversationID, toolCallID string, status domain.ToolCallStatus, run *domain.AcpRun, outputPath string) error {
	repo, err := a.loadRepo(conversationID)
	if err != nil {
		a.log("error", "acp", "completeSubagentRun: conversation %s not found: %v", conversationID, err)
		return err
	}
	conv := repo.Conversation()
	toolArgs := toolpresentation.ToolCallArgsFromConversation(conv, toolCallID)
	if toolCallID != "" {
		conv = a.UpdateToolResult(conv, "", toolCallID, status, domain.SubagentBriefResult(run), nil)
	}
	if err := repo.Add(domain.RoleAssistant, a.subagentResultMessage(run, outputPath, status)); err != nil {
		a.log("error", "acp", "completeSubagentRun: add result failed: %v", err)
		return err
	}
	if err := repo.Save(); err != nil {
		a.log("error", "acp", "completeSubagentRun: save failed: %v", err)
		return err
	}
	parentRunID := ""
	if parentRun := a.ActiveRunForConversation(conversationID); parentRun != nil {
		parentRunID = parentRun.ID
	}
	if toolCallID != "" {
		a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
			RunID:          parentRunID,
			ConversationID: conversationID,
			ToolCallID:     toolCallID,
			Name:           "subagent",
			Status:         string(status),
			Args:           toolpresentation.ToolArgsRaw(toolArgs),
			Output:         domain.SubagentBriefResult(run),
			Presentation:   toolpresentation.BuildToolPresentation("subagent", toolArgs, status, domain.SubagentBriefResult(run)),
		})
	}
	return nil
}

func (a *Service) subagentResultMessage(run *domain.AcpRun, outputPath string, status domain.ToolCallStatus) domain.Message {
	return domain.Message{
		ID:        domain.NewID(domain.IDPrefixMsg),
		Role:      domain.RoleAssistant,
		CreatedAt: clock.NewTime().Time(),
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.SubagentResultPrefix + nonce.Random(),
			Name:   domain.SubagentResultToolName,
			Args:   domain.SubagentResultArgs(run.ID),
			Status: status,
			Output: domain.SubagentCompletionResult(run, outputPath),
		}},
	}
}

func (a *Service) CompleteDelegateRunLocked(conversationID, runID, toolCallID string, status domain.ToolCallStatus, output, runConvID string) error {
	repo, err := a.loadRepo(conversationID)
	if err != nil {
		a.log("error", "delegate", "completeDelegateRun: conversation %s not found: %v", conversationID, err)
		return err
	}
	conv := repo.Conversation()
	toolArgs := toolpresentation.ToolCallArgsFromConversation(conv, toolCallID)
	brief := domain.DelegateBriefResult(runID, status == domain.ToolOK)
	if toolCallID != "" {
		conv = a.UpdateToolResult(conv, "", toolCallID, status, brief, nil)
	}
	if err := repo.Add(domain.RoleAssistant, a.delegateResultMessage(runID, status, output, runConvID)); err != nil {
		a.log("error", "delegate", "completeDelegateRun: add result failed: %v", err)
		return err
	}
	if err := repo.Save(); err != nil {
		a.log("error", "delegate", "completeDelegateRun: save failed: %v", err)
		return err
	}
	parentRunID := ""
	if parentRun := a.ActiveRunForConversation(conversationID); parentRun != nil {
		parentRunID = parentRun.ID
	}
	if toolCallID != "" {
		a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
			RunID:          parentRunID,
			ConversationID: conversationID,
			ToolCallID:     toolCallID,
			Name:           domain.DelegateToolName,
			Status:         string(status),
			Args:           toolpresentation.ToolArgsRaw(toolArgs),
			Output:         brief,
			Presentation:   toolpresentation.BuildToolPresentation(domain.DelegateToolName, toolArgs, status, brief),
		})
	}
	return nil
}

func (a *Service) delegateResultMessage(runID string, status domain.ToolCallStatus, output, runConvID string) domain.Message {
	return domain.Message{
		ID:        domain.NewID(domain.IDPrefixMsg),
		Role:      domain.RoleAssistant,
		CreatedAt: clock.NewTime().Time(),
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.DelegateResultPrefix + nonce.Random(),
			Name:   domain.DelegateResultToolName,
			Args:   domain.DelegateResultArgs(runID, runConvID),
			Status: status,
			Output: output,
		}},
	}
}

func (a *Service) TriggerBackgroundCompletionTurn(conversationID string) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	turnLock := a.ConversationTurnLock(conversationID)
	turnLock.Lock()
	defer turnLock.Unlock()

	if a.ActiveRunForConversation(conversationID) != nil {
		return
	}

	repo, err := a.loadRepo(conversationID)
	if err != nil {
		a.log("error", "acp", "triggerBackgroundCompletionTurn: conversation %s not found: %v", conversationID, err)
		return
	}
	conv := repo.Conversation()
	if conv.Status != "idle" {
		return
	}

	provider, model, apiKey, effort, err := a.ResolveConversationProvider(conv)
	if err != nil {
		a.log("error", "acp", "triggerBackgroundCompletionTurn: no provider: %v", err)
		return
	}

	now := clock.NewTime().Time()
	asstMsg := domain.Message{
		ID:         domain.NewID(domain.IDPrefixMsg),
		Role:       domain.RoleAssistant,
		CreatedAt:  now,
		ProviderID: provider.ID,
	}
	if err := repo.Add(domain.RoleAssistant, asstMsg); err != nil {
		a.log("error", "acp", "triggerBackgroundCompletionTurn: add failed: %v", err)
		return
	}
	conv.Status = "running"
	if err := repo.Save(); err != nil {
		a.log("error", "acp", "triggerBackgroundCompletionTurn: save failed: %v", err)
		return
	}

	turnCtx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{
		ID:             domain.NewID(domain.IDPrefixRun),
		ConversationID: conv.ID,
		MessageID:      asstMsg.ID,
		Ctx:            turnCtx,
		Cancel:         cancel,
		ProviderID:     provider.ID,
		Workspace:      a.effectiveWorkspace(conv.Workspace),
	}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	bareModel := strings.TrimSpace(strings.TrimPrefix(model, provider.ID+"/"))
	caps := modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams, a.modelOverrides)

	a.goSafe("agent", func() {
		a.RunTurn(run, provider, apiKey, bareModel, effort, asstMsg.ID, false, caps)
	})
	a.log("info", "acp", "subagent completion turn triggered for %s (model %s)", conv.ID, bareModel)
}

func (a *Service) ResolveConversationProvider(conv *domain.Conversation) (*domain.Provider, string, string, string, error) {
	model := ""
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		m := conv.Messages[i]
		if m.Role == domain.RoleAssistant && m.Model != "" && m.Status == domain.StatusDone {
			model = m.Model
			break
		}
	}
	if model == "" {
		model = conv.Model
	}
	if model != "" {
		p, bare, key, rpcErr := a.resolveModel(model)
		if rpcErr == nil && p != nil && p.Enabled {
			return p, bare, key, conv.Effort, nil
		}
	}
	if a.Providers == nil {
		return nil, "", "", "", fmt.Errorf("no enabled provider with a model")
	}
	for _, p := range a.Providers.List() {
		if !p.Enabled {
			continue
		}
		models := p.Models
		if len(models) == 0 {
			continue
		}
		key, has, err := a.Credentials.Get(p.ID)
		if err != nil || !has || key == "" {
			continue
		}
		return p, models[0].ID, key, conv.Effort, nil
	}
	return nil, "", "", "", fmt.Errorf("no enabled provider with a model")
}
