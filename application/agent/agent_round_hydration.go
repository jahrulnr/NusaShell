package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"nusashell/application/provider"
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

func serverCompactionContextManagementForKind(model string, kind domain.ProviderKind) []map[string]any {
	if domain.CodexSupportsRemoteCompaction(kind) {
		return nil
	}
	return serverCompactionContextManagement(model)
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
	if len(partial.Response.ReasoningExtra) > 0 {
		msg.ReasoningExtra = append(json.RawMessage(nil), partial.Response.ReasoningExtra...)
	}
	messages = append(messages, msg)
	return appendContinuationTool(messages)
}

// estimateRequestTokens keeps the old narrow test/helper API while routing it
// through the provider boundary. Production turns use the full ChatRequest
// estimator in application/provider so provider kind, reasoning replay, and
// compaction items are included in the same calculation as the sent request.
func estimateRequestTokens(system string, messages []ChatMessage, tools []ToolDef) int64 {
	return provider.EstimateRequestTokens(provider.ChatRequest{
		System:   system,
		Messages: messages,
		Tools:    tools,
	}, domain.ProviderChat, false)
}

const (
	promptCacheConversationPrefix = domain.PromptCacheConversationPrefix
	promptCacheBackgroundPrefix   = domain.PromptCacheBackgroundPrefix
)

func promptCachePrefixForRun(run *TurnRun) string {
	if run != nil && run.Headless {
		return promptCacheBackgroundPrefix
	}
	return promptCacheConversationPrefix
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
func (a *Service) PersistHydration(c *domain.Conversation, msgs []ChatMessage) *domain.Conversation {
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
func (a *Service) BuildHydration(c *domain.Conversation) []ChatMessage {
	ctx := DefaultRuntimeContext(a.effectiveWorkspace(c.Workspace))
	ctx.DataDir = a.DataDir
	ctx.InstructionFiles = listInstructionFiles(c.Workspace)
	// The runtime context slot also carries the active background/async tool
	// runs so the model always knows which subagents/delegates were spawned
	// and are still pending (fed into the compaction re-hydration too).
	ctx.BackgroundRuns = a.PendingBackgroundRuns(c.ID)
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
	// Background learner turns get the skill-creator authoring reference
	// forced into the checkpoint as a direct file_read slot: evaluate/evolve
	// (Stage 2/3) must follow it without spending tool rounds searching for
	// it. Live store first, embedded bundle as the guaranteed fallback.
	if c.Type == domain.ConversationTypeBackground {
		source.SkillCreatorPath, source.SkillCreatorContent = a.learnerSkillCreatorReference()
	}
	if a.Todos != nil {
		source.Todos = a.Todos
		source.ConvID = c.ID
	}
	source.ProjectMemory = a.ProjectMemory
	if a.MemoryRecords != nil {
		source.ApplyBlock = domain.BuildApplyBlock(a.MemoryRecords.List(), domain.ApplyBlockTokenCap)
	}
	return NewHydrationBuilder(source).Build().Messages
}

func (a *Service) CompleteWithRetry(ctx context.Context, adapter ProviderContext, request ChatRequest) (ChatResponse, error) {
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
