package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApproxTokenCount(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"abcd", 1},                 // exactly 4 bytes -> 1 token
		{"abc", 1},                  // 3 bytes -> ceil -> 1 token
		{"abcdefgh", 2},             // 8 bytes -> 2 tokens
		{strings.Repeat("x", 9), 3}, // 9 bytes -> 3 tokens
	}
	for _, tc := range cases {
		if got := ApproxTokenCount(tc.in); got != tc.want {
			t.Fatalf("ApproxTokenCount(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestEstimateReasoningLength(t *testing.T) {
	// encoded_len*3/4 - 650, clamped at zero.
	cases := []struct {
		encodedLen int64
		want       int64
	}{
		{0, 0},
		{100, 0},    // 75 - 650 -> clamp 0
		{800, 0},    // 600 - 650 -> clamp 0
		{866, 0},    // 649.5 int div -> 649 - 650 -> clamp 0
		{867, 0},    // 650 - 650 -> 0
		{1000, 100}, // 750 - 650 -> 100
		{40000, 29350},
	}
	for _, tc := range cases {
		if got := estimateReasoningLength(tc.encodedLen); got != tc.want {
			t.Fatalf("estimateReasoningLength(%d) = %d, want %d", tc.encodedLen, got, tc.want)
		}
	}
}

func TestEstimateEncryptedFunctionOutputLength(t *testing.T) {
	// ceil(encoded_len*9/16).
	cases := []struct {
		encodedLen int64
		want       int64
	}{
		{0, 0},
		{1, 1},
		{16, 9},
		{17, 10},
		{160, 90},
	}
	for _, tc := range cases {
		if got := estimateEncryptedFunctionOutputLength(tc.encodedLen); got != tc.want {
			t.Fatalf("estimateEncryptedFunctionOutputLength(%d) = %d, want %d", tc.encodedLen, got, tc.want)
		}
	}
}

func TestEstimateItemTokenCountUsesReasoningHeuristicForEncryptedItems(t *testing.T) {
	reasoning := ResponseItem{Type: ItemTypeReasoning, ID: "rs_1", EncryptedContent: strings.Repeat("E", 1000)}
	if got := EstimateItemTokenCount(reasoning); got != 100 {
		t.Fatalf("reasoning estimate = %d, want 100", got)
	}
	compaction := ResponseItem{Type: ItemTypeCompaction, ID: "cmp_1", EncryptedContent: strings.Repeat("E", 1000)}
	if got := EstimateItemTokenCount(compaction); got != 100 {
		t.Fatalf("compaction estimate = %d, want 100", got)
	}
}

func TestEstimateItemTokenCountSwapsImagePayloadForResizedEstimate(t *testing.T) {
	payload := strings.Repeat("A", 1000)
	withDataURL := ResponseItem{
		Type:    ItemTypeMessage,
		Role:    RoleUser,
		Content: []ContentItem{{Type: ContentInputImage, ImageURL: "data:image/png;base64," + payload}},
	}
	// Expected: serialized size minus the 1000 payload bytes plus the
	// 7373-byte resized-image estimate, then ceil/4.
	raw, err := json.Marshal(withDataURL)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := approxTokensFromByteCount(int64(len(raw)) - int64(len(payload)) + resizedImageBytesEstimate)
	if got := EstimateItemTokenCount(withDataURL); got != want {
		t.Fatalf("image estimate = %d, want %d", got, want)
	}
	// An https URL is not discounted: it stays at raw serialized size.
	withHTTP := ResponseItem{
		Type:    ItemTypeMessage,
		Role:    RoleUser,
		Content: []ContentItem{{Type: ContentInputImage, ImageURL: "https://example.invalid/a.png"}},
	}
	rawHTTP, _ := json.Marshal(withHTTP)
	if got, want := EstimateItemTokenCount(withHTTP), approxTokensFromByteCount(int64(len(rawHTTP))); got != want {
		t.Fatalf("http image estimate = %d, want %d", got, want)
	}
	// The base64 discount only applies to image/* data URLs.
	withAudioURL := ResponseItem{
		Type:    ItemTypeMessage,
		Role:    RoleUser,
		Content: []ContentItem{{Type: ContentInputImage, ImageURL: "data:audio/wav;base64," + payload}},
	}
	rawAudio, _ := json.Marshal(withAudioURL)
	if got, want := EstimateItemTokenCount(withAudioURL), approxTokensFromByteCount(int64(len(rawAudio))); got != want {
		t.Fatalf("mislabeled data URL estimate = %d, want undiscouned %d", got, want)
	}
}

func TestEstimateItemTokenCountAdjustsEncryptedFunctionOutput(t *testing.T) {
	encrypted := strings.Repeat("E", 160) // 9/16*160 = 90 estimated bytes
	item := ResponseItem{
		Type:   ItemTypeFunctionCallOutput,
		CallID: "call_1",
		Output: json.RawMessage(`[{"type":"encrypted_content","encrypted_content":"` + encrypted + `"}]`),
	}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Expected: raw size minus 160 payload bytes plus 90 estimated bytes.
	want := approxTokensFromByteCount(int64(len(raw)) - int64(len(encrypted)) + 90)
	if got := EstimateItemTokenCount(item); got != want {
		t.Fatalf("encrypted output estimate = %d, want %d", got, want)
	}
}

func activeTokensFixture() (TokenUsageInfo, []ResponseItem) {
	info := TokenUsageInfo{LastTokenUsage: TokenUsage{InputTokens: 900, OutputTokens: 100, TotalTokens: 1000}}
	history := []ResponseItem{
		NewTextMessage(RoleUser, ContentInputText, "first question"),                            // user turn boundary
		{Type: ItemTypeReasoning, ID: "rs_1", EncryptedContent: strings.Repeat("E", 1000)},      // 100 est tokens
		NewTextMessage(RoleAssistant, ContentOutputText, "answer"),                              // model-generated boundary
		NewTextMessage(RoleUser, ContentInputText, "second question"),                           // local tail
		{Type: ItemTypeFunctionCallOutput, CallID: "call_1", Output: json.RawMessage(`"done"`)}, // local tail
	}
	return info, history
}

func TestActiveContextTokensServerReasoningIncluded(t *testing.T) {
	info, history := activeTokensFixture()
	tail := EstimateItemTokenCount(history[3]) + EstimateItemTokenCount(history[4])
	want := saturatingAdd(info.LastTokenUsage.TotalTokens, tail)
	if got := ActiveContextTokens(info, history, true); got != want {
		t.Fatalf("active = %d, want %d (last usage + tail only)", got, want)
	}
}

func TestActiveContextTokensServerReasoningExcludedAddsOldReasoning(t *testing.T) {
	info, history := activeTokensFixture()
	tail := EstimateItemTokenCount(history[3]) + EstimateItemTokenCount(history[4])
	// The encrypted reasoning item sits before the LAST user turn boundary
	// (index 3), so its estimate is added when the server did not count it.
	want := saturatingAdd(saturatingAdd(info.LastTokenUsage.TotalTokens, tail), 100)
	if got := ActiveContextTokens(info, history, false); got != want {
		t.Fatalf("active = %d, want %d (last usage + old reasoning + tail)", got, want)
	}
}

func TestActiveContextTokensWithoutUserBoundary(t *testing.T) {
	// No user turn boundary means no old-reasoning estimate is added.
	info, _ := activeTokensFixture()
	history := []ResponseItem{
		{Type: ItemTypeReasoning, ID: "rs_1", EncryptedContent: strings.Repeat("E", 1000)},
		NewTextMessage(RoleAssistant, ContentOutputText, "answer"),
	}
	want := info.LastTokenUsage.TotalTokens
	if got := ActiveContextTokens(info, history, false); got != want {
		t.Fatalf("active = %d, want %d", got, want)
	}
}

func TestActiveContextTokensWithoutModelGeneratedItemsHasNoLocalTail(t *testing.T) {
	info, _ := activeTokensFixture()
	history := []ResponseItem{
		NewTextMessage(RoleUser, ContentInputText, "q1"),
		NewTextMessage(RoleUser, ContentInputText, "q2"),
	}
	// Upstream's items_after_last_model_generated_item is empty when no
	// model-generated item exists, so only the server usage is available.
	want := info.LastTokenUsage.TotalTokens
	if got := ActiveContextTokens(info, history, true); got != want {
		t.Fatalf("active = %d, want %d", got, want)
	}
}

func TestEstimateTokenCountWithBaseInstructions(t *testing.T) {
	history := []ResponseItem{
		NewTextMessage(RoleUser, ContentInputText, "hello"),
		{Type: ItemTypeReasoning, ID: "rs_1", EncryptedContent: strings.Repeat("E", 1000)},
	}
	want := saturatingAdd(ApproxTokenCount("base instructions here"),
		EstimateItemTokenCount(history[0])+EstimateItemTokenCount(history[1]))
	if got := EstimateTokenCountWithBaseInstructions("base instructions here", history); got != want {
		t.Fatalf("estimate = %d, want %d", got, want)
	}
}

func TestAutoCompactWindowPrefillLifecycle(t *testing.T) {
	// Ported from auto_compact_window.rs tracks_prefill_and_window_boundaries.
	window := NewAutoCompactWindow()
	if window.WindowNumber() != 0 {
		t.Fatalf("initial window number = %d, want 0", window.WindowNumber())
	}
	if _, ok := window.PrefillInputTokens(); ok {
		t.Fatalf("initial prefill exists, want none")
	}

	window.SetEstimatedPrefill(150)
	if prefill, ok := window.PrefillInputTokens(); !ok || prefill != 150 {
		t.Fatalf("estimated prefill = %d/%v, want 150/true", prefill, ok)
	}

	// The first server sample replaces the estimated baseline.
	window.EnsureServerObservedPrefillFromUsage(TokenUsage{InputTokens: 120, TotalTokens: 170})
	if prefill, ok := window.PrefillInputTokens(); !ok || prefill != 120 {
		t.Fatalf("server prefill = %d/%v, want 120/true", prefill, ok)
	}

	// Later samples and estimates never replace the observed baseline.
	window.EnsureServerObservedPrefillFromUsage(TokenUsage{InputTokens: 130, TotalTokens: 180})
	window.SetEstimatedPrefill(90)
	if prefill, ok := window.PrefillInputTokens(); !ok || prefill != 120 {
		t.Fatalf("server prefill after later samples = %d/%v, want 120/true", prefill, ok)
	}

	// Negative samples are clamped to zero on the first server sample.
	fresh := NewAutoCompactWindow()
	fresh.EnsureServerObservedPrefillFromUsage(TokenUsage{InputTokens: -5})
	if prefill, _ := fresh.PrefillInputTokens(); prefill != 0 {
		t.Fatalf("negative server prefill = %d, want 0", prefill)
	}

	// Advancing starts a new window: number bumps, prefill clears.
	number := window.Advance()
	if number != 1 || window.WindowNumber() != 1 {
		t.Fatalf("advance number = %d/%d, want 1", number, window.WindowNumber())
	}
	if _, ok := window.PrefillInputTokens(); ok {
		t.Fatalf("prefill survived advance, want cleared")
	}
}
