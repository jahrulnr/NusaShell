package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"nusashell/domain"
	"nusashell/pkg/nonce"
	clock "nusashell/pkg/time"
)

func reasoningDeltaVisible(accumulated string) bool {
	return strings.TrimSpace(accumulated) != ""
}

// userNudgeText is the minimal content injected as a synthetic user message
// when the provider requires a user message but none is present. A single
// "." is the smallest valid user turn that satisfies the constraint without
// adding semantic content the model would act on.
const userNudgeText = domain.UserNudgeText

// hasUserMessage reports whether the messages slice contains at least one
// message with Role "user". It is kept separate from
// needsUserMessageAtEnd because a provider may require the final role to be
// user even when an earlier user message exists.
func hasUserMessage(messages []ChatMessage) bool {
	for _, m := range messages {
		if m.Role == "user" {
			return true
		}
	}
	return false
}

// needsUserMessageAtEnd reports whether a learned provider constraint needs a
// synthetic user turn appended to the request. A tool result is already the
// valid final turn of an active tool cycle, so it must not be followed by a
// synthetic user message. Requests with no user at all preserve the older
// nudge behavior.
func needsUserMessageAtEnd(messages []ChatMessage) bool {
	if !hasUserMessage(messages) {
		return true
	}
	if len(messages) == 0 {
		return true
	}
	return messages[len(messages)-1].Role == "assistant"
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
	threshold := domain.ServerCompactionThreshold(window)
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

// appendContinuationFromPartial injects the partial content from a
// prematurely cut stream as an ephemeral assistant message, then appends the
// continuation announcement tool. The model sees the partial text it already
// produced and the "continue from where you stopped" instruction, so it
// resumes the interrupted response without repeating prior text.
//
// This is the automatic-retry variant of appendContinuationTool: it carries
// the partial content that was streamed but never persisted (the turn is
// still in progress). Manual retries use appendContinuationTool directly
// because the partial content is already in the conversation store.
func appendContinuationFromPartial(messages []ChatMessage, partial streamedTurnRound) []ChatMessage {
	msg := ChatMessage{Role: "assistant"}
	if partial.Content != "" {
		msg.Content = partial.Content
	}
	if partial.Reasoning != "" {
		msg.Reasoning = partial.Reasoning
	}
	messages = append(messages, msg)
	return appendContinuationTool(messages)
}

// estimateRequestTokens approximates provider tokens from the real request
// payload: system + messages + tools JSON. ~4 chars/token with a surcharge
// for non-ASCII (CJK-ish) characters, ~4 tokens per-message overhead, ~150
// tokens per image attachment, plus a 5% safety buffer.
func estimateRequestTokens(system string, messages []ChatMessage, tools []ToolDef) int64 {
	chars := int64(len(system))
	totalOverhead := int64(domain.RequestTokenPerMessageOverhead * len(messages))
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
	chars += int64(images * domain.RequestTokenImageCost)
	tokens := (chars + totalOverhead) / int64(domain.RequestTokenCharsPerToken)
	return int64(float64(tokens) * domain.RequestTokenSafetyBuffer)
}

const (
	promptCacheKeyLength          = domain.PromptCacheKeyLength
	promptCacheConversationPrefix = domain.PromptCacheConversationPrefix
	promptCacheBackgroundPrefix   = domain.PromptCacheBackgroundPrefix
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

// buildHydrationDomainMessages converts synthetic hydration ChatMessages
// into a single persisted assistant message with tool outputs attached.
func buildHydrationDomainMessages(msgs []ChatMessage) []domain.Message {
	built := make([]domain.Message, 0, len(msgs))
	var hyd *domain.Message
	for _, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			hyd = &domain.Message{
				ID:        domain.NewID(domain.IDPrefixMsg),
				Role:      domain.RoleAssistant,
				ToolCalls: m.ToolCalls,
				Status:    domain.StatusDone,
				CreatedAt: clock.NewTime().Time(),
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
	return built
}

// persistHydration inserts the synthetic hydration messages (assistant
// toolCalls + matching tool results) immediately after the FIRST user
// message in the transcript. If there is no user yet the conversation is
// left unchanged — an assistant+tool prefix under the system prompt is
// invalid for OpenAI Chat Completions and Anthropic Messages.
//
// Used only to shape an in-memory post-compaction transcript before
// ResetTranscript+Add. Live rooms append hydration in addTurnMessages.
func (a *App) persistHydration(c *domain.Conversation, msgs []ChatMessage) *domain.Conversation {
	built := buildHydrationDomainMessages(msgs)
	if len(built) == 0 {
		return c
	}
	idx := domain.HydrationInsertIndex(c.Messages)
	if idx < 0 {
		return c
	}
	c.Messages = slices.Insert(c.Messages, idx, built...)
	return c
}

// buildHydration assembles a synthetic runtime-hydration checkpoint from the
// App's read-only stores when the current history epoch does not already have
// one, normally on the initial turn or immediately after compaction.
func (a *App) buildHydration(c *domain.Conversation) []ChatMessage {
	ctx := DefaultRuntimeContext(c.Workspace)
	ctx.DataDir = a.DataDir
	// The runtime context slot also carries the active background/async tool
	// runs so the model always knows which subagents/delegates were spawned
	// and are still pending (fed into the compaction re-hydration too).
	ctx.BackgroundRuns = a.pendingBackgroundRuns(c.ID)
	source := HydrationSource{
		RuntimeContext: ctx,
		ConvID:         c.ID,
	}
	if a.Toolbox != nil {
		// The real toolbox executes the meta-tools (mcp_list, tool_list per
		// server, skill, file_read) so the checkpoint contains genuine
		// tool output — the same tools the agent calls.
		source.Executor = a.Toolbox
	}
	if a.User != nil {
		source.UserPath = a.User.Path()
	}
	if a.Agent != nil {
		source.AgentPath = a.Agent.Path()
	}
	if a.Todos != nil {
		source.Todos = a.Todos
		source.ConvID = c.ID
	}
	source.ProjectMemory = a.ProjectMemory
	return NewHydrationBuilder(source).Build().Messages
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
		if err := a.waitForRetry(ctx, delay); err != nil {
			return ChatResponse{}, err
		}
	}
}
