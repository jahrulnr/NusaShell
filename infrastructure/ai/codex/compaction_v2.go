package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"nusashell/infrastructure/ai/core"
)

// Remote v2 compaction budgets and retry caps, ported from
// core/src/compact_remote_v2.rs.
const (
	// RetainedMessageTokenBudget is the maximum retained history budget for
	// the v2 history rebuild.
	RetainedMessageTokenBudget int64 = 64_000
	// MaxRemoteCompactionV2StreamRetries bounds v2 stream retries. Compact
	// attempts run much longer than normal turns, so the per-transport retry
	// budget stays smaller than the general Responses stream retry budget.
	MaxRemoteCompactionV2StreamRetries = 2
)

// CompactionV2MaxRetries caps the provider's stream retry budget for a
// remote v2 compaction attempt: min(providerStreamMaxRetries, 2), floored at
// zero.
func CompactionV2MaxRetries(providerStreamMaxRetries int) int {
	if providerStreamMaxRetries < 0 {
		return 0
	}
	if providerStreamMaxRetries < MaxRemoteCompactionV2StreamRetries {
		return providerStreamMaxRetries
	}
	return MaxRemoteCompactionV2StreamRetries
}

// CompactionPrompt is the subset of the codex Prompt needed to build a
// remote v2 compaction request. Tools are raw wire JSON because tool wire
// shapes are owned by the caller.
type CompactionPrompt struct {
	Model             string
	Instructions      string
	Tools             []json.RawMessage
	ParallelToolCalls bool
	Reasoning         *Reasoning
	Input             []ResponseItem
}

// BuildCompactionV2Request builds the streaming /responses request for a
// remote v2 compaction attempt, ported from
// compact_remote_v2_attempt.rs run_remote_compact_v2_attempt: the retained
// conversation history is copied, the compaction_trigger control item is
// appended as the LAST input item, and the request is otherwise a normal
// Responses sampling request (store=false, stream=true). The trigger is a
// request control, never a durable history item.
func BuildCompactionV2Request(prompt CompactionPrompt) *ResponsesAPIRequest {
	input := AppendCompactionTrigger(prompt.Input)
	tools := make([]json.RawMessage, 0, len(prompt.Tools))
	for _, tool := range prompt.Tools {
		tools = append(tools, append(json.RawMessage(nil), tool...))
	}
	return &ResponsesAPIRequest{
		Model:             prompt.Model,
		Instructions:      prompt.Instructions,
		Input:             input,
		Tools:             tools,
		ParallelToolCalls: prompt.ParallelToolCalls,
		Reasoning:         prompt.Reasoning,
		Store:             false,
		Stream:            true,
	}
}

// AppendCompactionTrigger returns a copy of input with the
// compaction_trigger control item appended last.
func AppendCompactionTrigger(input []ResponseItem) []ResponseItem {
	out := make([]ResponseItem, len(input)+1)
	copy(out, input)
	out[len(input)] = NewCompactionTrigger()
	return out
}

// CompactionOutput is the successful result of a remote v2 compaction
// stream: the single opaque compaction item, the response id, and the
// best-effort usage observed at completion.
type CompactionOutput struct {
	CompactionItem  ResponseItem
	ResponseID      string
	TokenUsage      *TokenUsage
	OutputItemCount int
}

// CollectCompactionOutput consumes a remote v2 compaction stream, ported
// from compact_remote_v2.rs collect_compaction_output. The contract is:
//
//   - the stream must reach response.completed; a close before it is a
//     retryable stream error;
//   - exactly one output item must be a compaction item; any other count is
//     a fatal (non-retryable) error;
//   - other output items are allowed (only the compaction count is
//     constrained);
//   - all other events are ignored for the compaction result.
//
// The compaction item is kept as an opaque checkpoint: the client never
// decrypts or validates the encrypted_content.
func CollectCompactionOutput(stream *ResponsesStream) (*CompactionOutput, error) {
	var outputItemCount int
	var compactionCount int
	var compactionItem *ResponseItem
	var completed *EventCompleted
	for {
		event, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		switch e := event.(type) {
		case EventOutputItemDone:
			outputItemCount++
			if e.Item.IsCompaction() {
				compactionCount++
				if compactionItem == nil {
					item := e.Item
					compactionItem = &item
				}
			}
		case EventCompleted:
			completed = &e
		case EventError:
			return nil, e.Err
		default:
			// Added/other events do not contribute to the compaction result.
		}
		if completed != nil {
			break
		}
	}
	if completed == nil {
		return nil, core.NewNetworkError("codex",
			"codex: remote compaction v2 stream closed before response.completed",
			io.ErrUnexpectedEOF)
	}
	if compactionCount != 1 {
		return nil, &core.LiteLLMError{
			Type:     core.ErrorTypeProvider,
			Provider: "codex",
			Message: fmt.Sprintf("codex: remote compaction v2 expected exactly one compaction output item, got %d from %d output items",
				compactionCount, outputItemCount),
		}
	}
	return &CompactionOutput{
		CompactionItem:  *compactionItem,
		ResponseID:      completed.ResponseID,
		TokenUsage:      completed.TokenUsage,
		OutputItemCount: outputItemCount,
	}, nil
}

// BuildRetainedHistory rebuilds the post-compaction history, ported from
// compact_remote_v2.rs build_v2_compacted_history:
//
//   - retain real user messages (the composition of
//     is_retainedForRemoteCompactionV2 and
//     shouldKeepCompactedHistoryItem: developer/system instruction wrappers
//     are dropped, assistant and tool items are not retained by the v2
//     filter, agent-message handling is not ported);
//   - truncate the retained messages to RetainedMessageTokenBudget counted
//     from the end (the oldest messages are dropped or text-truncated
//     first, images and audio pass through);
//   - append the opaque compaction output item as the LAST item.
//
// The compaction_trigger control item is never part of the replacement
// history: callers pass the prompt input without the trigger.
func BuildRetainedHistory(promptInput []ResponseItem, compactionOutput ResponseItem) []ResponseItem {
	retained := make([]ResponseItem, 0, len(promptInput)+1)
	for _, item := range promptInput {
		if isRetainedForRemoteCompactionV2(item) && shouldKeepCompactedHistoryItem(item) {
			retained = append(retained, item)
		}
	}
	retained = truncateRetainedMessages(retained, RetainedMessageTokenBudget)
	retained = append(retained, compactionOutput)
	return retained
}

// isRetainedForRemoteCompactionV2 mirrors
// compact_remote_v2.rs is_retained_for_remote_compaction_v2, reduced to the
// ported item set: user, developer, and system messages are candidates
// (subject to shouldKeepCompactedHistoryItem); assistant and tool items are
// not retained.
func isRetainedForRemoteCompactionV2(item ResponseItem) bool {
	if item.Type != ItemTypeMessage {
		return false
	}
	switch item.Role {
	case RoleUser, RoleDeveloper, RoleSystem:
		return true
	default:
		return false
	}
}

// shouldKeepCompactedHistoryItem mirrors compact_remote.rs
// should_keep_compacted_history_item: developer messages (stale/duplicated
// instruction content) are dropped, non-text user wrappers are dropped, and
// real user text is kept. Assistant messages are kept upstream but are
// already excluded by the v2 retention filter.
func shouldKeepCompactedHistoryItem(item ResponseItem) bool {
	if item.Type != ItemTypeMessage {
		return false
	}
	switch item.Role {
	case RoleDeveloper:
		return false
	case RoleUser:
		return item.HasTextContent()
	case RoleAssistant:
		return true
	default:
		return false
	}
}

// truncateRetainedMessages keeps as many of the newest messages as fit in
// maxTokens, ported from compact_remote_v2.rs truncate_retained_messages
// (image budget and client-authored developer notices are not ported). Each
// message is charged by its text token count with a minimum of one token;
// the oldest overflowing message is text-truncated into the remaining
// budget, and everything older is dropped.
func truncateRetainedMessages(items []ResponseItem, maxTokens int64) []ResponseItem {
	remaining := maxTokens
	truncatedReversed := make([]ResponseItem, 0, len(items))
	for idx := len(items) - 1; idx >= 0; idx-- {
		if remaining == 0 {
			break
		}
		item := items[idx]
		tokenCount := max64(messageTextTokenCount(item), 1)
		if tokenCount <= remaining {
			truncatedReversed = append(truncatedReversed, item)
			remaining = saturatingSub(remaining, tokenCount)
			continue
		}
		truncated, ok := truncateMessageTextToTokenBudget(item, remaining)
		if ok {
			truncatedReversed = append(truncatedReversed, truncated)
		}
		remaining = 0
	}
	// Reverse back into conversation order.
	for i, j := 0, len(truncatedReversed)-1; i < j; i, j = i+1, j-1 {
		truncatedReversed[i], truncatedReversed[j] = truncatedReversed[j], truncatedReversed[i]
	}
	return truncatedReversed
}

// messageTextTokenCount mirrors compact_remote_v2.rs
// message_text_token_count: the summed coarse token count of text content;
// images and audio count as zero (their byte cost is discounted upstream).
func messageTextTokenCount(item ResponseItem) int64 {
	if item.Type != ItemTypeMessage {
		return max64(EstimateItemTokenCount(item), 0)
	}
	var total int64
	for _, content := range item.Content {
		if content.Type == ContentInputText || content.Type == ContentOutputText {
			total = saturatingAdd(total, ApproxTokenCount(content.Text))
		}
	}
	return total
}

// truncateMessageTextToTokenBudget truncates a message's text content into
// maxTokens, mirroring compact_remote_v2.rs
// truncate_message_text_to_token_budget. Images and audio content pass
// through; once the budget is spent, remaining text items are dropped. The
// cut keeps a prefix on a rune boundary sized to the token budget.
func truncateMessageTextToTokenBudget(item ResponseItem, maxTokens int64) (ResponseItem, bool) {
	if item.Type != ItemTypeMessage {
		return ResponseItem{}, false
	}
	remaining := max64(maxTokens, 0)
	content := make([]ContentItem, 0, len(item.Content))
	for _, original := range item.Content {
		switch original.Type {
		case ContentInputText, ContentOutputText:
			if remaining == 0 {
				continue
			}
			text := original.Text
			tokens := ApproxTokenCount(text)
			if tokens > remaining {
				text = truncateTextToTokenBudget(text, remaining)
				remaining = 0
			} else {
				remaining = saturatingSub(remaining, tokens)
			}
			if text != "" {
				truncated := original
				truncated.Text = text
				truncated.Raw = nil // mutation must not re-marshal stale raw bytes
				content = append(content, truncated)
			}
		default:
			content = append(content, original)
		}
	}
	if len(content) == 0 {
		return ResponseItem{}, false
	}
	out := item
	out.Content = content
	out.Raw = nil
	return out, true
}

// truncateTextToTokenBudget cuts text to a coarse token budget, keeping a
// prefix on a rune boundary and appending an omission marker, mirroring the
// spirit of utils/string truncate_text (upstream preserves a suffix too;
// this port keeps the prefix cut only).
func truncateTextToTokenBudget(text string, maxTokens int64) string {
	byteBudget := int(approxBytesForTokens(max64(maxTokens, 0)))
	if byteBudget <= 0 || len(text) <= byteBudget {
		return text
	}
	cut := byteBudget
	for cut > 0 && !isRuneBoundary(text, cut) {
		cut--
	}
	return text[:cut] + "… [truncated]"
}

func isRuneBoundary(s string, idx int) bool {
	if idx <= 0 || idx >= len(s) {
		return true
	}
	// Continuation bytes are 0b10xxxxxx.
	return s[idx]&0xC0 != 0x80
}

// ResponsesStreamRetryState tracks retry attempts for one Responses stream
// request, ported from core/src/responses_retry.rs
// ResponsesStreamRetryState (transport fallback and unbounded connection
// retries are not ported; delay policy is owned by the caller).
type ResponsesStreamRetryState struct {
	retries int
}

// Retries returns the number of retries recorded so far.
func (s *ResponsesStreamRetryState) Retries() int {
	if s == nil {
		return 0
	}
	return s.retries
}

// CanRetry reports whether a retryable error may be retried under the
// maxRetries budget and records the retry attempt when it may.
func (s *ResponsesStreamRetryState) CanRetry(maxRetries int) bool {
	if s == nil {
		return false
	}
	if s.retries >= maxRetries {
		return false
	}
	s.retries++
	return true
}

// IsRetryableStreamError classifies a stream error for the shared retry
// handler, mirroring the codex provider defaults: transport/stream errors
// and 5xx responses are retryable; 429 rate-limit responses are not
// retried.
func IsRetryableStreamError(err error) bool {
	if err == nil {
		return false
	}
	if core.IsRateLimitError(err) {
		return false
	}
	return core.IsRetryableError(err)
}
