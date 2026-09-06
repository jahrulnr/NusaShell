package codex

import (
	"encoding/json"
	"math"
	"strings"
)

// TokenUsage mirrors protocol/src/protocol.rs TokenUsage: the client-side
// usage record derived from a response.completed usage payload.
type TokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

// TokenUsageInfo tracks the last observed server usage for context
// accounting (protocol/src/protocol.rs TokenUsageInfo, reduced to the field
// the accounting formula reads).
type TokenUsageInfo struct {
	LastTokenUsage TokenUsage
}

// Approximate token-counting constants, ported from
// codex-rs/utils/string/src/truncate.rs and
// core/src/context_manager/history.rs. The estimator is deliberately coarse:
// it is not a tokenizer, only a proxy for model-visible size.
const (
	approxBytesPerToken = 4
	// resizedImageBytesEstimate approximates the model-visible byte cost of
	// one resized image input (7,373 bytes ~ 1,844 tokens).
	resizedImageBytesEstimate = 7373
)

// ApproxTokenCount returns the coarse token estimate for a string:
// ceil(len(s)/4).
func ApproxTokenCount(s string) int64 {
	return approxTokensFromByteCount(int64(len(s)))
}

// approxTokensFromByteCount converts a byte count to a coarse token count
// with ceiling division; non-positive input yields 0.
func approxTokensFromByteCount(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	if bytes > math.MaxInt64-approxBytesPerToken+1 {
		return math.MaxInt64
	}
	return (bytes + approxBytesPerToken - 1) / approxBytesPerToken
}

// approxBytesForTokens converts a token budget back to a byte budget.
func approxBytesForTokens(tokens int64) int64 {
	if tokens <= 0 {
		return 0
	}
	if tokens > math.MaxInt64/approxBytesPerToken {
		return math.MaxInt64
	}
	return tokens * approxBytesPerToken
}

// estimateReasoningLength estimates the model-visible length of an encrypted
// reasoning/compaction blob: encoded_len*3/4 - 650, clamped at zero.
func estimateReasoningLength(encodedLen int64) int64 {
	if encodedLen <= 0 {
		return 0
	}
	scaled := saturatingMul(encodedLen, 3) / 4
	if scaled < 650 {
		return 0
	}
	return scaled - 650
}

// estimateEncryptedFunctionOutputLength estimates the model-visible length
// of an encrypted function-call output blob: ceil(encoded_len*9/16).
func estimateEncryptedFunctionOutputLength(encodedLen int64) int64 {
	if encodedLen <= 0 {
		return 0
	}
	return (encodedLen*9 + 15) / 16
}

// parseBase64DataURL returns the base64 payload of a data: URL whose media
// type starts with mediaTypePrefix (e.g. "image/"), mirroring
// history.rs parse_base64_data_url. Non-data URLs and non-base64 payloads
// return ok=false.
func parseBase64DataURL(url, mediaTypePrefix string) (payload string, ok bool) {
	if len(url) < len("data:") || !strings.EqualFold(url[:len("data:")], "data:") {
		return "", false
	}
	comma := strings.IndexByte(url, ',')
	if comma < 0 {
		return "", false
	}
	metadata := url[len("data:"):comma]
	payload = url[comma+1:]
	mime := metadata
	hasBase64 := false
	for idx, part := range strings.Split(metadata, ";") {
		if idx == 0 {
			mime = part
			continue
		}
		if strings.EqualFold(part, "base64") {
			hasBase64 = true
		}
	}
	if len(mime) < len(mediaTypePrefix) || !strings.EqualFold(mime[:len(mediaTypePrefix)], mediaTypePrefix) {
		return "", false
	}
	if !hasBase64 {
		return "", false
	}
	return payload, true
}

// estimateImageBytes returns the byte estimate for one image input. This
// port always uses the resized-input estimate; the detail:"original" patch
// decoding of upstream is out of scope for the accounting port.
func estimateImageBytes() int64 {
	return resizedImageBytesEstimate
}

// EstimateItemTokenCount returns the coarse model-visible token estimate for
// one item, ported from history.rs estimate_item_token_count:
//
//   - reasoning/compaction items with encrypted_content use the encrypted
//     reasoning-length heuristic;
//   - everything else is estimated from its serialized JSON byte length,
//     with inline base64 image data-URL payloads replaced by the resized
//     image estimate and encrypted function-call output payloads replaced by
//     the 9/16 heuristic.
func EstimateItemTokenCount(item ResponseItem) int64 {
	if item.Type == ItemTypeCompaction || (item.Type == ItemTypeReasoning && item.EncryptedContent != "") {
		return estimateReasoningLength(int64(len(item.EncryptedContent)))
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return 0
	}
	bytes := int64(len(raw))
	payload, replacement := imageEstimateAdjustment(item)
	bytes = saturatingSub(saturatingAdd(bytes, replacement), payload)
	payload, replacement = encryptedFunctionOutputAdjustment(item)
	bytes = saturatingSub(saturatingAdd(bytes, replacement), payload)
	return approxTokensFromByteCount(bytes)
}

// imageEstimateAdjustment scans a message item for inline base64 image data
// URLs and returns (payloadBytes, replacementBytes) for the estimate swap.
func imageEstimateAdjustment(item ResponseItem) (payloadBytes, replacementBytes int64) {
	if item.Type != ItemTypeMessage {
		return 0, 0
	}
	for _, content := range item.Content {
		if content.Type != ContentInputImage {
			continue
		}
		if payload, ok := parseBase64DataURL(content.ImageURL, "image/"); ok {
			payloadBytes = saturatingAdd(payloadBytes, int64(len(payload)))
			replacementBytes = saturatingAdd(replacementBytes, estimateImageBytes())
		}
	}
	return payloadBytes, replacementBytes
}

// encryptedFunctionOutputAdjustment scans a function_call_output item for
// encrypted content entries inside its structured output and returns
// (payloadBytes, replacementBytes).
func encryptedFunctionOutputAdjustment(item ResponseItem) (payloadBytes, replacementBytes int64) {
	if item.Type != ItemTypeFunctionCallOutput || len(item.Output) == 0 {
		return 0, 0
	}
	var contents []ContentItem
	if err := json.Unmarshal(item.Output, &contents); err != nil {
		return 0, 0
	}
	for _, content := range contents {
		if content.Type != ContentEncrypted {
			continue
		}
		payloadBytes = saturatingAdd(payloadBytes, int64(len(content.EncryptedContent)))
		replacementBytes = saturatingAdd(replacementBytes,
			estimateEncryptedFunctionOutputLength(int64(len(content.EncryptedContent))))
	}
	return payloadBytes, replacementBytes
}

// isModelGeneratedItem mirrors history.rs is_model_generated_item: items the
// model itself emitted. Local items added after the most recent such item
// are not reflected in last_token_usage.total_tokens yet.
func isModelGeneratedItem(item ResponseItem) bool {
	switch item.Type {
	case ItemTypeMessage:
		return item.Role == RoleAssistant
	case ItemTypeReasoning, ItemTypeFunctionCall, ItemTypeWebSearchCall, ItemTypeCompaction:
		return true
	default:
		// function_call_output, custom tool outputs, compaction_trigger,
		// and passthrough items are client/environment-authored.
		return false
	}
}

// isUserTurnBoundary mirrors history.rs is_user_turn_boundary, reduced to
// the ported item set: an ordinary user message starts a new turn. The
// contextual-user-message and agent-message distinctions of upstream are
// not modeled here.
func isUserTurnBoundary(item ResponseItem) bool {
	return item.IsUserMessage()
}

// ActiveContextTokens computes the active context size, ported from
// history.rs get_total_token_usage:
//
//	active = last_token_usage.total_tokens
//	           + (sum of estimates for items after the last model-generated item)
//	           + (sum of estimates for encrypted reasoning items before the
//	              last user turn boundary, only when the server did NOT
//	              already include past reasoning in its usage)
//
// All additions are saturating. The estimate is a client-side approximation
// combining the last server-observed usage with local tail estimates, not a
// full tokenizer recount.
func ActiveContextTokens(info TokenUsageInfo, history []ResponseItem, serverReasoningIncluded bool) int64 {
	total := info.LastTokenUsage.TotalTokens
	// Local tail: items after the last model-generated item.
	start := len(history)
	for idx := len(history) - 1; idx >= 0; idx-- {
		if isModelGeneratedItem(history[idx]) {
			start = idx + 1
			break
		}
	}
	for _, item := range history[start:] {
		total = saturatingAdd(total, EstimateItemTokenCount(item))
	}
	if !serverReasoningIncluded {
		total = saturatingAdd(total, nonLastReasoningItemsTokens(history))
	}
	return total
}

// nonLastReasoningItemsTokens sums estimates for encrypted reasoning items
// strictly before the last user turn boundary (history.rs
// get_non_last_reasoning_items_tokens). Returns 0 when no boundary exists.
func nonLastReasoningItemsTokens(history []ResponseItem) int64 {
	lastUserIndex := -1
	for idx := len(history) - 1; idx >= 0; idx-- {
		if isUserTurnBoundary(history[idx]) {
			lastUserIndex = idx
			break
		}
	}
	if lastUserIndex <= 0 {
		return 0
	}
	var total int64
	for _, item := range history[:lastUserIndex] {
		if item.Type == ItemTypeReasoning && item.EncryptedContent != "" {
			total = saturatingAdd(total, EstimateItemTokenCount(item))
		}
	}
	return total
}

// EstimateTokenCountWithBaseInstructions estimates the token cost of the
// base instructions plus the full history (history.rs
// estimate_token_count_with_base_instructions). The non-negative result is
// what recompute_token_usage installs as last_token_usage.total_tokens
// after a compaction or resume replaces the history.
func EstimateTokenCountWithBaseInstructions(baseInstructions string, history []ResponseItem) int64 {
	total := ApproxTokenCount(baseInstructions)
	for _, item := range history {
		total = saturatingAdd(total, EstimateItemTokenCount(item))
	}
	return total
}

// prefillKind distinguishes the two baselines an AutoCompactWindow tracks.
type prefillKind int

const (
	prefillNone prefillKind = iota
	prefillEstimated
	prefillServerObserved
)

// AutoCompactWindow tracks the compaction window prefill baseline, ported
// from core/src/state/auto_compact_window.rs. The body_after_prefix scope
// subtracts this absolute input-token baseline from later active-context
// usage. An estimated baseline (from resume/recompute) is only used until
// the first server-observed sample replaces it; later samples never replace
// the observed baseline. Window UUIDs are rollout-persistence concerns and
// are not ported.
type AutoCompactWindow struct {
	windowNumber  int64
	prefillKind   prefillKind
	prefillTokens int64
}

// NewAutoCompactWindow returns the initial window (number 0, no prefill).
func NewAutoCompactWindow() *AutoCompactWindow {
	return &AutoCompactWindow{}
}

// WindowNumber returns the current compaction window number.
func (w *AutoCompactWindow) WindowNumber() int64 {
	return w.windowNumber
}

// Advance starts a new compaction window: the number is bumped (saturating)
// and the prefill baseline is cleared.
func (w *AutoCompactWindow) Advance() int64 {
	w.windowNumber = saturatingAdd(w.windowNumber, 1)
	w.ClearPrefill()
	return w.windowNumber
}

// ClearPrefill drops the current prefill baseline.
func (w *AutoCompactWindow) ClearPrefill() {
	w.prefillKind = prefillNone
	w.prefillTokens = 0
}

// EnsureServerObservedPrefillFromUsage records the request-input side of the
// first server usage sample: max(input_tokens, 0). Later samples do not
// replace an already observed baseline.
func (w *AutoCompactWindow) EnsureServerObservedPrefillFromUsage(usage TokenUsage) {
	if w.prefillKind == prefillServerObserved {
		return
	}
	w.prefillKind = prefillServerObserved
	w.prefillTokens = max64(usage.InputTokens, 0)
}

// SetEstimatedPrefill installs an estimated baseline, clamped at zero, only
// while no server-observed baseline exists.
func (w *AutoCompactWindow) SetEstimatedPrefill(tokens int64) {
	if w.prefillKind == prefillServerObserved {
		return
	}
	w.prefillKind = prefillEstimated
	w.prefillTokens = max64(tokens, 0)
}

// PrefillInputTokens returns the effective prefill baseline and whether one
// exists.
func (w *AutoCompactWindow) PrefillInputTokens() (int64, bool) {
	if w.prefillKind == prefillNone {
		return 0, false
	}
	return w.prefillTokens, true
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

func saturatingSub(a, b int64) int64 {
	if b < 0 && a > math.MaxInt64+b {
		return math.MaxInt64
	}
	if b > 0 && a < math.MinInt64+b {
		return math.MinInt64
	}
	return a - b
}

func saturatingMul(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	negative := (a < 0) != (b < 0)
	ua, ub := mag64(a), mag64(b)
	if ua > math.MaxUint64/ub {
		if negative {
			return math.MinInt64
		}
		return math.MaxInt64
	}
	m := ua * ub
	if negative {
		// |-MinInt64| == 2^63 is representable only as MinInt64 itself.
		if m > uint64(math.MaxInt64)+1 {
			return math.MinInt64
		}
		return -int64(m)
	}
	if m > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(m)
}

// mag64 returns |v| as an unsigned magnitude; it stays correct for
// math.MinInt64, whose magnitude (2^63) only fits unsigned.
func mag64(v int64) uint64 {
	if v < 0 {
		return uint64(-v)
	}
	return uint64(v)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
