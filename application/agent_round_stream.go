package application

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"nusashell/application/service/learnedparams"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/text"
)

const defaultMaxParallelTools = domain.DefaultMaxParallelTools

type streamedTurnRound struct {
	Content   string
	Reasoning string
	Response  ChatResponse
}

func (a *App) initializeTurn(run *TurnRun, provider *domain.Provider, apiKey, model string) (ProviderContext, *domain.Conversation, domain.Settings, error) {
	adapter, err := a.Factory(run.Ctx, provider, apiKey)
	if err != nil {
		return ProviderContext{}, nil, domain.Settings{}, err
	}
	pc := NewProviderContext(provider, adapter)

	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return ProviderContext{}, nil, domain.Settings{}, err
	}
	settings := a.Settings.Get()
	contextWindow := a.resolveContextWindow(provider, model, settings)
	maxOutput := domain.ResolveMaxOutput(provider, model, settings)
	compactionTrigger := domain.CompactionTriggerTokens(contextWindow, maxOutput, settings)
	beforeTokens := conversation.EstimateTokens()
	if !settings.CompactionEnabled || beforeTokens <= compactionTrigger {
		return pc, conversation, settings, nil
	}

	a.log("info", "agent", "compaction triggered for %s: est=%d trigger=%d window=%d maxOut=%d",
		conversation.ID, beforeTokens, compactionTrigger, contextWindow, maxOutput)
	compAdapter, compModel, compWindow := a.resolveCompactionAdapter(run.Ctx, pc, model, contextWindow, settings)
	a.emitCompactionStarted(run, conversation.ID)
	summary, err := a.compactConversation(run.Ctx, compAdapter, conversation, compModel, compWindow, settings)
	if err != nil {
		a.log("warn", "agent", "compaction failed for %s: %v", conversation.ID, err)
		a.Bus.Emit(contracts.EventCompactionFailed, contracts.CompactionFailedEvent{RunID: run.ID, ConversationID: conversation.ID, Error: err.Error()})
	} else {
		a.Bus.Emit(contracts.EventCompacted, contracts.CompactedEvent{RunID: run.ID, ConversationID: conversation.ID, Summary: summary})
		a.log("info", "agent", "compacted conversation %s", conversation.ID)
	}
	refreshed, getErr := a.Conversations.Get(run.ConversationID)
	if getErr != nil {
		return pc, nil, settings, getErr
	}
	conversation = refreshed
	afterTokens := conversation.EstimateTokens()
	a.log("info", "agent", "compaction result for %s: before=%d after=%d (msgs=%d)",
		conversation.ID, beforeTokens, afterTokens, len(conversation.Messages))
	return pc, conversation, settings, err
}

func (a *App) streamTurnRound(run *TurnRun, adapter ProviderContext, conversation *domain.Conversation, messageID, model, effort string, tools []ToolDef, settings domain.Settings, continuation bool, maxTokens int, promptCache *PromptCachePolicy, caps ModelCapabilities, round int) (streamedTurnRound, error) {
	for retry := 1; ; retry++ {
		roundResult, err := a.streamTurnRoundOnce(run, adapter, conversation, messageID, model, effort, tools, settings, continuation, maxTokens, promptCache, caps, round)
		if err == nil || retry >= maxProviderAttempts || text.Visible(roundResult.Content) != "" || text.Visible(roundResult.Reasoning) != "" {
			return roundResult, err
		}
		adapted := a.learnFromStreamError(run, model, err, &caps)
		delay, retryable := providerRetryDelay(err, retry)
		if !retryable && !adapted {
			return roundResult, err
		}
		if !retryable {
			// Learnable 400 was adapted but is not normally retryable.
			// Use a short fixed delay; the adaptation makes the next
			// request valid.
			delay = retryBaseDelay
		}
		a.prepareStreamRetry(run, messageID, retry, delay, err)
		if err := a.retrySleeper(run.Ctx, delay); err != nil {
			return roundResult, err
		}
	}
}

// learnFromStreamError records a recoverable provider 400 and updates the
// capabilities used to rebuild the next request. It returns true only when
// learning produced an adaptation that makes an otherwise non-retryable 400
// worth retrying in the current round.
func (a *App) learnFromStreamError(run *TurnRun, model string, err error, caps *ModelCapabilities) bool {
	if !isLearnable400(err) {
		return false
	}

	body := extractErrBody(err)
	action, param := a.learnedParams.LearnFrom400(run.ProviderID, model, body)
	adapted := action != "" && param != "" && action != domain.LearnedActionCapContext
	providerName := a.providerNameByID(run.ProviderID)

	switch {
	case action == domain.LearnedActionCapContext:
		a.log("info", "learning", "capped context window for %s/%s to %s tokens from 400", providerName, model, param)
	case action == domain.LearnedActionInject && strings.EqualFold(param, learnedparams.StripReasoningContentParam):
		if !caps.ReasoningReplay {
			caps.ReasoningReplay = true
			a.log("info", "learning", "upgraded ReasoningReplay for %s/%s from 400 learning", providerName, model)
		}
	case action == domain.LearnedActionDisableModality:
		if strings.EqualFold(param, "vision") && caps.Vision {
			caps.Vision = false
			a.log("info", "learning", "disabled Vision for %s/%s from 400 learning (text-only model)", providerName, model)
		}
		if strings.EqualFold(param, "audio") && caps.Audio {
			caps.Audio = false
			a.log("info", "learning", "disabled Audio for %s/%s from 400 learning", providerName, model)
		}
		if strings.EqualFold(param, "video") && caps.Video {
			caps.Video = false
			a.log("info", "learning", "disabled Video for %s/%s from 400 learning", providerName, model)
		}
	case action == domain.LearnedActionNudgeUser:
		a.log("info", "learning", "learned nudge_user for %s/%s from 400 learning (provider requires user message)", providerName, model)
	}
	return adapted
}

// prepareStreamRetry publishes the retry state and clears buffered deltas so
// subscribers never replay a partial failed attempt together with its retry.
func (a *App) prepareStreamRetry(run *TurnRun, messageID string, retry int, delay time.Duration, err error) {
	a.log("warn", "ai", "retrying provider stream for turn %s (%d/%d) after %s: %s", run.ID, retry, maxProviderAttempts, delay.Round(time.Millisecond), describeProviderError(err))
	retryEvt := contracts.ProviderRetryEvent{
		RunID:          run.ID,
		ConversationID: run.ConversationID,
		MessageID:      messageID,
		Attempt:        retry + 1,
		MaxAttempts:    maxProviderAttempts,
		DelayMS:        delay.Milliseconds(),
		Error:          err.Error(),
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		retryEvt.Kind = string(upstream.Kind)
		retryEvt.Status = upstream.StatusCode
	}
	a.Bus.Emit(contracts.EventProviderRetry, retryEvt)
	if a.RoundStreams != nil {
		a.RoundStreams.Reset(run.ID, messageID)
	}
}

func (a *App) streamTurnRoundOnce(run *TurnRun, adapter ProviderContext, conversation *domain.Conversation, messageID, model, effort string, tools []ToolDef, settings domain.Settings, continuation bool, maxTokens int, promptCache *PromptCachePolicy, caps ModelCapabilities, round int) (streamedTurnRound, error) {
	var content strings.Builder
	var reasoning strings.Builder
	// Guard: strip effort for models that do not support reasoning. Sending
	// "low"/"medium"/"high" to a non-reasoning model pushes a thinking field
	// the upstream rejects or silently ignores. "auto" (omit) and "none"
	// (explicit disable) are safe to keep — they do not request thinking.
	if !caps.Reasoning && effort != "" && effort != "auto" && effort != "none" {
		a.log("warn", "ai", "stripping effort %q for non-reasoning model %s", effort, model)
		effort = "auto"
	}
	// The system prompt is deliberately cache-stable: identity, user
	// instructions, system-level skill messages, and workspace only. No
	// runtime state (delegation config, continuation instructions, async
	// results) is appended here — those travel as tool hydration or tool
	// descriptions so the system prefix keeps its prompt-cache hits.
	system := buildSystemPromptForRun(run, conversation, settings.UserPrompt)
	// The hydration checkpoint is persisted once per history epoch (fresh
	// room at turn start, and inside persistCompactedConversation after
	// compaction) — never inside the turn loop. The first Stream reads the
	// already-persisted transcript, so the provider prefix up to and
	// including the checkpoint is frozen across rounds and follow-up user
	// messages. Re-injecting mid-loop relocated the checkpoint after
	// whatever user was last (the cache-poison dump) and invalidated the
	// prompt-cache prefix from the hydration byte onward. The checkpoint
	// messages carry the "hydrate-" tool call ID prefix so the UI can hide
	// them and compaction can strip them before summarization.
	messages := a.chatMessagesForProvider(conversation, messageID, caps)
	// Continuation rounds (partial-stream recovery, failed-message retry)
	// inject the "continue from where you stopped" instruction as an
	// ephemeral synthetic tool call + result instead of a system prompt
	// mutation. The model processes it like any tool output and continues
	// the interrupted response; nothing is persisted, so later rounds keep
	// the cache-stable system prefix.
	if continuation {
		messages = appendContinuationTool(messages)
	}
	// Some providers/models reject requests that contain no user message
	// ("No user query found in messages"). When 400-learning has recorded
	// this for the current provider+model and the messages don't already
	// contain a user role, inject a minimal user message ("."). This is
	// ephemeral — not persisted to the conversation store — so later turns
	// keep the real transcript intact.
	if a.learnedParams != nil && a.learnedParams.NeedsUserNudge(run.ProviderID, model) && !hasUserMessage(messages) {
		messages = append(messages, ChatMessage{Role: "user", Content: userNudgeText})
	}
	// Publish a lightweight server-side context estimate (system + messages +
	// tool definitions as actually sent) so the UI badge is not just a guess
	// from the transcript alone — and remember it on the conversation so the
	// idle badge shows the same number.
	if a.Bus != nil {
		est := estimateRequestTokens(system, messages, tools)
		a.Bus.Emit(contracts.EventContextEstimate, contracts.ContextEstimateEvent{
			RunID: run.ID, ConversationID: run.ConversationID, MessageID: messageID,
			EstimatedTokens: est,
		})
		if conversation.EstimatedTokens != est {
			conversation.EstimatedTokens = est
			if repo := bindConversation(a.Conversations, conversation); repo != nil {
				_ = repo.Save()
			}
		}
	}
	response, err := adapter.Stream(run.Ctx, ChatRequest{
		Model:             model,
		System:            system,
		Messages:          messages,
		Tools:             tools,
		PromptCaching:     settings.PromptCaching,
		PromptCache:       promptCache,
		MaxTokens:         maxTokens,
		Effort:            effort,
		ProviderRoute:     conversation.ProviderRoute,
		Temperature:       settings.Temperature,
		TopP:              settings.TopP,
		TopK:              settings.TopK,
		FrequencyPenalty:  settings.FrequencyPenalty,
		PresencePenalty:   settings.PresencePenalty,
		ConversationID:    run.ConversationID,
		ReasoningReplay:   caps.ReasoningReplay,
		StripParams:       a.learnedParams.StripParams(run.ProviderID, model),
		CompactionBlob:    conversation.CompactionBlob,
		ContextManagement: serverCompactionContextManagement(model),
	}, func(delta string) {
		content.WriteString(delta)
		a.publishRoundDelta(run.ID, messageID, round, contracts.RoundDeltaText, "", "", delta)
	}, func(delta string) {
		reasoning.WriteString(delta)
		if !reasoningDeltaVisible(reasoning.String()) {
			return
		}
		a.publishRoundDelta(run.ID, messageID, round, contracts.RoundDeltaReasoning, "", "", delta)
	})
	for _, warning := range response.Warnings {
		a.log("warn", "ai", "provider warning: %s", warning)
	}
	// Capture server-side compaction items from the response. When the
	// server triggers compaction (context_management), it emits opaque
	// compaction items in the output. Store them on the conversation so the
	// next turn replays them as a prefix; the server then truncates context
	// before the last compaction item automatically.
	if len(response.CompactionItems) > 0 {
		blob, marshalErr := json.Marshal(response.CompactionItems)
		if marshalErr != nil {
			a.log("warn", "agent", "failed to marshal compaction items for %s: %v", run.ConversationID, marshalErr)
		} else {
			conversation.CompactionBlob = string(blob)
			conversation.Summary = ""
			if repo := bindConversation(a.Conversations, conversation); repo != nil {
				_ = repo.Save()
			}
			a.log("info", "agent", "server-side compaction captured for %s (%d items)", run.ConversationID, len(response.CompactionItems))
		}
	}
	return streamedTurnRound{Content: content.String(), Reasoning: reasoning.String(), Response: response}, err
}

// reasoningDeltaVisible is true once accumulated reasoning has something the
// UI can show. Leading whitespace-only deltas must not open an empty
// Thinking disclosure.
