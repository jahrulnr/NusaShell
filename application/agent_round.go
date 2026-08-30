package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"nusashell/application/service/learnedparams"
	"nusashell/application/service/mediaread"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/nonce"
)

// defaultMaxParallelTools is the fallback concurrency bound for tool calls
// from a single assistant round when settings.MaxParallelTools is not set.
// The actual bound is settings.MaxParallelTools (default 6, range 1–64,
// configurable in Settings).
const defaultMaxParallelTools = 6

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
	maxOutput := resolveMaxOutput(provider, model, settings)
	compactionTrigger := compactionTriggerTokens(contextWindow, maxOutput, settings)
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
		if err == nil || retry >= maxProviderAttempts || visibleText(roundResult.Content) != "" || visibleText(roundResult.Reasoning) != "" {
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
	system := buildSystemPrompt(conversation, settings.UserPrompt)
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
			_ = a.Conversations.Save(conversation)
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
			_ = a.Conversations.Save(conversation)
			a.log("info", "agent", "server-side compaction captured for %s (%d items)", run.ConversationID, len(response.CompactionItems))
		}
	}
	return streamedTurnRound{Content: content.String(), Reasoning: reasoning.String(), Response: response}, err
}

// reasoningDeltaVisible is true once accumulated reasoning has something the
// UI can show. Leading whitespace-only deltas must not open an empty
// Thinking disclosure.
func reasoningDeltaVisible(accumulated string) bool {
	return strings.TrimSpace(accumulated) != ""
}

// serverCompactionThresholdFloor is the minimum compact_threshold we ever
// send. It matches the client-side compaction trigger so the server does not
// wait longer than the client would have. Models with larger context windows
// get a higher threshold (90% of window), but never below this floor.
var serverCompactionThresholdFloor = 120_000

// userNudgeText is the minimal content injected as a synthetic user message
// when the provider requires a user message but none is present. A single
// "." is the smallest valid user turn that satisfies the constraint without
// adding semantic content the model would act on.
const userNudgeText = "."

// hasUserMessage reports whether the messages slice contains at least one
// message with Role "user". Used to decide whether to inject a nudge user
// message for providers that require one.
func hasUserMessage(messages []ChatMessage) bool {
	for _, m := range messages {
		if m.Role == "user" {
			return true
		}
	}
	return false
}

// serverCompactionContextManagement returns the context_management directive
// for server-side compaction when the model is eligible. Returns nil for
// ineligible models (the client-side summarization path handles them).
// The threshold is max(context_window*0.9, floor) so small-window eligible
// models (200k) trigger at a reasonable point while large-window models
// (400k–1M) use most of their window before compacting.
func serverCompactionContextManagement(model string) []map[string]any {
	if !domain.OpenAISupportsServerCompaction(model) {
		return nil
	}
	window := domain.OpenAIServerCompactionContextWindow(model)
	threshold := int(float64(window) * 0.9)
	if threshold < serverCompactionThresholdFloor {
		threshold = serverCompactionThresholdFloor
	}
	return []map[string]any{
		{"type": "compaction", "compact_threshold": threshold},
	}
}

// appendContinuationTool appends the synthetic announcement tool call (with
// its result pre-filled) to the provider message list for a continuation
// round after an interrupted response. Ephemeral: it exists only in this
// request, never persisted to the conversation store.
func appendContinuationTool(messages []ChatMessage) []ChatMessage {
	id := domain.AnnouncementToolCallPrefix + nonce.Random()
	call := domain.ToolCall{ID: id, Name: domain.AnnouncementToolName, Args: "{}", Status: domain.ToolOK, Output: domain.AnnouncementInterruptedMessage}
	return append(messages,
		ChatMessage{Role: "assistant", ToolCalls: []domain.ToolCall{call}},
		ChatMessage{Role: "tool", ToolResult: &ToolResult{ToolCallID: id, Name: domain.AnnouncementToolName, Content: domain.AnnouncementInterruptedMessage}},
	)
}

// estimateRequestTokens approximates provider tokens from the real request
// payload: system + messages + tools JSON. ~4 chars/token with a surcharge
// for non-ASCII (CJK-ish) characters, ~4 tokens per-message overhead, ~150
// tokens per image attachment, plus a 5% safety buffer.
func estimateRequestTokens(system string, messages []ChatMessage, tools []ToolDef) int64 {
	chars := int64(len(system))
	totalOverhead := int64(4 * len(messages))
	images := 0
	raw, _ := json.Marshal(messages)
	chars += int64(len(raw))
	if len(tools) > 0 {
		rawTools, _ := json.Marshal(tools)
		chars += int64(len(rawTools))
	}
	// Non-ASCII cost more (CJK ≈ 1–2 tokens each): add ~1 token per non-ASCII
	// rune so unicode-heavy threads are not undercounted.
	for _, m := range messages {
		if len(m.Attachments) > 0 {
			for _, a := range m.Attachments {
				if a.Type == "image" {
					images++
				}
			}
		}
	}
	chars += int64(images * 150)
	tokens := (chars + totalOverhead) / 4
	return int64(float64(tokens) * 1.05)
}

const (
	promptCacheKeyLength          = 32
	promptCacheConversationPrefix = "nusashell_cv_"
	promptCacheBackgroundPrefix   = "nusashell_bg_"
)

func promptCachePrefixForRun(run *TurnRun) string {
	if run != nil && run.Headless {
		return promptCacheBackgroundPrefix
	}
	return promptCacheConversationPrefix
}

func buildPromptCachePolicy(settings domain.Settings, p *domain.Provider, model, conversationID, prefix string) *PromptCachePolicy {
	if !settings.PromptCaching || p == nil {
		return nil
	}
	if prefix == "" {
		prefix = promptCacheConversationPrefix
	}
	canonical, _ := json.Marshal([4]string{prefix, p.ID, model, conversationID})
	sum := sha256.Sum256(canonical)
	full := hex.EncodeToString(sum[:])
	// Keep the established 32-byte budget while reserving a visible namespace
	// for the caller. Both namespaces are ASCII, so byte and character counts
	// are identical and safe for provider key limits.
	suffixLength := promptCacheKeyLength - len(prefix)
	if suffixLength <= 0 || suffixLength > len(full) {
		return nil
	}
	return &PromptCachePolicy{
		Mode: "auto",
		Key:  prefix + full[:suffixLength],
		TTL:  domain.NormalizeCacheTTL(p.Kind, p.EffectiveDriver(), p.CacheTTL),
	}
}

// persistHydration inserts the synthetic hydration messages (assistant
// toolCalls + matching tool results) immediately after the FIRST user
// message in the transcript. If there is no user yet the conversation is
// left unchanged — an assistant+tool prefix under the system prompt is
// invalid for OpenAI Chat Completions and Anthropic Messages.
//
// Hydration is an epoch marker anchored to the first user of the epoch: the
// opening user of a fresh room, or the compaction handover user (which
// Compact places at messages[0]). Anchoring after the first user — not after
// the last user before the in-flight placeholder — keeps the checkpoint
// stable across follow-up user messages and steers. The poison dump showed
// the old last-user-before-placeholder index relocating the checkpoint after
// a "." follow-up once a brief-change strip removed the original, breaking
// the prompt-cache prefix from the hydration byte onward.
//
// Does not save — the caller persists once after related mutations. Returns
// the updated conversation.
func (a *App) persistHydration(c *domain.Conversation, msgs []ChatMessage) *domain.Conversation {
	if len(msgs) == 0 {
		return c
	}
	// Build the durable messages first: one assistant message carrying all
	// hydration tool calls, then each tool result attached to its matching
	// call's Output. ToolCalls is a shared slice backing, so mutating hyd
	// after appending to built updates the stored copy as well.
	built := make([]domain.Message, 0, len(msgs))
	var hyd *domain.Message
	for _, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			hyd = &domain.Message{
				ID:        domain.NewID("msg"),
				Role:      domain.RoleAssistant,
				ToolCalls: m.ToolCalls,
				Status:    domain.StatusDone,
				CreatedAt: time.Now().UTC(),
			}
			built = append(built, *hyd)
			continue
		}
		if m.Role == "tool" && m.ToolResult != nil && hyd != nil {
			for j := range hyd.ToolCalls {
				if hyd.ToolCalls[j].ID == m.ToolResult.ToolCallID {
					hyd.ToolCalls[j].Output = m.ToolResult.Content
					break
				}
			}
		}
	}
	idx := hydrationInsertIndex(c.Messages)
	if idx < 0 {
		// No user yet (empty-room workspace pick). Inserting here would
		// park an assistant+tool turn under the system prompt; OpenAI and
		// Claude reject or mis-handle that. The first turn persists after
		// the user exists.
		return c
	}
	c.Messages = slices.Insert(c.Messages, idx, built...)
	return c
}

// hydrationInsertIndex is the slot immediately after the first user message
// in the transcript, or -1 when there is no user. The first user is the
// epoch anchor (fresh-room opening user or compaction handover), so the
// checkpoint stays put across later follow-up users and steers. OpenAI Chat
// Completions and Anthropic Messages require that user before any
// assistant/tool hydration turn.
func hydrationInsertIndex(msgs []domain.Message) int {
	for i := range msgs {
		if msgs[i].Role == domain.RoleUser {
			return i + 1
		}
	}
	return -1
}

// hydrationPrecedesFirstUser reports a protocol-invalid checkpoint: the
// synthetic assistant+tool turn sits before any user message, so the
// provider payload would be system → assistant → tool → user.
func hydrationPrecedesFirstUser(msgs []domain.Message) bool {
	for _, m := range msgs {
		if m.Role == domain.RoleUser {
			return false
		}
		if isHydrationMessage(m) {
			return true
		}
	}
	return false
}

// relocateHydrationAfterFirstUser moves a leading hydration checkpoint to
// immediately after the first user. Existing call IDs are preserved so the
// prompt-cache prefix is not rebuilt. If no user exists the orphan
// checkpoint is dropped.
func relocateHydrationAfterFirstUser(msgs []domain.Message) []domain.Message {
	if !hydrationPrecedesFirstUser(msgs) {
		return msgs
	}
	hyd := make([]domain.Message, 0, 1)
	rest := make([]domain.Message, 0, len(msgs))
	for _, m := range msgs {
		if isHydrationMessage(m) {
			hyd = append(hyd, m)
			continue
		}
		rest = append(rest, m)
	}
	idx := hydrationInsertIndex(rest)
	if idx < 0 {
		return rest
	}
	return slices.Insert(rest, idx, hyd...)
}

// repairHydrationPlacement persists relocateHydrationAfterFirstUser when a
// prior epoch parked the checkpoint before the first user (empty-room
// workspace pick, then the opening turn appended the user after it).
func (a *App) repairHydrationPlacement(run *TurnRun) {
	if a == nil || a.Conversations == nil || run == nil {
		return
	}
	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil || conversation == nil {
		return
	}
	if !hydrationPrecedesFirstUser(conversation.Messages) {
		return
	}
	conversation.Messages = relocateHydrationAfterFirstUser(conversation.Messages)
	_ = a.Conversations.Save(conversation)
}

// buildHydration assembles a synthetic runtime-hydration checkpoint from the
// App's read-only stores when the current history epoch does not already have
// one, normally on the initial turn or immediately after compaction.
func (a *App) buildHydration(c *domain.Conversation) []ChatMessage {
	ctx := DefaultRuntimeContext(c.Workspace)
	ctx.DataDir = a.DataDir
	source := HydrationSource{
		RuntimeContext: ctx,
		ConvID:         c.ID,
		Journal:        a.Journal,
	}
	if a.Toolbox != nil {
		// The real toolbox executes the meta-tools (mcp_list, tool_list per
		// server, skill, file_read) so the checkpoint contains genuine
		// tool output — the same tools the agent calls.
		source.Executor = a.Toolbox
	}
	if a.Primary != nil {
		source.PrimaryPath = a.Primary.Path()
	}
	if a.Todos != nil {
		source.Todos = a.Todos
		source.ConvID = c.ID
	}
	source.ProjectMemory = a.ProjectMemory
	return NewHydrationBuilder(source).Build().Messages
}

// ensureFreshRoomHydration persists the hydration checkpoint at the start of a
// turn when the conversation is on its first user message and has no
// checkpoint yet. This is the only turn-loop entry point that builds
// hydration; post-compaction checkpoints are built inside
// persistCompactedConversation. Follow-up turns, steers, and retries all
// return early (checkpoint present, or not a fresh room) so the cached prefix
// is never relocated.
func (a *App) ensureFreshRoomHydration(run *TurnRun, messageID string, caps ModelCapabilities) {
	if a.Conversations == nil {
		return
	}
	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil || conversation == nil {
		return
	}
	if HasHydration(chatMessages(conversation, messageID, caps)) {
		return
	}
	if !isFreshRoom(conversation) {
		return
	}
	hydrationMsgs := a.buildHydration(conversation)
	if len(hydrationMsgs) == 0 {
		return
	}
	conversation = a.persistHydration(conversation, hydrationMsgs)
	a.updateMessage(conversation, messageID, func(message *domain.Message) {
		message.ContextUpdated = true
	})
	_ = a.Conversations.Save(conversation)
}

// isFreshRoom reports whether the conversation is on its first user turn: at
// most one user message and no hydration checkpoint. A fresh room has exactly
// the opening user; follow-up turns and post-steer turns have two or more
// users. Post-compaction turns are excluded by the HasHydration guard in the
// caller (the checkpoint is rebuilt by persistCompactedConversation), so a
// handover-only transcript with no checkpoint still counts as fresh only when
// it has no prior user — which compaction never produces (the handover is a
// user, and the checkpoint is built in the same Save).
func isFreshRoom(c *domain.Conversation) bool {
	userCount := 0
	for _, m := range c.Messages {
		if m.Role == domain.RoleUser {
			userCount++
		}
	}
	return userCount <= 1
}

func (a *App) completeWithRetry(ctx context.Context, adapter ProviderContext, request ChatRequest) (ChatResponse, error) {
	for retry := 1; ; retry++ {
		response, err := adapter.Complete(ctx, request)
		if err == nil || retry >= maxProviderAttempts {
			return response, err
		}
		delay, retryable := providerRetryDelay(err, retry)
		if !retryable {
			return response, err
		}
		a.log("warn", "ai", "retrying provider completion (%d/%d) after %s: %v", retry, maxProviderAttempts, delay.Round(time.Millisecond), err)
		if err := a.retrySleeper(ctx, delay); err != nil {
			return ChatResponse{}, err
		}
	}
}

func (a *App) persistTurnRound(conversationID, messageID, model string, round streamedTurnRound) error {
	conversation, err := a.Conversations.Get(conversationID)
	if err != nil {
		return err
	}

	a.updateMessage(conversation, messageID, func(message *domain.Message) {
		applyStreamRound(message, model, round)
		message.Status = domain.StatusDone
	})

	newToolCalls := make([]domain.ToolCall, 0, len(round.Response.ToolCalls))
	for _, toolCall := range round.Response.ToolCalls {
		found := false
		for i := range conversation.Messages {
			if conversation.Messages[i].ID != messageID {
				continue
			}
			for _, tc := range conversation.Messages[i].ToolCalls {
				if tc.ID == toolCall.ID {
					found = true
					break
				}
			}
		}
		if found {
			continue
		}
		for i := range conversation.Messages {
			if conversation.Messages[i].ID == messageID {
				conversation.Messages[i].ToolCalls = append(conversation.Messages[i].ToolCalls, toolCall)
				break
			}
		}
		newToolCalls = append(newToolCalls, toolCall)
	}
	if len(newToolCalls) > 0 {
		a.updateMessage(conversation, messageID, func(message *domain.Message) {
			message.Steps = append(message.Steps, domain.MessageStep{Type: domain.StepToolCalls, ToolCalls: newToolCalls})
		})
	}
	return a.Conversations.Save(conversation)
}

func applyStreamRound(message *domain.Message, model string, round streamedTurnRound) {
	if round.Reasoning != "" {
		message.Steps = append(message.Steps, domain.MessageStep{Type: domain.StepReasoning, Content: round.Reasoning})
		message.Reasoning = round.Reasoning
	}
	if content := persistableText(round.Content); content != "" {
		message.Steps = append(message.Steps, domain.MessageStep{Type: domain.StepText, Content: content})
		message.Content = content
	}
	message.Model = model
	message.Usage = toDomainUsage(round.Response.Usage)
}

// publishRoundDelta stages a live round delta into the in-memory round
// stream registry (SSE /stream). The registry is process-local and
// drop-tolerant: consumers re-open with after=<seq> to replay missed frames.
func (a *App) publishRoundDelta(runID, messageID string, round int, kind, toolCallID, name, text string) {
	if a.RoundStreams != nil {
		a.RoundStreams.Publish(runID, messageID, round, kind, toolCallID, name, text)
	}
}

func (a *App) publishRoundToolStart(runID, messageID string, round int, toolCallID, name string, presentation *contracts.ToolPresentationDTO) {
	if a.RoundStreams != nil {
		a.RoundStreams.PublishWithPresentation(runID, messageID, round, contracts.RoundDeltaTool, toolCallID, name, "", presentation)
	}
}

// visibleText is the assistant text worth persisting or sending. Models such
// as Qwen3.8 emit a blank paragraph ("\n\n") as the first/last content tokens
// after thinking; storing that makes empty rounds look like real turns.
func visibleText(s string) string {
	return strings.TrimSpace(s)
}

// persistableText is the content stored for a streamed round. Whitespace-only
// rounds are dropped entirely (visibleText's job), but real content keeps its
// trailing space: a stream cut mid-sentence ends on a word boundary that the
// continuation round needs so the two halves do not run together
// ("here.And"). Leading whitespace (including blank paragraphs) carries no
// meaning and is trimmed; on the trailing side only newlines are stripped.
func persistableText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	left := strings.TrimLeftFunc(s, unicode.IsSpace)
	return strings.TrimRight(left, "\r\n")
}

// toolExecResult is one tool's outcome from the concurrent execution phase,
// held until results are persisted in tool-call order.
type toolExecResult struct {
	status          domain.ToolCallStatus
	output          string
	atts            []domain.Attachment
	learningNodeIDs []string
}

func (a *App) executeTurnTools(run *TurnRun, messageID string, toolCalls []domain.ToolCall, caps ModelCapabilities, settings domain.Settings, round int) error {
	if err := run.Ctx.Err(); err != nil {
		if len(toolCalls) > 0 {
			conversation, gerr := a.Conversations.Get(run.ConversationID)
			if gerr == nil {
				for _, toolCall := range toolCalls {
					a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
						RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID,
						Name: toolCall.Name, Status: string(domain.ToolInterrupted), Output: "interrupted by user",
						Presentation: buildToolPresentation(toolCall.Name, toolCall.Args, domain.ToolInterrupted, "interrupted by user"),
					})
					conversation = a.updateToolResult(conversation, messageID, toolCall.ID, domain.ToolInterrupted, "interrupted by user", nil)
				}
				_ = a.Conversations.Save(conversation)
			}
		}
		return err
	}

	// Phase 1: execute all tool calls concurrently (bounded). Only tool
	// execution and event emission happen here — no conversation-store writes —
	// so each backing store's own lock is sufficient and there are no
	// read-modify-write races on the conversation snapshot. Results are kept in
	// tool-call order for deterministic persistence in phase 2.
	results := make([]toolExecResult, len(toolCalls))
	var wg sync.WaitGroup
	limit := settings.MaxParallelTools
	if limit < 1 {
		limit = defaultMaxParallelTools
	}
	sem := make(chan struct{}, limit)
	for i := range toolCalls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = a.runOneTool(run, messageID, toolCalls[i], caps, settings, round)
		}(i)
	}
	wg.Wait()

	// Phase 2: persist results in tool-call order and emit todo updates. This
	// runs on the single turn goroutine, so conversation snapshot writes never
	// race with each other.
	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return err
	}
	for i := range toolCalls {
		toolCall := toolCalls[i]
		r := results[i]
		conversation = a.updateToolResult(conversation, messageID, toolCall.ID, r.status, r.output, r.atts)
		// When the model updates the todo checklist, emit a dedicated event so
		// the UI can re-render the strip without polling agent.todos.get.
		if toolCall.Name == "todo" && r.status == domain.ToolOK && a.Todos != nil {
			items := a.Todos.Get(run.ConversationID)
			dtos := make([]contracts.TodoItemDTO, 0, len(items))
			for _, item := range items {
				dtos = append(dtos, contracts.TodoItemDTO{ID: item.ID, Content: item.Content, Status: string(item.Status)})
			}
			summary := domain.SummarizeTodos(items)
			a.Bus.Emit(contracts.EventTodoUpdated, contracts.TodoUpdatedEvent{
				ConversationID: run.ConversationID,
				Items:          dtos,
				Summary:        contracts.TodoSummaryDTO{Total: summary.Total, Pending: summary.Pending, InProgress: summary.InProgress, Completed: summary.Completed},
				Brief:          a.Todos.GetBrief(run.ConversationID),
			})
		}
	}
	// A brief change no longer strips the hydration checkpoint. The
	// checkpoint's todo_list brief is frozen until the next compaction epoch;
	// the agent can call todo/todo_list live. Stripping + rebuilding relocated
	// the checkpoint after whatever user was last (the cache-poison dump),
	// invalidating the prompt-cache prefix from the hydration byte onward.
	if saveErr := a.Conversations.Save(conversation); saveErr != nil {
		return saveErr
	}
	for i := range results {
		if results[i].status == domain.ToolOK {
			a.recordLearningTurnNodes(run, results[i].learningNodeIDs)
		}
	}
	if err := run.Ctx.Err(); err != nil {
		return err
	}
	return nil
}

// runOneTool executes a single tool call and returns its result. It emits the
// tool-started and tool-completed events and never writes to the conversation
// store, so it is safe to run concurrently for the tool calls of one round.
func (a *App) runOneTool(run *TurnRun, messageID string, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings, round int) toolExecResult {
	a.Bus.Emit(contracts.EventToolStarted, contracts.ToolStartedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID, Name: toolCall.Name, Args: []byte(toolCall.Args),
		Presentation: buildToolPresentation(toolCall.Name, toolCall.Args, domain.ToolRunning, ""),
	})
	a.log("info", "tools", "tool call: %s", toolCall.Name)

	// If the turn was already cancelled, do not start the tool — mark it
	// interrupted (mirrors the pre-parallel behavior of skipping remaining
	// tools after cancellation).
	if run.Ctx.Err() != nil {
		res := toolExecResult{status: domain.ToolInterrupted, output: "interrupted by user"}
		a.emitToolCompleted(run, toolCall, res)
		return res
	}

	var output string
	var outputAttachments []domain.Attachment
	var err error
	switch toolCall.Name {
	case "read_media":
		kind, sniffErr := mediaread.SniffMediaKind([]byte(toolCall.Args))
		if sniffErr != nil {
			output = "error: " + sniffErr.Error()
			err = sniffErr
		} else {
			switch kind {
			case "image":
				output, outputAttachments, err = a.executeReadImage(run, toolCall, caps, settings)
			case "audio":
				output, outputAttachments, err = a.executeReadAudio(run, toolCall, caps, settings)
			case "video":
				output, outputAttachments, err = a.executeReadVideo(run, toolCall, caps, settings)
			case "document":
				output, outputAttachments, err = a.executeReadDocument(run, toolCall, caps, settings)
			default:
				output = "error: unrecognized media type"
				err = fmt.Errorf("unrecognized media type")
			}
		}
	case "generate_media", "generate_image", "generate_speech", "generate_video":
		output, outputAttachments, err = a.executeGenerateMedia(run, toolCall, settings)
	default:
		toolCtx := WithConversationID(run.Ctx, run.ConversationID)
		toolCtx = WithWorkspace(toolCtx, run.Workspace)
		toolCtx = WithRunID(toolCtx, run.ID)
		toolCtx = WithToolCallID(toolCtx, toolCall.ID)
		toolPresentation := buildToolPresentation(toolCall.Name, toolCall.Args, domain.ToolRunning, "")
		executeTool := func() error {
			if s, ok := a.Toolbox.(interface {
				ExecuteStreamed(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (string, error)
			}); ok {
				a.publishRoundToolStart(run.ID, messageID, round, toolCall.ID, toolCall.Name, toolPresentation)
				var e error
				output, e = s.ExecuteStreamed(toolCtx, toolCall.Name, []byte(toolCall.Args), func(text string) {
					if text != "" {
						a.publishRoundDelta(run.ID, messageID, round, contracts.RoundDeltaTool, toolCall.ID, toolCall.Name, text)
					}
				})
				return e
			}
			var e error
			output, e = a.Toolbox.Execute(toolCtx, toolCall.Name, []byte(toolCall.Args))
			return e
		}
		req := ClassifyMutation(toolCall.Name, []byte(toolCall.Args))
		if a.Journal != nil && req.Class != domain.MutationNone {
			req.ConversationID = run.ConversationID
			req.RunID = run.ID
			req.ToolCallID = toolCall.ID
			req.WorkspaceRoot = run.Workspace
			root := req.Cwd
			if root == "" {
				root = req.WorkspaceRoot
			}
			if root == "" {
				root = "\x00journal"
			}
			mu := a.rootMutationLock(root)
			mu.Lock()
			err = a.Journal.WrapMutation(toolCtx, req, executeTool)
			mu.Unlock()
		} else {
			err = executeTool()
		}
	}
	status := domain.ToolOK
	if err != nil {
		// Interrupted streaming tools keep the partial output received so
		// far (the executor returns it with the cancellation error), so the
		// persisted tool call still shows the streamed lines after a reload.
		if run.Ctx.Err() != nil && strings.Contains(err.Error(), "partial output") {
			status = domain.ToolInterrupted
			output = err.Error()
		} else if run.Ctx.Err() != nil {
			status = domain.ToolInterrupted
			output = "interrupted by user"
		} else {
			status = domain.ToolFailed
			output = "error: " + truncateToolError(err.Error())
		}
	}
	// Async subagent: the tool returns immediately with "starting" status.
	// Keep the tool call marked as running so the UI shows a spinner; the
	// OnDone callback will update it to ok/fail with the summary when the
	// subagent finishes.
	if toolCall.Name == "subagent" && err == nil {
		status = domain.ToolRunning
	}
	res := toolExecResult{status: status, output: output, atts: outputAttachments}
	if status == domain.ToolOK {
		res.learningNodeIDs = learningNodeIDsFromTool(a, toolCall, output)
	}
	a.emitToolCompleted(run, toolCall, res)
	a.emitLearningMutationEvents(toolCall.Name, status)
	// Skill nudge: count tool calls per conversation so tool-heavy but
	// user-turn-light coding sessions trigger skill review independently
	// of the turn threshold.
	if !run.Headless && (status == domain.ToolOK || status == domain.ToolFailed) {
		a.incrementToolCallCounter(run.ConversationID)
	}
	return res
}

func (a *App) emitToolCompleted(run *TurnRun, toolCall domain.ToolCall, res toolExecResult) {
	event := contracts.ToolCompletedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID,
		Name: toolCall.Name, Status: string(res.status), Output: res.output,
		Presentation: buildToolPresentation(toolCall.Name, toolCall.Args, res.status, res.output),
	}
	for _, att := range res.atts {
		event.Attachments = append(event.Attachments, contracts.AttachmentDTO{
			Type: att.Type, Name: att.Name, MediaType: att.MediaType, FilePath: att.FilePath,
		})
	}
	a.Bus.Emit(contracts.EventToolCompleted, event)
}

// emitLearningMutationEvents publishes memory.updated and/or skill.updated
// events when a tool mutates the memory or skill stores, so the Learning UI
// can refresh its memory list, search results, and graph in real time without
// polling. Only successful tool calls trigger the events.
func (a *App) emitLearningMutationEvents(toolName string, status domain.ToolCallStatus) {
	if a.Bus == nil || status != domain.ToolOK {
		return
	}
	// Route by dispatcher root; any successful family op refreshes the
	// Learning UI panes in real time.
	switch toolName {
	case "memory":
		a.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{
			"source": "tool",
			"tool":   toolName,
		})
	case "skill":
		a.Bus.Emit(contracts.EventSkillUpdated, map[string]any{
			"source": "tool",
			"tool":   toolName,
		})
	}
}

func (a *App) appendTurnAssistant(conversationID string) (*domain.Conversation, string, error) {
	conversation, err := a.Conversations.Get(conversationID)
	if err != nil {
		return nil, "", err
	}
	next := domain.Message{ID: domain.NewID("msg"), Role: domain.RoleAssistant, CreatedAt: time.Now().UTC()}
	conversation.AddMessage(next)
	if err := a.Conversations.Save(conversation); err != nil {
		return nil, "", err
	}
	return conversation, next.ID, nil
}

func (a *App) finishTurn(run *TurnRun, messageID, model string, usage ChatUsage, contextTokens, autoContinueIndex int) error {
	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return err
	}
	conversation.Status = "idle"
	// Record the authoritative provider-measured context fill (last round's
	// input + cached input + output) as the source of truth for the idle
	// badge. Providers that report no usage leave it at zero, and the UI
	// falls back to the heuristic EstimatedTokens.
	if contextTokens > 0 {
		conversation.ContextTokens = int64(contextTokens)
	}
	conversation.Touch()
	if err := a.Conversations.Save(conversation); err != nil {
		return err
	}

	// Compute the outer multi-turn auto-continue decision. Only attached
	// when a todo port is configured; failed/cancelled paths omit it.
	var autoContinue *contracts.AutoContinueDTO
	if a.Todos != nil {
		items := a.Todos.Get(run.ConversationID)
		decision := domain.DecideAutoContinue(domain.AutoContinueInput{
			Items:             items,
			AutoContinueIndex: autoContinueIndex,
			MaxAutoContinues:  a.Settings.Get().MaxAutoContinues,
			TurnOK:            true,
			HasConversation:   true,
			HasBackgroundJobs: a.hasPendingSubagents(run.ConversationID),
		})
		autoContinue = &contracts.AutoContinueDTO{
			ShouldContinue:   decision.ShouldContinue,
			OpenTodoCount:    decision.OpenTodoCount,
			ContinuesUsed:    decision.ContinuesUsed,
			MaxAutoContinues: decision.MaxAutoContinues,
			Reason:           string(decision.Reason),
		}
	}

	a.Bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: messageID, Model: model,
		Usage: &contracts.UsageDTO{
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite,
		},
		ContextTokens: contextTokens,
		AutoContinue:  autoContinue,
	})
	a.log("info", "agent", "turn finished: %s (in %d / out %d)", run.ID, usage.InputTokens, usage.OutputTokens)

	// Threshold-based learning review: accumulate turns since the last
	// review and only fire when the threshold is reached. This avoids
	// burning extraction cycles on short "hi/thanks" exchanges. The
	// compaction hook (subscribeCompactionReview) fires independently
	// when a conversation is compacted, so learning still happens for
	// long conversations that compact before reaching the threshold.
	// Pipeline agent steps are unattended automation, not user rooms.
	if !run.Headless {
		a.incrementTurnCounter(conversation.ID)
	}

	return nil
}

// incrementTurnCounter bumps the per-conversation turn counter and
// triggers a learning review if the threshold is reached. The threshold
// is read from settings (default 10, 0 disables turn-based review).
// Counters are persisted to disk so they survive server restarts.
func (a *App) incrementTurnCounter(conversationID string) {
	if a.ReviewAgent == nil {
		return
	}
	threshold := a.Settings.Get().LearningReviewThreshold
	if threshold <= 0 {
		return // turn-based review disabled
	}
	a.learningMu.Lock()
	a.turnsSinceReview[conversationID]++
	count := a.turnsSinceReview[conversationID]
	a.learningMu.Unlock()
	// Persist so the counter survives restarts.
	a.saveTurnCounters()
	if count < threshold {
		return
	}
	a.flushLearningReview(conversationID, "threshold")
}

// incrementToolCallCounter bumps the per-conversation tool-call counter and
// triggers a learning review if the skill-nudge threshold is reached. This
// catches skill-worthy patterns in tool-heavy but user-turn-light coding
// sessions that would never reach the turn threshold. The threshold is read
// from settings (default 15, 0 disables tool-based review). Counters are
// persisted to disk so they survive server restarts.
func (a *App) incrementToolCallCounter(conversationID string) {
	if a.ReviewAgent == nil {
		return
	}
	threshold := a.Settings.Get().SkillNudgeInterval
	if threshold <= 0 {
		return // tool-based review disabled
	}
	a.learningMu.Lock()
	a.toolCallsSinceReview[conversationID]++
	count := a.toolCallsSinceReview[conversationID]
	a.learningMu.Unlock()
	a.saveTurnCounters()
	if count < threshold {
		return
	}
	a.flushLearningReview(conversationID, "skill_nudge")
}

// flushLearningReview resets the turn counter for a conversation and
// fires a background LLM review over the recent transcript. Called when
// the threshold is reached or when a compaction event fires (whichever
// comes first). The review uses the conversation's configured model
// (the "global LLM") with a restricted toolset and the review
// prompt. It is fire-and-forget — it never blocks or fails the parent
// turn.
func (a *App) flushLearningReview(conversationID string, reason string) {
	if a.ReviewAgent == nil {
		return
	}
	// Reset the counters on every trigger attempt, including deferred ones.
	// The counters measure "activity since the last trigger attempt"; once an
	// attempt is made (even if rejected by cooldown/in-flight), the activity
	// is acknowledged. Without this reset the counters stay at/above the
	// threshold so every subsequent tool call/turn re-enters this function,
	// flooding the learning log with "review triggered" events for the whole
	// cooldown window. A deferred trigger still leaves the pending flag set
	// inside acquireReview, so activity that arrived while a review was
	// running is picked up by the coalesced follow-up after release.
	a.learningMu.Lock()
	a.turnsSinceReview[conversationID] = 0
	a.toolCallsSinceReview[conversationID] = 0
	a.learningMu.Unlock()
	a.saveTurnCounters()
	// Reserve synchronously before launching the worker. Rejected triggers
	// log "review deferred" (coalesced) inside reserveReviewWithReason; only a
	// trigger that wins the slot logs "review triggered" and launches work.
	if !a.ReviewAgent.reserveReview(conversationID) {
		return
	}
	a.log("info", "learning", "review triggered: conv=%s reason=%s", conversationID, reason)
	a.goSafe("learning", func() {
		if a.Bus != nil {
			a.Bus.Emit(contracts.EventLearningReviewStarted, contracts.LearningReviewEvent{
				ConversationID: conversationID,
				Status:         "started",
				Reason:         reason,
			})
		}
		err := a.ReviewAgent.runReservedReview(context.Background(), conversationID)
		if err != nil {
			a.ReviewAgent.recordReviewError(conversationID, err.Error())
			if a.Bus != nil {
				a.Bus.Emit(contracts.EventLearningReviewError, contracts.LearningReviewEvent{
					ConversationID: conversationID,
					Status:         "error",
					Error:          err.Error(),
				})
			}
			return
		}
		if a.Bus != nil {
			a.Bus.Emit(contracts.EventLearningReviewDone, contracts.LearningReviewEvent{
				ConversationID: conversationID,
				Status:         "done",
				Reason:         reason,
			})
		}
	})
}

// truncateToolError prevents oversized error messages from wasting tokens
// when a provider returns an error that embeds base64 media data (e.g.
// "Failed to load image from data:audio/mpeg;base64,//NkxAAAA..."). Such
// errors can be 1.5MB+ and pollute the conversation history, causing every
// subsequent request to fail with HTTP 400. The error is truncated to a
// reasonable length while preserving the diagnostic prefix.
const maxToolErrorLen = 2000

func truncateToolError(msg string) string {
	if len(msg) <= maxToolErrorLen {
		return msg
	}
	return msg[:maxToolErrorLen] + "...[truncated: original error was " +
		strconv.Itoa(len(msg)) + " chars]"
}
