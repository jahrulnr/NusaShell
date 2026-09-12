package agent

import (
	"nusashell/contracts"
	"nusashell/domain"
)

// maybeCompactInitialTurn performs the first compaction check after all
// request inputs are known. In particular, media fallback enrichment and the
// actual tool definitions must be part of the same provider-shaped estimate
// as the first request.
func (a *Service) maybeCompactInitialTurn(run *TurnRun, adapter ProviderContext, conversation *domain.Conversation, providerMeta *domain.Provider, model, messageID, effort string, settings domain.Settings, caps ModelCapabilities, tools []ToolDef, maxTokens int, promptCache *PromptCachePolicy, continuation bool) (*domain.Conversation, error) {
	if conversation == nil {
		return conversation, nil
	}
	contextWindow := a.ResolveContextWindow(providerMeta, model, settings)
	trigger := domain.CompactionTriggerTokens(contextWindow, maxTokens, settings)
	request := a.buildTurnRequest(run, adapter, conversation, messageID, model, effort, tools, settings, continuation, nil, maxTokens, promptCache, caps)
	beforeTokens := a.estimateTurnRequest(request, conversation, adapter)
	if !settings.CompactionEnabled || beforeTokens <= int64(trigger) {
		return conversation, nil
	}

	a.log("info", "agent", "compaction triggered for %s: est=%d trigger=%d window=%d maxOut=%d",
		conversation.ID, beforeTokens, trigger, contextWindow, maxTokens)
	compAdapter, compModel, compWindow := a.ResolveCompactionAdapter(run.Ctx, adapter, model, contextWindow, settings)
	compactionCache := a.compactionPromptCache(settings, compAdapter, conversation, compModel)
	a.EmitCompactionStarted(run, conversation.ID)
	summary, compErr := a.compactConversationWithCache(run.Ctx, compAdapter, conversation, compModel, compWindow, settings, domain.CompactionTriggerInitial, compactionCache, caps)
	if compErr != nil {
		a.log("warn", "agent", "compaction failed for %s: %v", conversation.ID, compErr)
		a.EmitInteractiveTurnEvent(run, contracts.EventCompactionFailed, contracts.CompactionFailedEvent{RunID: run.ID, ConversationID: conversation.ID, Error: compErr.Error()})
	} else {
		a.EmitInteractiveTurnEvent(run, contracts.EventCompacted, contracts.CompactedEvent{RunID: run.ID, ConversationID: conversation.ID, Summary: summary})
		a.log("info", "agent", "compacted conversation %s", conversation.ID)
	}
	refreshed, getErr := a.Conversations.Get(run.ConversationID)
	if getErr != nil {
		return nil, getErr
	}
	afterRequest := a.buildTurnRequest(run, adapter, refreshed, messageID, model, effort, tools, settings, continuation, nil, maxTokens, promptCache, caps)
	afterTokens := a.estimateTurnRequest(afterRequest, refreshed, adapter)
	a.log("info", "agent", "compaction result for %s: before=%d after=%d (msgs=%d)",
		refreshed.ID, beforeTokens, afterTokens, len(refreshed.Messages))
	return refreshed, compErr
}
