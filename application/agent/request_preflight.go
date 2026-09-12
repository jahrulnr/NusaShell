package agent

import (
	"nusashell/application/provider"
	"nusashell/domain"
	"nusashell/pkg/text"
)

// requestEffort is the normalized effort value used both by the request sent
// to the provider and by the preflight estimator. Non-reasoning models must
// not receive a level that asks for thinking.
func requestEffort(effort string, caps ModelCapabilities) string {
	if !caps.Reasoning && effort != "" && effort != "auto" && effort != "none" {
		return "auto"
	}
	return effort
}

// buildTurnRequest is the single request-shaping path for a conversation
// round. Compaction checks call it before streaming so the watermark sees the
// same system prompt, hydrated messages, tools, provider options, and opaque
// provider-visible compaction items as the request that will be sent.
func (a *Service) buildTurnRequest(run *TurnRun, adapter ProviderContext, conversation *domain.Conversation, messageID, model, effort string, tools []ToolDef, settings domain.Settings, continuation bool, partial *StreamedTurnRound, maxTokens int, promptCache *PromptCachePolicy, caps ModelCapabilities) ChatRequest {
	if conversation == nil {
		conversation = &domain.Conversation{}
	}
	providerID := ""
	conversationID := conversation.ID
	if run != nil {
		providerID = run.ProviderID
		if run.ConversationID != "" {
			conversationID = run.ConversationID
		}
	}

	messages := a.chatMessagesForProvider(conversation, messageID, caps)
	if continuation {
		if partial != nil && (visible(partial.Content) || visible(partial.Reasoning)) {
			messages = appendContinuationFromPartial(messages, *partial)
		} else {
			messages = appendContinuationTool(messages)
		}
	}
	if a.learnedParams != nil && a.learnedParams.NeedsUserNudge(providerID, model) && needsUserMessageAtEnd(messages) {
		messages = append(messages, ChatMessage{Role: "user", Content: userNudgeText})
	}

	return ChatRequest{
		Model:                    model,
		System:                   buildSystemPromptForRun(run, conversation, settings.UserPrompt),
		Messages:                 messages,
		Tools:                    tools,
		PromptCaching:            settings.PromptCaching,
		PromptCache:              promptCache,
		MaxTokens:                maxTokens,
		Effort:                   requestEffort(effort, caps),
		ReasoningSummary:         adapter.ReasoningSummary,
		ProviderRoute:            conversation.ProviderRoute,
		Temperature:              settings.Temperature,
		TopP:                     settings.TopP,
		TopK:                     settings.TopK,
		FrequencyPenalty:         settings.FrequencyPenalty,
		PresencePenalty:          settings.PresencePenalty,
		ConversationID:           conversationID,
		ReasoningReplay:          caps.ReasoningReplay,
		StripParams:              a.learnedParams.StripParams(providerID, model),
		CompactionBlob:           conversation.CompactionBlob,
		CompactionPrefixMessages: a.compactionPrefixMessageCount(conversation, caps),
		ContextManagement:        serverCompactionContextManagementForKind(model, adapter.Kind),
	}
}

func visible(value string) bool {
	return len(value) > 0 && text.Visible(value) != ""
}

// estimateTurnRequest estimates the model-visible request after provider
// conversion. The fallback keeps a malformed/empty request from disabling a
// compaction safety check entirely; normal turn requests always include a
// non-empty system prompt and use the exact provider estimate.
func (a *Service) estimateTurnRequest(req ChatRequest, conversation *domain.Conversation, adapter ProviderContext) int64 {
	estimate := provider.EstimateRequestTokens(req, adapter.Kind, adapter.OpenRouter)
	if estimate <= 0 && conversation != nil {
		return int64(conversation.EstimateTokens())
	}
	return estimate
}
