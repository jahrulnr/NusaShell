package agent

import (
	"context"
	"fmt"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
)

// HandleConversationsCompact starts the same compaction pass a turn would
// use, without waiting for the token watermark. The HTTP call returns as
// soon as the pass is accepted; agent.compacting / agent.compacted /
// agent.compaction.failed carry the outcome.
func (a *Service) HandleConversationsCompact(ctx context.Context, req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "id is required"}
	}
	if a == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "agent service not available"}
	}

	a.startMu.Lock()
	defer a.startMu.Unlock()
	if a.ActiveRunForConversation(id) != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
	}
	if !a.beginManualCompaction(id) {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is already compacting"}
	}
	release := true
	defer func() {
		if release {
			a.clearManualCompaction(id)
		}
	}()

	c, rpcErr := a.getConversation(id)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if c == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "conversation not found"}
	}
	if len(c.Messages) <= 1 {
		return contracts.ConversationCompactResult{
			ConversationID: id,
			Status:         contracts.CompactStatusNothingToCompact,
		}, nil
	}
	model := strings.TrimSpace(c.Model)
	if model == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "conversation has no model"}
	}
	provider, bareModel, apiKey, rpcErr := a.resolveModel(model)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if provider == nil || bareModel == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "conversation model could not be resolved"}
	}
	if !domain.CodexSupportsRemoteCompaction(provider.Kind) && domain.OpenAISupportsServerCompaction(bareModel) {
		return contracts.ConversationCompactResult{
			ConversationID: id,
			Status:         contracts.CompactStatusServerManaged,
		}, nil
	}

	compactCtx := context.WithoutCancel(ctx)
	release = false
	a.goSafe("agent", func() {
		defer a.clearManualCompaction(id)
		// goSafe recovers outside this function. A panic inside the model
		// call would then clear the server flag and emit nothing, leaving
		// the page in "compacting" until reload.
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			a.log("error", "agent", "manual compaction panicked for %s: %v", id, recovered)
			a.EmitInteractiveTurnEvent(&TurnRun{
				ID:             domain.NewID(domain.IDPrefixRun),
				ConversationID: id,
			}, contracts.EventCompactionFailed, contracts.CompactionFailedEvent{
				ConversationID: id,
				Error:          fmt.Sprintf("compaction panicked: %v", recovered),
			})
		}()
		a.runManualCompaction(compactCtx, id, provider, apiKey, bareModel)
	})
	a.log("info", "agent", "manual compaction started for %s (model %s)", id, bareModel)
	return contracts.ConversationCompactResult{
		ConversationID: id,
		Status:         contracts.CompactStatusStarted,
	}, nil
}

func (a *Service) runManualCompaction(ctx context.Context, conversationID string, provider *domain.Provider, apiKey, model string) {
	turnLock := a.ConversationTurnLock(conversationID)
	turnLock.Lock()
	defer turnLock.Unlock()

	run := &TurnRun{
		ID:             domain.NewID(domain.IDPrefixRun),
		ConversationID: conversationID,
		Ctx:            ctx,
		ProviderID:     provider.ID,
	}
	fail := func(err error) {
		a.log("warn", "agent", "manual compaction failed for %s: %v", conversationID, err)
		a.EmitInteractiveTurnEvent(run, contracts.EventCompactionFailed, contracts.CompactionFailedEvent{
			RunID:          run.ID,
			ConversationID: conversationID,
			Error:          err.Error(),
		})
	}
	if ctx.Err() != nil {
		fail(ctx.Err())
		return
	}
	if a.ActiveRunForConversation(conversationID) != nil {
		fail(fmt.Errorf("conversation is busy"))
		return
	}
	repo, err := a.loadRepo(conversationID)
	if err != nil || repo == nil {
		if err == nil {
			err = fmt.Errorf("conversation not found")
		}
		fail(err)
		return
	}
	conv := repo.Conversation()
	if conv == nil {
		fail(fmt.Errorf("conversation not found"))
		return
	}
	run.Workspace = a.effectiveWorkspace(conv.Workspace)
	if a.Factory == nil {
		fail(fmt.Errorf("provider factory is required"))
		return
	}
	adapter, err := a.Factory(ctx, provider, apiKey)
	if err != nil {
		fail(err)
		return
	}
	settings := domain.DefaultSettings()
	if a.Settings != nil {
		settings = a.Settings.Get()
	}
	window := a.ResolveContextWindow(provider, model, settings)
	caps := ModelCapabilitiesWithLearned(provider, model, a.learnedParams, a.modelOverrides)
	if _, compErr := a.runCompaction(ctx, run, conv, NewProviderContext(provider, adapter), model, window, settings, caps, domain.CompactionTriggerManual, nil); compErr != nil {
		a.log("warn", "agent", "manual compaction failed for %s: %v", conversationID, compErr)
	}
}

func (a *Service) beginManualCompaction(id string) bool {
	if a == nil || id == "" {
		return false
	}
	a.manualMu.Lock()
	defer a.manualMu.Unlock()
	if a.manualCompacting == nil {
		a.manualCompacting = map[string]struct{}{}
	}
	if _, ok := a.manualCompacting[id]; ok {
		return false
	}
	a.manualCompacting[id] = struct{}{}
	return true
}

func (a *Service) clearManualCompaction(id string) {
	if a == nil || id == "" {
		return
	}
	a.manualMu.Lock()
	defer a.manualMu.Unlock()
	delete(a.manualCompacting, id)
}

func (a *Service) manualCompactionActive(id string) bool {
	if a == nil || id == "" {
		return false
	}
	a.manualMu.Lock()
	defer a.manualMu.Unlock()
	_, ok := a.manualCompacting[id]
	return ok
}
