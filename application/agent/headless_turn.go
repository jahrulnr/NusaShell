package agent

import (
	"context"
	"fmt"
	"strings"

	"nusashell/domain"
	"nusashell/pkg/text"
	clock "nusashell/pkg/time"
)

// headlessConversationType maps an agent kind to the persisted conversation
// type. Learning jobs are background transcripts; every other headless run
// (pipeline agent steps, internal delegates) is an automation transcript.
// Neither appears in agent.conversations.list.
func headlessConversationType(kind AgentKind) domain.ConversationType {
	switch kind {
	case AgentLearner, AgentMemoryConsolidator, AgentSkillEvolver, AgentSkillEvaluator:
		return domain.ConversationTypeBackground
	default:
		return domain.ConversationTypeAutomation
	}
}

// headlessWorkspace resolves the workspace a headless turn hydrates against.
// Background learning kinds operate on the NusaShell data directory itself
// (profile documents, growth/learning stores, skills) and the learner prompt
// expects its checkpoint to expose exactly those surfaces, so their turns
// use the data dir instead of the source conversation's project tree (no
// user-project AGENTS.md/file_list leaking into every learning job).
// Pipeline and delegate turns keep the context workspace. An empty dataDir
// (tests, partial wiring) falls back to the context workspace unchanged.
func headlessWorkspace(ctxWorkspace string, kind AgentKind, fallbackDataDir string) string {
	if isLearnerKind(kind) && strings.TrimSpace(fallbackDataDir) != "" {
		return fallbackDataDir
	}
	return ctxWorkspace
}

// headlessTurnTitle names the persisted transcript. Learning jobs carry a
// stable readable title so learning/trajectory.jsonl and the conversation
// store stay auditable; other headless runs keep the historical
// "[pipeline] " prefix.
func headlessTurnTitle(kind AgentKind, prompt string) string {
	switch kind {
	case AgentLearner, AgentMemoryConsolidator, AgentSkillEvolver, AgentSkillEvaluator:
		return "[learning] learner"
	default:
		return "[pipeline] " + text.Truncate(prompt, 60)
	}
}

// RunHeadlessTurn executes a full agent turn synchronously (no Agent room)
// and returns the final assistant text as {"output": text}. It is the backing
// implementation for pipeline agent steps. The persisted conversation is
// marked Origin=pipeline so it stays out of agent.conversations.list while
// remaining addressable for automation(op="steer"). AgentAutomation turns
// filter ACP subagent tools so permission prompts never stall a pipeline run;
// background learning kinds use a pruned toolbox (no project memory, ACP/
// delegate, or MCP) plus learn() and conversation inspect ops.
func (a *Service) RunHeadlessTurn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any) (map[string]any, string, error) {
	return a.RunHeadlessTurnIn(ctx, prompt, model, trust, schema, "")
}

// RunHeadlessTurnIn is RunHeadlessTurn with an optional conversation to
// resume: when conversationID is non-empty, the prompt is appended to that
// existing automation conversation instead of starting a fresh transcript,
// so a reused agent step keeps the memory of its earlier runs.
func (a *Service) RunHeadlessTurnIn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, conversationID string) (map[string]any, string, error) {
	return a.RunHeadlessTurnKindObserved(ctx, prompt, model, trust, schema, AgentAutomation, conversationID, nil)
}

// runHeadlessTurnKind is RunHeadlessTurn parameterized by the agent kind:
// pipeline steps use AgentAutomation, internal delegates use AgentDelegate
// (which also removes the delegate tool itself to prevent recursion).
func (a *Service) RunHeadlessTurnKind(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, kind AgentKind) (map[string]any, string, error) {
	return a.RunHeadlessTurnKindObserved(ctx, prompt, model, trust, schema, kind, "", nil)
}

func (a *Service) RunHeadlessTurnKindObserved(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, kind AgentKind, conversationID string, onUpdate func(conversationID string)) (out map[string]any, convID string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = nil
			err = fmt.Errorf("headless turn panicked: %v", recovered)
		}
	}()

	provider, bareModel, apiKey, err := a.ResolveHeadlessModel(model)
	if err != nil {
		return nil, "", err
	}

	repo := NewConversation(a.Conversations, headlessTurnTitle(kind, prompt))
	if conversationID != "" {
		existing, getErr := a.Conversations.Get(conversationID)
		if getErr != nil || existing == nil {
			return nil, "", fmt.Errorf("headless turn reuse: conversation %s not found", conversationID)
		}
		if existing.Type != domain.ConversationTypeAutomation {
			return nil, "", fmt.Errorf("headless turn reuse: conversation %s is not an automation conversation", conversationID)
		}
		if existing.Status == "running" {
			return nil, "", fmt.Errorf("headless turn reuse: conversation %s is busy", conversationID)
		}
		repo = bindConversation(a.Conversations, existing)
	}
	conv := repo.Conversation()
	if conversationID == "" {
		conv.Type = headlessConversationType(kind)
	}
	conv.Workspace = headlessWorkspace(WorkspaceFromContext(ctx), kind, a.DataDir)
	conv.Model = provider.ID + ":" + bareModel
	conv.Status = "running"
	now := clock.NewTime().Time()
	asstMsgID := domain.NewID(domain.IDPrefixMsg)
	a.AddTurnMessages(conv, domain.Message{
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
	convID = repo.ID()

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
		Workspace:      a.effectiveWorkspace(conv.Workspace),
	}
	if onUpdate != nil {
		run.HeadlessUpdate = func() { onUpdate(convID) }
	}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()
	if onUpdate != nil {
		// Steer gap: expose ConversationID as soon as the turn is registered,
		// not only after RunAgentStep returns / at later round boundaries.
		onUpdate(convID)
	}

	a.emitRunStepPre(run)
	a.log("info", "agent", "headless turn started: run=%s conv=%s model=%s", run.ID, convID, provider.ID+":"+bareModel)
	a.RunTurn(run, provider, apiKey, bareModel, "", asstMsgID, false, modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams, a.modelOverrides))
	a.log("info", "agent", "headless turn finished: run=%s conv=%s", run.ID, convID)

	finalMessageID := run.CurrentMessageID()
	saved, err := a.Conversations.Get(convID)
	if err != nil || saved == nil {
		a.emitRunStepPost(run, AgentStatusError, "read conversation failed")
		return nil, "", fmt.Errorf("headless turn: read conversation: %w", err)
	}
	final, found := finalHeadlessAssistantMessage(saved.Messages, finalMessageID)
	if !found {
		a.emitRunStepPost(run, AgentStatusError, "final assistant message not found")
		return nil, "", fmt.Errorf("headless turn: final assistant message %s not found", finalMessageID)
	}
	if final.Status == domain.StatusError {
		err := fmt.Errorf("headless turn failed: %s", final.Content)
		a.emitRunStepPost(run, AgentStatusError, final.Content)
		a.log("error", "agent", "headless turn failed: run=%s conv=%s: %v", run.ID, convID, err)
		return nil, "", err
	}
	if final.Status == domain.StatusInterrupted {
		err := fmt.Errorf("headless turn interrupted before producing output (run=%s conv=%s)", run.ID, convID)
		a.emitRunStepPost(run, AgentStatusError, err.Error())
		a.log("warn", "agent", "%v", err)
		return nil, convID, err
	}
	if err := validateHeadlessOutput(final.Content, schema); err != nil {
		a.emitRunStepPost(run, AgentStatusError, err.Error())
		return nil, convID, err
	}
	a.emitRunStepPost(run, AgentStatusOK, "")
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
func (a *Service) SteerHeadlessTurn(conversationID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("steer text is required")
	}
	run := a.ActiveRunForConversation(conversationID)
	if run == nil {
		return fmt.Errorf("no active headless turn for conversation %s", conversationID)
	}
	entry := newSteerEntry(text, nil)
	if !run.QueueSteer(entry) {
		return fmt.Errorf("a steer is already queued for conversation %s", conversationID)
	}
	return nil
}

// resolveHeadlessModel picks the provider + model for a headless turn. When
// modelID is empty, the Settings "Internal delegate model" (delegate_model)
// is used when configured; otherwise the first enabled provider with at least
// one model is used. Returns an error when no usable provider is available.
func (a *Service) ResolveHeadlessModel(modelID string) (*domain.Provider, string, string, error) {
	if strings.TrimSpace(modelID) == "" && a.Settings != nil {
		if configured := strings.TrimSpace(a.Settings.Get().DelegateModel); configured != "" {
			p, bare, key, rpcErr := a.resolveModel(configured)
			if rpcErr != nil {
				return nil, "", "", fmt.Errorf("%s", rpcErr.Message)
			}
			return p, bare, key, nil
		}
	}
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
		key, _, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, "", "", fmt.Errorf("read credential for %s: %w", p.Name, err)
		}
		m := &p.Models[0]
		a.applyModelOverrides(p, m)
		return p, m.ID, key, nil
	}
	return nil, "", "", fmt.Errorf("no enabled provider with a model is available for headless agent steps")
}
