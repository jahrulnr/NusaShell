package application

import (
	"context"
	"fmt"
	"strings"

	"nusashell/domain"
	"nusashell/pkg/text"
	clock "nusashell/pkg/time"
)

// RunHeadlessTurn executes a full agent turn synchronously (no Agent room)
// and returns the final assistant text as {"output": text}. It is the backing
// implementation for pipeline agent steps. The persisted conversation is
// marked Origin=pipeline so it stays out of agent.conversations.list while
// remaining addressable for automation(op="steer"). ACP subagent tools are filtered out
// so permission prompts never stall a headless run.
func (a *App) RunHeadlessTurn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any) (map[string]any, string, error) {
	return a.runHeadlessTurnKind(ctx, prompt, model, trust, schema, AgentAutomation)
}

// runHeadlessTurnKind is RunHeadlessTurn parameterized by the agent kind:
// pipeline steps use AgentAutomation, internal delegates use AgentDelegate
// (which also removes the delegate tool itself to prevent recursion).
func (a *App) runHeadlessTurnKind(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, kind AgentKind) (map[string]any, string, error) {
	return a.runHeadlessTurnKindObserved(ctx, prompt, model, trust, schema, kind, nil)
}

func (a *App) runHeadlessTurnKindObserved(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, kind AgentKind, onUpdate func(conversationID string)) (map[string]any, string, error) {
	provider, bareModel, apiKey, err := a.resolveHeadlessModel(model)
	if err != nil {
		return nil, "", err
	}

	repo := NewConversation(a.Conversations, "[pipeline] "+text.Truncate(prompt, 60))
	conv := repo.Conversation()
	conv.Origin = domain.ConversationOriginPipeline
	conv.Model = provider.ID + ":" + bareModel
	conv.Status = "running"
	now := clock.NewTime().Time()
	asstMsgID := domain.NewID(domain.IDPrefixMsg)
	a.addTurnMessages(conv, domain.Message{
		ID:        domain.NewID(domain.IDPrefixMsg),
		Role:      domain.RoleUser,
		Content:   prompt,
		CreatedAt: now,
		Status:    domain.StatusDone,
	}, domain.Message{
		ID:         asstMsgID,
		Role:       domain.RoleAssistant,
		CreatedAt:  now,
		ProviderID: provider.ID,
	})
	if err := repo.Save(); err != nil {
		return nil, "", fmt.Errorf("headless turn: save conversation: %w", err)
	}
	convID := repo.ID()

	turnCtx, cancel := context.WithCancel(ctx)
	run := &TurnRun{
		ID:             domain.NewID(domain.IDPrefixRun),
		ConversationID: convID,
		MessageID:      asstMsgID,
		Ctx:            turnCtx,
		Cancel:         cancel,
		ProviderID:     provider.ID,
		Headless:       true,
		ToolKind:       kind,
		RiskTierCap:    domain.TrustLevelToRiskTierCap(trust),
		Workspace:      conv.Workspace,
	}
	if onUpdate != nil {
		run.HeadlessUpdate = func() { onUpdate(convID) }
	}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	a.runTurn(run, provider, apiKey, bareModel, "", asstMsgID, false, modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams, a.modelOverrides))

	finalMessageID := run.currentMessageID()
	saved, err := a.Conversations.Get(convID)
	if err != nil || saved == nil {
		return nil, "", fmt.Errorf("headless turn: read conversation: %w", err)
	}
	final, found := finalHeadlessAssistantMessage(saved.Messages, finalMessageID)
	if !found {
		return nil, "", fmt.Errorf("headless turn: final assistant message %s not found", finalMessageID)
	}
	if final.Status == domain.StatusError {
		return nil, "", fmt.Errorf("headless turn failed: %s", final.Content)
	}
	_ = schema // structured output validation is a future enhancement
	return map[string]any{"output": final.Content}, convID, nil
}

// finalHeadlessAssistantMessage returns the assistant message that ended the
// headless run. A tool round may leave an intermediate assistant message with
// an acknowledgement or partial text, followed by a fresh assistant message
// containing the actual final answer. Prefer the run's current message ID so
// an intentionally empty final round is not replaced by stale earlier text;
// the reverse scan is a compatibility fallback for callers with no ID.
func finalHeadlessAssistantMessage(messages []domain.Message, finalMessageID string) (domain.Message, bool) {
	if finalMessageID != "" {
		for _, message := range messages {
			if message.ID == finalMessageID && message.Role == domain.RoleAssistant && !domain.IsHydrationMessage(message) {
				return message, true
			}
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role == domain.RoleAssistant && !domain.IsHydrationMessage(message) {
			return message, true
		}
	}
	return domain.Message{}, false
}

// SteerHeadlessTurn queues a steer message on a running headless turn
// identified by its conversation ID. Returns an error if no active turn
// exists for the conversation.
func (a *App) SteerHeadlessTurn(conversationID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("steer text is required")
	}
	run := a.activeRunForConversation(conversationID)
	if run == nil {
		return fmt.Errorf("no active headless turn for conversation %s", conversationID)
	}
	entry := newSteerEntry(text, nil)
	if !run.queueSteer(entry) {
		return fmt.Errorf("a steer is already queued for conversation %s", conversationID)
	}
	return nil
}

// resolveHeadlessModel picks the provider + model for a headless turn. When
// modelID is empty, the first enabled provider with at least one model is
// used. Returns an error when no enabled provider is available.
func (a *App) resolveHeadlessModel(modelID string) (*domain.Provider, string, string, error) {
	if strings.TrimSpace(modelID) != "" {
		p, bare, key, rpcErr := a.resolveModel(modelID)
		if rpcErr != nil {
			return nil, "", "", fmt.Errorf("%s", rpcErr.Message)
		}
		return p, bare, key, nil
	}
	for _, p := range a.Providers.List() {
		if !p.Enabled || len(p.Models) == 0 {
			continue
		}
		key, has, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, "", "", fmt.Errorf("read credential for %s: %w", p.Name, err)
		}
		if !has && domain.RequiresKey(p.Kind) {
			continue
		}
		m := &p.Models[0]
		a.applyModelOverrides(p, m)
		return p, m.ID, key, nil
	}
	return nil, "", "", fmt.Errorf("no enabled provider with a model is available for headless agent steps")
}
