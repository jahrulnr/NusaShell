package provider

import (
	"regexp"
	"strconv"
	"strings"

	"nusashell/domain"
)

// ── 400 error classification ───────────────────────────────────────────
//
// This classifier reads upstream 400 error-body prose and decides the
// recovery action (strip / inject / cap context). It moved out of domain/
// because it encodes provider-wire knowledge and has no domain consumer.
//
// The tokens-per-minute parser deliberately stays in domain/provider_error.go:
// the domain retry policy (CanAutoRetry) consumes it, the domain layer cannot
// import application code, and a second copy would mean two sources of truth
// for one regex.
//
// The classifier extracts a parameter name from an upstream 400 error body
// and decides whether the recovery action is to strip (unsupported) or
// inject (required) that parameter. Mirrors OmniRoute's
// detectUnsupportedParam + the "reasoning_content must be passed back"
// pattern.

// unsupportedParamRe matches "Unsupported parameter: X",
// "Unsupported parameter(s): 'X'", "Unsupported parameter 'X'", and the
// "Unknown parameter: 'X'" phrasing used by OpenAI-compatible aggregators
// (TokenRouter rejects the OpenRouter reasoning object with
// "Unknown parameter: 'reasoning'").
var unsupportedParamRe = regexp.MustCompile(`(?i)(?:unsupported|unknown)\s+parameter\w*(?:\s*\(s\))?\s*[:'"]+\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// requiredFieldRe matches "reasoning_content must be passed back",
// "reasoning_content is required", "field 'reasoning_content' is required".
//
// LIMITATION: it captures the single identifier immediately before the
// "is required" phrase. When the upstream error separates the field name
// from that phrase with prose — e.g. Gemini's "...missing a thought_signature
// in functionCall parts. This is required for tools..." — the captured word
// is the pronoun "This", not the field. missingFieldRe (checked first) and
// the isParamStopword guard (below) cover that case.
var requiredFieldRe = regexp.MustCompile(`(?i)(?:field\s+['"]?)?([A-Za-z_][A-Za-z0-9_]*)(?:['"]?)?\s+(?:must be passed back|is required|is a required field|must be provided)`)

// quotedRequiredFieldRe matches a backticked or quoted identifier that is
// later described as required / "must be passed back". OpenCode Console Go
// says: The `reasoning_content` in the thinking mode must be passed back.
// requiredFieldRe would capture the noun "mode" (the word immediately
// before "must be passed back"); this pattern captures the quoted field.
var quotedRequiredFieldRe = regexp.MustCompile("(?i)[`'\"]([A-Za-z_][A-Za-z0-9_]*)[`'\"].{0,80}?(?:must be passed back|is required|is a required field|must be provided)")

// missingFieldRe matches the "missing <field>" family of errors where the
// required field directly follows the word "missing" (Gemini-style). This is
// a stronger signal than requiredFieldRe for those bodies because it names
// the field itself rather than the word before "is required". Examples:
//   - "Function call is missing a thought_signature in functionCall parts"
//   - "missing required field 'reasoning_content'"
//   - "missing parameter api_key"
//   - "The request is missing an auth_token"
var missingFieldRe = regexp.MustCompile(`(?i)missing\s+(?:(?:a|an|the)\s+)?(?:required\s+)?(?:field\s+|param(?:eter)?\s+)?['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// noUserQueryRe matches error bodies where the provider requires at least
// one user message but none was found in the request. Examples:
//   - "No user query found in messages."
//   - "No user message found in the conversation"
//   - "Messages must contain at least one user message"
//   - "bad_response_status_code: No user query found in messages."
var noUserQueryRe = regexp.MustCompile(`(?i)no\s+user\s+(?:query|message)\s+found|messages?\s+must\s+contain\s+at\s+least\s+one\s+user\s+message`)

// userMessageAtEndRe matches providers that reject assistant prefilling and
// require the conversation to end with a user message. Claude 4.6-compatible
// gateways use this wording when a retry replays a completed assistant turn.
var userMessageAtEndRe = regexp.MustCompile(`(?i)assistant\s+message\s+prefill|conversation\s+must\s+end\s+with\s+(?:a\s+)?user\s+message`)

// textOnlyModelRe matches "text-only" in error messages from text-only
// models that reject non-text content (images, audio, video). Examples:
//   - "Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part"
//   - "model is text-only; messages[5].content[2] must be a text part"
//   - "this model is text-only and cannot process images"
//
// The param is "vision" because images are the most common non-text
// modality that triggers this error. If audio or video also fail, they
// will be learned separately on subsequent retries.
var textOnlyModelRe = regexp.MustCompile(`(?i)text-only`)

// contextLimitRe matches error bodies that state an explicit context-window
// limit, e.g.:
//
//	"Requested token count exceeds the model's maximum context length of 262144 tokens."
//	"This model's maximum context length is 8192 tokens."
var contextLimitRe = regexp.MustCompile(`(?i)(?:maximum\s+)?context\s+(?:length|window)(?:\s+(?:is|of))?\s*[:=]?\s*(\d[\d_,]*)\b`)

// ExtractContextLimit parses an explicit context-window limit from an
// upstream error body. Returns the limit, the normalized numeric string, and
// ok=true when a number was found.
func ExtractContextLimit(body string) (int, string, bool) {
	if body == "" {
		return 0, "", false
	}
	m := contextLimitRe.FindStringSubmatch(strings.TrimSpace(body))
	if len(m) < 2 {
		return 0, "", false
	}
	num := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(m[1], ",", ""), "_", ""))
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return 0, "", false
	}
	return n, num, true
}

// Classify400Error inspects an upstream 400 error body and returns the
// learned action + parameter name, or (0, "") when the body does not match
// a known pattern.
//
// Order matters:
//  1. "missing <field>" pattern — names the required field directly
//     (Gemini-style "missing a thought_signature"). Checked before the
//     required-field pattern because it captures the field itself, not the
//     word that happens to sit before "is required".
//  2. quoted-field + "must be passed back" — OpenCode Console Go wraps the
//     field in backticks and inserts prose ("in the thinking mode") between
//     the name and the requirement verb. Checked before the adjacent-word
//     required-field pattern so "mode" is not captured.
//  3. "required field" pattern — a model explicitly requiring a field like
//     reasoning_content. Captured identifiers that are prose stopwords
//     ("this", "it", ...) are rejected as false positives.
//  4. "text-only" pattern — the model rejects all non-text content; we
//     disable the vision modality (most common trigger) and retry.
//  5. "unsupported parameter" pattern — the model rejects a specific
//     parameter; we strip it and retry.
//  6. assistant-prefill / "no user query" patterns — the provider requires
//     a user message at the end (or at least one user message); we inject a
//     minimal user message on retry.
func Classify400Error(body string) (domain.LearnedParamAction, string) {
	b := strings.TrimSpace(body)
	if b == "" {
		return "", ""
	}
	if m := missingFieldRe.FindStringSubmatch(b); len(m) > 1 && !domain.IsParamStopword(m[1]) {
		return domain.LearnedActionInject, strings.ToLower(m[1])
	}
	if m := quotedRequiredFieldRe.FindStringSubmatch(b); len(m) > 1 && !domain.IsParamStopword(m[1]) {
		return domain.LearnedActionInject, strings.ToLower(m[1])
	}
	if m := requiredFieldRe.FindStringSubmatch(b); len(m) > 1 && !domain.IsParamStopword(m[1]) {
		return domain.LearnedActionInject, strings.ToLower(m[1])
	}
	if textOnlyModelRe.MatchString(b) {
		return domain.LearnedActionDisableModality, "vision"
	}
	if _, num, ok := ExtractContextLimit(b); ok {
		return domain.LearnedActionCapContext, num
	}
	if m := unsupportedParamRe.FindStringSubmatch(b); len(m) > 1 {
		return domain.LearnedActionStrip, strings.ToLower(m[1])
	}
	if userMessageAtEndRe.MatchString(b) || noUserQueryRe.MatchString(b) {
		return domain.LearnedActionNudgeUser, "user_message"
	}
	return "", ""
}
