package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"nusashell/domain"
	"nusashell/pkg/text"
	"nusashell/resources"
)

func mutationSnippet(argsJSON, field string) string {
	var args map[string]json.RawMessage
	if json.Unmarshal([]byte(argsJSON), &args) != nil {
		return ""
	}
	raw, ok := args[field]
	if !ok {
		return ""
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	v = strings.TrimSpace(v)
	const max = 200
	if len(v) > max {
		v = v[:max] + "…"
	}
	return v
}

// runReviewLoop executes the review loop: send transcript → get tool calls →
// execute whitelisted tools → feed results back → repeat until the model is
// terminal. Returns
// the mutations and the full message history (the review agent's own
// conversation with the LLM) so it can be persisted for the learning log.
func (r *BackgroundReviewAgent) runReviewLoop(ctx context.Context, adapter ProviderContext, model string, conversation *domain.Conversation) ([]ReviewMutation, []ChatMessage, error) {
	systemPrompt := resources.ReviewPrompt()
	if systemPrompt == "" {
		return nil, nil, nil
	}
	if len(conversation.Messages) == 0 {
		return nil, nil, nil
	}

	// Compute the incremental message range [start:end) since the last
	// review. LastReviewedMsgCount is the persistent marker — each review
	// only processes messages from that index onward, avoiding re-reading
	// and re-reasoning over already reviewed content.
	start := conversation.LastReviewedMsgCount
	if start < 0 {
		start = 0
	}
	allMsgs := r.transcriptMessages(conversation)
	end := len(allMsgs)
	if start > end {
		// Compaction shrank the merged array (transcriptMessages only
		// includes the latest chunk, not all chunks). Reset to review
		// from the beginning of the new array — compaction is a natural
		// checkpoint, and the chunk content deserves a fresh pass since
		// the compaction summary itself is new signal.
		start = 0
	}
	if start >= end {
		// No new messages since last review — nothing to do.
		return nil, nil, nil
	}

	// Keep the nodes inspected or changed by this review together so the
	// resulting used_with edges describe one review turn, even when the LLM
	// needs several tool rounds. Seeded by the pre-injected file_read and
	// grown by the review rules during the engine run.
	var reviewLearningIDs []string

	// Resolve the conversation JSON file path so the agent can file_read
	// the full conversation if the bounded segment lacks context.
	convPath := ""
	if r.app != nil && r.app.Conversations != nil {
		if store, ok := r.app.Conversations.(interface{ Path(string) string }); ok {
			convPath = store.Path(conversation.ID)
		}
	}

	tools := r.reviewTools()

	// Build the opening message sequence:
	//   user:    user/review.md (imperative instruction)
	//   assistant: synthetic tool_call(review_transcript) [+ tool_call(file_read)]
	//   tool:    review_transcript result (bounded segment)
	//   tool:    file_read result (primary.md content, when available)
	//
	// Pre-injecting the tool results means the agent starts with both the
	// transcript segment and the current primary memory as factual ground
	// truth (tool output authority > system prompt authority). The agent
	// does not need to call these tools — their results are already in the
	// message stream.
	userPrompt := resources.ReviewUserPrompt()
	if userPrompt == "" {
		userPrompt = "Review this conversation and manage your memories."
	}

	transcriptOutput := r.executeReviewTranscript(conversation, start, end, convPath)

	reviewTCID := "synthetic_review_transcript"
	toolCalls := []domain.ToolCall{
		{ID: reviewTCID, Name: reviewTranscriptToolName, Args: fmt.Sprintf(`{"path":%q,"messages_start":%d,"messages_end":%d}`, convPath, start, end)},
	}
	toolResults := []ChatMessage{
		{
			Role: "tool",
			ToolResult: &ToolResult{
				ToolCallID: reviewTCID,
				Name:       reviewTranscriptToolName,
				Content:    transcriptOutput,
			},
		},
	}

	// Primary memory: pre-inject as a real file_read call so the agent
	// sees the actual primary.md file (including frontmatter) as ground
	// truth. Uses the real Toolbox.Execute so the output format is
	// identical to what the agent would get calling file_read itself.
	if r.app.Primary != nil && r.app.Primary.Path() != "" {
		primaryPath := r.app.Primary.Path()
		fileReadArgs := fmt.Sprintf(`{"path":%q}`, primaryPath)
		fileReadTCID := "synthetic_file_read_primary"
		output, err := r.app.Toolbox.Execute(ctx, "file_read", []byte(fileReadArgs))
		if err != nil {
			output = "(primary memory file unavailable: " + err.Error() + ")"
		} else {
			reviewLearningIDs = append(reviewLearningIDs, learningNodeIDsFromTool(r.app, domain.ToolCall{
				Name: "file_read",
				Args: fileReadArgs,
			}, output)...)
		}
		toolCalls = append(toolCalls, domain.ToolCall{ID: fileReadTCID, Name: "file_read", Args: fileReadArgs})
		toolResults = append(toolResults, ChatMessage{
			Role: "tool",
			ToolResult: &ToolResult{
				ToolCallID: fileReadTCID,
				Name:       "file_read",
				Content:    output,
			},
		})
	}

	messages := []ChatMessage{
		{Role: "user", Content: userPrompt},
		{Role: "assistant", ToolCalls: toolCalls},
	}
	messages = append(messages, toolResults...)

	reviewSettings := domain.Settings{}
	if r.app != nil && r.app.Settings != nil {
		reviewSettings = r.app.Settings.Get()
	}
	promptCache := buildPromptCachePolicyForContext(reviewSettings, adapter, model, conversation.ID, promptCacheBackgroundPrefix)
	// The review tool loop (stream → whitelisted/local tools → repeat)
	// is the AgentEngine with the AgentReview rule set
	// (review_agent_rules.go): virtual conversation, mutation tracking,
	// and the "Nothing to save" early exit. The engine runs without an
	// artificial tool-round cap, like a conversation turn.
	pr := &reviewAgentRules{
		agent:       r,
		adapter:     adapter,
		model:       model,
		conv:        conversation,
		start:       start,
		end:         end,
		convPath:    convPath,
		tools:       tools,
		base:        messages,
		settings:    reviewSettings,
		promptC:     promptCache,
		ctx:         ctx,
		learningIDs: reviewLearningIDs,
	}
	defer func() {
		if r.app != nil {
			r.app.recordLearningUsage(pr.learningIDs)
		}
	}()
	st, loopErr := (&AgentEngine{}).Run(ctx, pr.rules(), 0)
	// The returned history is the full review conversation: the
	// pre-injected opening (user prompt + synthetic tool results) plus
	// everything the engine appended across rounds.
	full := make([]ChatMessage, 0, len(messages)+len(st.Messages))
	full = append(full, messages...)
	full = append(full, st.Messages...)
	if loopErr != nil {
		return pr.mutations, full, loopErr
	}
	return pr.mutations, full, nil
}

// isNothingToSave reports whether the LLM's response signals that the
// review found no durable knowledge worth saving. The review prompt
// instructs the agent to respond with exactly "Nothing to save." when
// there is nothing to persist, but models sometimes add trailing
// whitespace, punctuation, or a short explanation. We treat the response
// as a no-save signal when the canonical phrase appears (case-insensitive)
// and no tool calls were made.
func isNothingToSave(content string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(content)), "nothing to save")
}

// reviewTools returns the restricted tool definitions for the review agent.
// The synthetic review_transcript tool is listed first so the LLM knows its
// schema, but its result is pre-injected by runReviewLoop before the first
// LLM call — the agent does not need to call it to get the initial data.
// Whitelisted tools (memory, skill, file_read) come from the Toolbox.
func (r *BackgroundReviewAgent) reviewTools() []ToolDef {
	if r.app == nil || r.app.Toolbox == nil {
		return nil
	}
	return toolFactoryFor(r.app).Get(AgentReview, "")
}

// transcriptMessages returns the messages to review. Compaction retains real
// user messages in the live transcript (they carry the user's goal) but
// archives non-user messages (assistant responses, tool calls) that exceed
// the keep-token budget. This means the chunk and live slices are
// temporally interleaved, not contiguous — naively prepending the chunk
// produces assistant responses before the user messages that triggered them.
// To preserve the real conversation flow, chunk and live messages are merged
// by CreatedAt timestamp. Falls back to the live messages alone when no
// store is configured or the chunk is unavailable.
func (r *BackgroundReviewAgent) transcriptMessages(c *domain.Conversation) []domain.Message {
	msgs := c.Messages
	if r.app == nil || r.app.Conversations == nil || c.ChunkCount <= 0 {
		return msgs
	}
	chunk, err := r.app.Conversations.GetChunk(c.ID, c.ChunkCount-1)
	if err != nil || len(chunk) == 0 {
		return msgs
	}
	merged := make([]domain.Message, 0, len(chunk)+len(msgs))
	merged = append(merged, chunk...)
	merged = append(merged, msgs...)
	// Sort by CreatedAt to restore chronological order. Compaction retains
	// user messages in live and archives assistant messages to chunks, so
	// without sorting the merged array has responses before questions.
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].CreatedAt.Before(merged[j].CreatedAt)
	})
	return merged
}

// transcriptMessageJSON is one message in the structured JSON transcript
// returned by the review_transcript hydration tool.
type transcriptMessageJSON struct {
	Role      string                   `json:"role"`
	Content   string                   `json:"content"`
	ToolCalls []transcriptToolCallJSON `json:"tool_calls,omitempty"`
}

// transcriptToolCallJSON is one tool call nested inside an assistant message.
type transcriptToolCallJSON struct {
	Name   string `json:"name"`
	Args   string `json:"args,omitempty"`
	Output string `json:"output,omitempty"`
}

// transcriptEnvelope is the top-level JSON object returned by the hydration
// tool. It carries conversation metadata plus the message array.
type transcriptEnvelope struct {
	ConversationID string                  `json:"conversation_id"`
	Model          string                  `json:"model,omitempty"`
	Path           string                  `json:"path,omitempty"`
	MessageStart   int                     `json:"message_start"`
	MessageEnd     int                     `json:"message_end"`
	MessageCount   int                     `json:"message_count"`
	Messages       []transcriptMessageJSON `json:"messages"`
}

// executeReviewTranscript returns a bounded segment of the conversation
// transcript as structured JSON for the review agent. It is the local
// handler for the review_transcript hydration tool — NOT dispatched via
// Toolbox.Execute.
//
// The start and end parameters define the message range [start:end) to
// include. This is an incremental marker: each review only processes
// messages since the last review, avoiding re-reading and re-reasoning
// over already reviewed content. The path field exposes the absolute path
// to the full conversation JSON file so the agent can file_read it if the
// bounded segment lacks context.
//
// The JSON preserves role alternation (user/assistant), nests tool calls
// inside the assistant message that produced them, and truncates per-message
// content and per-tool-output to the same caps as buildTranscript. The total
// output is bounded by MaxTranscriptTokens (chars = tokens*4) so tool-heavy
// conversations cannot overflow the review model's context window.
func (r *BackgroundReviewAgent) executeReviewTranscript(c *domain.Conversation, start, end int, path string) string {
	all := r.transcriptMessages(c)
	if start < 0 {
		start = 0
	}
	if end <= 0 || end > len(all) {
		end = len(all)
	}
	if start > end {
		start = end
	}
	segment := all[start:end]

	env := transcriptEnvelope{
		ConversationID: c.ID,
		Model:          c.Model,
		Path:           path,
		MessageStart:   start,
		MessageEnd:     end,
		MessageCount:   len(segment),
		Messages:       make([]transcriptMessageJSON, 0, len(segment)),
	}

	for _, m := range segment {
		content := m.Content
		if len(content) > r.settings.MaxTranscriptChars {
			content = content[:r.settings.MaxTranscriptChars] + "…[truncated]"
		}
		msg := transcriptMessageJSON{
			Role:    string(m.Role),
			Content: content,
		}
		for _, tc := range m.ToolCalls {
			args := text.Truncate(strings.TrimSpace(tc.Args), maxToolArgsChars)
			output := text.Truncate(strings.TrimSpace(tc.Output), maxToolOutputChars)
			msg.ToolCalls = append(msg.ToolCalls, transcriptToolCallJSON{
				Name:   tc.Name,
				Args:   args,
				Output: output,
			})
		}
		env.Messages = append(env.Messages, msg)
	}

	// Trim from the oldest message until the serialized JSON fits the
	// configured token cap (~chars/4). The most recent message is always
	// kept even when it alone exceeds the cap (best effort).
	charCap := r.settings.MaxTranscriptTokens * 4
	for len(env.Messages) > 1 {
		raw, _ := json.Marshal(env)
		if len(raw) <= charCap {
			break
		}
		env.Messages = env.Messages[1:]
		env.MessageStart++
		env.MessageCount = len(env.Messages)
	}

	raw, _ := json.Marshal(env)
	return string(raw)
}
