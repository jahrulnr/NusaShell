package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nusashell/domain"
)

// RunHeadlessTurn executes a full agent turn synchronously (no streaming UI)
// and returns the final assistant text as {"output": text}. It is the backing
// implementation for pipeline agent steps. ACP subagent tools are filtered
// out so permission prompts never stall a headless run.
func (a *App) RunHeadlessTurn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any) (map[string]any, string, error) {
	provider, bareModel, apiKey, err := a.resolveHeadlessModel(model)
	if err != nil {
		return nil, "", err
	}

	convID := domain.NewID("conv")
	now := time.Now().UTC()
	conv := domain.NewConversation(convID, "[pipeline] "+truncate(prompt, 60))
	conv.Model = provider.ID + ":" + bareModel
	conv.Status = "running"
	conv.AddMessage(domain.Message{
		ID:        domain.NewID("msg"),
		Role:      domain.RoleUser,
		Content:   prompt,
		CreatedAt: now,
		Status:    domain.StatusDone,
	})
	asstMsgID := domain.NewID("msg")
	conv.AddMessage(domain.Message{
		ID:         asstMsgID,
		Role:       domain.RoleAssistant,
		CreatedAt:  now,
		ProviderID: provider.ID,
	})
	if err := a.Conversations.Save(conv); err != nil {
		return nil, "", fmt.Errorf("headless turn: save conversation: %w", err)
	}

	turnCtx, cancel := context.WithCancel(ctx)
	run := &TurnRun{
		ID:             domain.NewID("run"),
		ConversationID: convID,
		MessageID:      asstMsgID,
		Ctx:            turnCtx,
		Cancel:         cancel,
		Headless:       true,
		RiskTierCap:    domain.TrustLevelToRiskTierCap(trust),
		Workspace:      conv.Workspace,
	}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	a.runTurn(run, provider, apiKey, bareModel, "", asstMsgID, false, modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams, a.modelOverrides))

	saved, err := a.Conversations.Get(convID)
	if err != nil || saved == nil {
		return nil, "", fmt.Errorf("headless turn: read conversation: %w", err)
	}
	var text string
	var failed bool
	for _, m := range saved.Messages {
		if m.ID == asstMsgID {
			text = m.Content
			if m.Status == domain.StatusError {
				failed = true
			}
			break
		}
	}
	if failed {
		return nil, "", fmt.Errorf("headless turn failed: %s", text)
	}
	_ = schema // structured output validation is a future enhancement
	return map[string]any{"output": text}, convID, nil
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
