package application

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

func (a *App) emitAcpRun(event string, run *domain.AcpRun) {
	if run == nil || a.Bus == nil {
		return
	}
	a.Bus.Emit(event, contracts.AcpRunEvent{Run: acpRunDTO(run)})
}

// onAcpRunDone handles ACP subagent completion: persists the transcript,
// then delivers the result through the shared background-run path
// (queued at the next tool-round boundary on a live parent turn, or
// injected immediately plus a new turn when the parent is idle).
func (a *App) onAcpRunDone(run *domain.AcpRun) {
	if run == nil || run.ConversationID == "" {
		return
	}

	// 1. Persist the full transcript.
	outputPath := a.persistAcpRun(run)
	status := domain.ToolOK
	if run.Status == domain.AcpRunFailed || run.Status == domain.AcpRunCancelled {
		status = domain.ToolFailed
	}
	a.deliverRunDone(run.ConversationID, pendingRunDone{
		RunID: run.ID,
		Complete: func(cid string) error {
			return a.completeSubagentRunLocked(cid, run.ParentToolCallID, status, run, outputPath)
		},
	})
}

// deliverRunDone delivers a finished background run to its parent
// conversation: queued at the next tool-round boundary when the parent
// turn is live (the conversation lock is held for the whole turn), or
// injected immediately — under the conversation turn lock — plus a new
// turn when the parent is idle. The Complete callback is always the
// "Locked" variant because the caller (deliverRunDone or the turn-boundary
// drain) already holds the lock.
func (a *App) deliverRunDone(conversationID string, pending pendingRunDone) {
	if conversationID == "" || pending.Complete == nil {
		return
	}
	a.runsMu.Lock()
	if parent := a.activeRunForConversationLocked(conversationID); parent != nil {
		parent.queueRunDone(pending)
		a.runsMu.Unlock()
		return
	}
	a.runsMu.Unlock()

	turnLock := a.conversationTurnLock(conversationID)
	turnLock.Lock()
	err := pending.Complete(conversationID)
	turnLock.Unlock()
	if err != nil {
		a.log("error", "agent", "deliver background run %s: %v", pending.RunID, err)
		return
	}
	if a.untrackPendingRun(conversationID, pending.RunID) {
		a.triggerBackgroundCompletionTurn(conversationID)
	}
}

func (a *App) persistAcpRun(run *domain.AcpRun) string {
	if run == nil || run.Live() || a.AcpRunStorage == nil {
		return ""
	}
	record := domain.AcpRunRecord{
		ID:               run.ID,
		AgentID:          run.AgentID,
		AgentName:        run.AgentName,
		ConversationID:   run.ConversationID,
		ParentToolCallID: run.ParentToolCallID,
		Workspace:        run.Workspace,
		Prompt:           run.Prompt,
		Status:           run.Status,
		ModelID:          run.CurrentModelID,
		RiskTier:         run.RiskTier,
		StopReason:       run.StopReason,
		Error:            run.Error,
		Transcript:       run.Transcript,
		StartedAt:        run.StartedAt,
		EndedAt:          run.FinishedAt,
	}
	if err := a.AcpRunStorage.Save(record); err != nil {
		a.log("error", "acp", "failed to persist run %s: %v", run.ID, err)
		return ""
	}
	return a.AcpRunStorage.Path(run.ConversationID, run.ID)
}

// completeSubagentRunLocked updates the original `subagent` tool call to its
// brief terminal state and injects a synthetic assistant message carrying
// the `subagent_result` tool call with the full result pre-filled
// (announcement-style). Persisted so the model sees it in this turn and
// in later turns (auto-continue), and the UI renders it as a normal tool
// card. Keeps the cache-stable system prompt untouched. The caller must
// hold the conversation turn lock.
func (a *App) completeSubagentRunLocked(conversationID, toolCallID string, status domain.ToolCallStatus, run *domain.AcpRun, outputPath string) error {
	repo, err := a.loadRepo(conversationID)
	if err != nil {
		a.log("error", "acp", "completeSubagentRun: conversation %s not found: %v", conversationID, err)
		return err
	}
	conv := repo.Conversation()
	toolArgs := toolpresentation.ToolCallArgsFromConversation(conv, toolCallID)
	if toolCallID != "" {
		conv = a.updateToolResult(conv, "", toolCallID, status, domain.SubagentBriefResult(run), nil)
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
	if parentRun := a.activeRunForConversation(conversationID); parentRun != nil {
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

// subagentResultMessage builds the synthetic assistant message carrying
// the `subagent_result` tool call with its result pre-filled. Mirrors
// restartAnnouncement: persisted into the conversation so the model
// processes the result like any freshly completed tool output.
func (a *App) subagentResultMessage(run *domain.AcpRun, outputPath string, status domain.ToolCallStatus) domain.Message {
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

// triggerBackgroundCompletionTurn starts a new agent turn for the
// conversation to process a completed background run's output. This is
// the "tool injection" mechanism: the parent agent sees the synthetic
// result tool call in its message history and processes the result as if
// it had just completed the tool call itself. Today the only producer is
// ACP subagents (subagent_result); future async tools reuse this path.
//
// The turn is only started if the conversation is idle (no active turn).
// Live parent turns drain queued completions at the next tool-round
// boundary (same as steer) instead of starting a second turn.
func (a *App) triggerBackgroundCompletionTurn(conversationID string) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	turnLock := a.conversationTurnLock(conversationID)
	turnLock.Lock()
	defer turnLock.Unlock()

	// If a turn is already active, the updated tool call will be picked
	// up in the next round — no need to start a new one.
	if a.activeRunForConversation(conversationID) != nil {
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

	// Find the provider + model from the conversation's last assistant
	// message. If none, use the first enabled provider.
	provider, model, apiKey, effort, err := a.resolveConversationProvider(conv)
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
		Workspace:      conv.Workspace,
	}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	bareModel := strings.TrimSpace(strings.TrimPrefix(model, provider.ID+"/"))
	caps := modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams, a.modelOverrides)

	a.goSafe("agent", func() {
		a.runTurn(run, provider, apiKey, bareModel, effort, asstMsg.ID, false, caps)
	})
	a.log("info", "acp", "subagent completion turn triggered for %s (model %s)", conv.ID, bareModel)
}

// resolveConversationProvider picks the provider + model + API key +
// effort for a subagent completion turn. It uses the conversation's
// last successful assistant message model; if none, it falls back to
// the first enabled provider with a model.
func (a *App) resolveConversationProvider(conv *domain.Conversation) (*domain.Provider, string, string, string, error) {
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
	// Fallback: first enabled provider with at least one model.
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
