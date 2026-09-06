package codex

import (
	"errors"
	"io"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func compactionSSE(compactions int, extraMessage bool, complete bool) io.Reader {
	frames := []string{
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_c\"}}",
	}
	for i := 0; i < compactions; i++ {
		frames = append(frames, strings.Join([]string{
			"event: response.output_item.done",
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"cmp_" + string(rune('0'+i)) + "\",\"type\":\"compaction\",\"encrypted_content\":\"ENC-" + string(rune('0'+i)) + "\"}}",
		}, "\n"))
	}
	if extraMessage {
		frames = append(frames, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}}")
	}
	if complete {
		frames = append(frames, strings.Join([]string{
			"event: response.completed",
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_c\",\"usage\":{\"input_tokens\":100,\"output_tokens\":50,\"total_tokens\":150}}}",
		}, "\n"))
	}
	return sseBody(frames...)
}

func TestCollectCompactionOutputSuccess(t *testing.T) {
	// Exactly one compaction item plus other output items is valid: only the
	// compaction count is constrained.
	output, err := CollectCompactionOutput(NewResponsesStream(compactionSSE(1, true, true)))
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !output.CompactionItem.IsCompaction() || output.CompactionItem.EncryptedContent != "ENC-0" {
		t.Fatalf("compaction item = %+v, want opaque ENC-0 item", output.CompactionItem)
	}
	if output.ResponseID != "resp_c" {
		t.Fatalf("response id = %q, want resp_c", output.ResponseID)
	}
	if output.TokenUsage == nil || output.TokenUsage.TotalTokens != 150 {
		t.Fatalf("usage = %+v, want recorded total 150", output.TokenUsage)
	}
	if output.OutputItemCount != 2 {
		t.Fatalf("output item count = %d, want 2 (compaction + message)", output.OutputItemCount)
	}
}

func TestCollectCompactionOutputRejectsZeroCompactionItems(t *testing.T) {
	_, err := CollectCompactionOutput(NewResponsesStream(compactionSSE(0, true, true)))
	if err == nil {
		t.Fatalf("expected fatal error for zero compaction items")
	}
	if !strings.Contains(err.Error(), "got 0") || !strings.Contains(err.Error(), "exactly one compaction") {
		t.Fatalf("err = %v, want fatal exactly-one message", err)
	}
	if core.IsRetryableError(err) {
		t.Fatalf("zero-compaction error classified retryable, want fatal")
	}
}

func TestCollectCompactionOutputRejectsTwoCompactionItems(t *testing.T) {
	_, err := CollectCompactionOutput(NewResponsesStream(compactionSSE(2, false, true)))
	if err == nil {
		t.Fatalf("expected fatal error for two compaction items")
	}
	if !strings.Contains(err.Error(), "got 2") {
		t.Fatalf("err = %v, want got-2 count", err)
	}
}

func TestCollectCompactionOutputStreamClosedBeforeCompleted(t *testing.T) {
	_, err := CollectCompactionOutput(NewResponsesStream(compactionSSE(1, false, false)))
	if err == nil {
		t.Fatalf("expected stream error when closed before response.completed")
	}
	if !strings.Contains(err.Error(), "closed before response.completed") {
		t.Fatalf("err = %v, want closed-before-completed", err)
	}
	if !IsRetryableStreamError(err) {
		t.Fatalf("closed-before-completed not retryable, want retryable stream error")
	}
}

func TestCollectCompactionOutputPropagatesFailedResponse(t *testing.T) {
	stream := NewResponsesStream(sseBody(
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"boom\"}}}",
	))
	_, err := CollectCompactionOutput(stream)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want failed-response error", err)
	}
}

func TestCompactionV2MaxRetries(t *testing.T) {
	cases := []struct {
		providerRetries int
		want            int
	}{
		{5, 2}, // capped at the v2 bound
		{2, 2}, // exactly the bound
		{1, 1}, // provider budget is smaller
		{0, 0},
		{-3, 0}, // negative budgets floor at zero
	}
	for _, tc := range cases {
		if got := CompactionV2MaxRetries(tc.providerRetries); got != tc.want {
			t.Fatalf("CompactionV2MaxRetries(%d) = %d, want %d", tc.providerRetries, got, tc.want)
		}
	}
}

func TestResponsesStreamRetryStateBudget(t *testing.T) {
	state := &ResponsesStreamRetryState{}
	if !state.CanRetry(2) || state.Retries() != 1 {
		t.Fatalf("first retry rejected")
	}
	if !state.CanRetry(2) || state.Retries() != 2 {
		t.Fatalf("second retry rejected")
	}
	if state.CanRetry(2) || state.Retries() != 2 {
		t.Fatalf("third retry allowed past the budget")
	}
}

func TestIsRetryableStreamError(t *testing.T) {
	rateLimit := core.NewRateLimitError("codex", "too many requests", 60)
	if IsRetryableStreamError(rateLimit) {
		t.Fatalf("429 rate limit classified retryable, want NOT retried (provider default)")
	}
	network := core.NewNetworkError("codex", "stream cut", io.ErrUnexpectedEOF)
	if !IsRetryableStreamError(network) {
		t.Fatalf("transport error classified non-retryable, want retryable")
	}
	if IsRetryableStreamError(nil) {
		t.Fatalf("nil error classified retryable")
	}
}

func userMessageWithTokens(tokens int64) ResponseItem {
	byteLen := int(tokens * 4)
	return NewTextMessage(RoleUser, ContentInputText, strings.Repeat("a", byteLen))
}

func TestBuildRetainedHistoryKeepsOnlyRealUserMessages(t *testing.T) {
	compaction := ResponseItem{Type: ItemTypeCompaction, ID: "cmp_1", EncryptedContent: "ENC-COMPACT"}
	promptInput := []ResponseItem{
		NewTextMessage(RoleDeveloper, ContentInputText, "stale developer instructions"),
		NewTextMessage(RoleSystem, ContentInputText, "system wrapper"),
		userMessageWithTokens(4),
		{Type: ItemTypeMessage, Role: RoleUser, Content: []ContentItem{{Type: ContentInputImage, ImageURL: "https://example.invalid/a.png"}}}, // no-text wrapper
		NewTextMessage(RoleAssistant, ContentOutputText, "assistant text"),
		{Type: ItemTypeReasoning, ID: "rs_1", EncryptedContent: "ENC-1"},
		{Type: ItemTypeFunctionCall, CallID: "call_1", Name: "noop", Arguments: "{}"},
		{Type: ItemTypeFunctionCallOutput, CallID: "call_1", Output: []byte(`"done"`)},
		userMessageWithTokens(6),
		NewCompactionTrigger(), // never part of the replacement history
	}
	retained := BuildRetainedHistory(promptInput, compaction)
	// Only the two real user messages plus the compaction item survive.
	if len(retained) != 3 {
		t.Fatalf("retained len = %d, want 3 (two user messages + compaction)", len(retained))
	}
	if !retained[0].IsUserMessage() || !retained[1].IsUserMessage() {
		t.Fatalf("retained[0:2] = %+v, want user messages", retained[:2])
	}
	last := retained[len(retained)-1]
	if !last.IsCompaction() || last.EncryptedContent != "ENC-COMPACT" {
		t.Fatalf("last retained = %+v, want opaque compaction output", last)
	}
	for _, item := range retained {
		if item.IsCompactionTrigger() {
			t.Fatalf("compaction_trigger leaked into the replacement history")
		}
	}
}

func TestTruncateRetainedMessagesKeepsNewestWithinBudget(t *testing.T) {
	items := []ResponseItem{
		userMessageWithTokens(4), // oldest, dropped: budget exhausted
		userMessageWithTokens(6),
		userMessageWithTokens(4), // newest
	}
	retained := truncateRetainedMessages(items, 10)
	if len(retained) != 2 {
		t.Fatalf("retained len = %d, want 2", len(retained))
	}
	if messageTextTokenCount(retained[0]) != 6 || messageTextTokenCount(retained[1]) != 4 {
		t.Fatalf("retained order = [%d, %d], want [6, 4]",
			messageTextTokenCount(retained[0]), messageTextTokenCount(retained[1]))
	}
}

func TestTruncateRetainedMessagesTruncatesOverflowingOldestKept(t *testing.T) {
	items := []ResponseItem{
		userMessageWithTokens(4), // oldest, fully dropped
		userMessageWithTokens(6), // overflows the 5-token remainder after the newest
		userMessageWithTokens(4), // newest fits
	}
	retained := truncateRetainedMessages(items, 5)
	if len(retained) != 2 {
		t.Fatalf("retained len = %d, want 2 (truncated middle + newest)", len(retained))
	}
	if !strings.Contains(retained[0].Content[0].Text, "[truncated]") {
		t.Fatalf("overflowing message was not text-truncated: %q", retained[0].Content[0].Text)
	}
	if retained[1].Content[0].Text != strings.Repeat("a", 16) {
		t.Fatalf("newest message was modified: %q", retained[1].Content[0].Text)
	}
}

func TestTruncateRetainedMessagesImagesPassThrough(t *testing.T) {
	withImage := ResponseItem{
		Type:    ItemTypeMessage,
		Role:    RoleUser,
		Content: []ContentItem{{Type: ContentInputImage, ImageURL: "https://example.invalid/a.png"}},
	}
	retained := truncateRetainedMessages([]ResponseItem{withImage}, 1)
	if len(retained) != 1 {
		t.Fatalf("retained len = %d, want 1 (image message charged the 1-token minimum)", len(retained))
	}
	if retained[0].Content[0].Type != ContentInputImage {
		t.Fatalf("image content dropped during truncation")
	}
}

func TestTruncateTextToTokenBudgetCutsOnRuneBoundary(t *testing.T) {
	text := strings.Repeat("a", 40) // 10 tokens
	if got := truncateTextToTokenBudget(text, 10); got != text {
		t.Fatalf("within-budget text was cut: %q", got)
	}
	cut := truncateTextToTokenBudget(text, 2)
	if !strings.HasPrefix(cut, strings.Repeat("a", 8)) {
		t.Fatalf("cut prefix = %q, want 8 bytes", cut)
	}
	if !strings.Contains(cut, "[truncated]") {
		t.Fatalf("cut text missing truncation marker: %q", cut)
	}
	// Multi-byte runes must not be split.
	multibyte := strings.Repeat("é", 10) // 20 bytes, 5 tokens
	cutMultibyte := truncateTextToTokenBudget(multibyte, 1)
	for i := 0; i < len(cutMultibyte); i++ {
		if cutMultibyte[i] == 0xEF && i+1 < len(cutMultibyte) && cutMultibyte[i+1]&0xC0 == 0x80 {
			continue // valid lead byte followed by continuation
		}
	}
	if !strings.HasPrefix(cutMultibyte, "éé") { // 4-byte budget -> 2 full runes
		t.Fatalf("multibyte cut = %q, want rune-aligned prefix", cutMultibyte)
	}
}

func TestBuildRetainedHistoryTruncatesToBudget(t *testing.T) {
	// A huge oldest message is text-truncated into whatever budget remains
	// after the newest messages are kept.
	compaction := ResponseItem{Type: ItemTypeCompaction, ID: "cmp_1", EncryptedContent: "ENC-COMPACT"}
	promptInput := []ResponseItem{
		userMessageWithTokens(70_000), // far over the 64k budget
		userMessageWithTokens(4),      // newest, kept intact
	}
	retained := BuildRetainedHistory(promptInput, compaction)
	if len(retained) != 3 {
		t.Fatalf("retained len = %d, want 3 (truncated oldest + newest + compaction)", len(retained))
	}
	oldest := retained[0]
	if !strings.Contains(oldest.Content[0].Text, "[truncated]") {
		t.Fatalf("oldest message not truncated: %d bytes", len(oldest.Content[0].Text))
	}
	if got := ApproxTokenCount(oldest.Content[0].Text); got > 63_997+3 {
		t.Fatalf("truncated text still too large: ~%d tokens", got)
	}
	if retained[1].Content[0].Text != strings.Repeat("a", 16) {
		t.Fatalf("newest message was modified")
	}
}

func TestCollectIgnoresEventsAfterCompleted(t *testing.T) {
	// Completion is terminal for the collector; trailing frames are not read.
	stream := NewResponsesStream(io.NopCloser(strings.NewReader(strings.Join([]string{
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"ENC-OK\"}}",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_z\"}}",
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"ENC-LATE\"}}",
	}, "\n\n") + "\n\n")))
	output, err := CollectCompactionOutput(stream)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if output.OutputItemCount != 1 || output.CompactionItem.EncryptedContent != "ENC-OK" {
		t.Fatalf("output = %+v, want single pre-completion compaction", output)
	}
}

func TestErrorEventBeforeCompletionFailsCollection(t *testing.T) {
	stream := NewResponsesStream(sseBody(
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"ENC-OK\"}}",
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"mid-stream failure\"}}",
	))
	err := error(nil)
	if _, err = CollectCompactionOutput(stream); err == nil || !strings.Contains(err.Error(), "mid-stream failure") {
		t.Fatalf("err = %v, want mid-stream error propagated", err)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("mid-stream error was swallowed as EOF")
	}
}
