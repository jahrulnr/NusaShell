package domain

import (
	"regexp"
	"strings"
)

// ReasoningReplayProviders is the static whitelist of provider IDs whose
// upstreams require reasoning_content to be echoed back on every assistant
// message in subsequent turns. Mirrors OmniRoute's REASONING_REPLAY_PROVIDERS.
var ReasoningReplayProviders = map[string]bool{
	"deepseek":           true,
	"opencode-go":        true,
	"siliconflow":        true,
	"nebius":             true,
	"deepinfra":          true,
	"sambanova":          true,
	"fireworks":          true,
	"together":           true,
	"kimi-coding":        true,
	"kimi-coding-apikey": true,
	"xiaomi-mimo":        true,
}

// ReasoningReplayModelPatterns matches model IDs that require reasoning
// replay even when the provider is not in the whitelist (e.g. gateway-routed
// models). Patterns are case-insensitive.
var ReasoningReplayModelPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)deepseek-r1`),
	regexp.MustCompile(`(?i)deepseek-reasoner`),
	regexp.MustCompile(`(?i)deepseek-chat`),
	regexp.MustCompile(`(?i)deepseek[-/]v4[-.](flash|pro)`),
	regexp.MustCompile(`(?i)kimi[-/]k\d`),
	regexp.MustCompile(`(?i)qwq`),
	regexp.MustCompile(`(?i)qwen.*think`),
	regexp.MustCompile(`(?i)glm.*think`),
	regexp.MustCompile(`(?i)^mimo[-.]?v\d`),
	// ox-alpha is a stealth GLM-5.3 variant (confirmed via models.dev
	// opencode-go/ox-alpha-free interleaved.field = "reasoning_content").
	// OpenRouter does not expose the interleaved signal, so match by name.
	regexp.MustCompile(`(?i)ox-alpha`),
}

// RequiresReasoningReplay reports whether the given provider/model
// combination requires reasoning_content to be echoed back on assistant
// messages in subsequent turns.
//
// Resolution order (mirrors OmniRoute):
//  1. interleavedField == "reasoning_content" → required (catalog signal,
//     preferred source of truth from models.dev)
//  2. interleavedField == "reasoning_details" → not required
//  3. Known provider whitelist
//  4. Known model pattern fallback
//
// When interleavedField is empty and neither the provider nor the model
// matches a known pattern, the model does not require replay. Providers
// that ignore an absent reasoning_content (OpenAI, Anthropic) are
// unaffected — the field is simply omitted.
func RequiresReasoningReplay(provider, model, interleavedField string) bool {
	field := strings.ToLower(strings.TrimSpace(interleavedField))
	if field == "reasoning_content" {
		return true
	}
	if field == "reasoning_details" {
		return false
	}
	normalizedProvider := strings.ToLower(strings.TrimSpace(provider))
	if ReasoningReplayProviders[normalizedProvider] {
		return true
	}
	normalizedModel := strings.TrimSpace(model)
	for _, p := range ReasoningReplayModelPatterns {
		if p.MatchString(normalizedModel) {
			return true
		}
	}
	return false
}

// ReasoningPlaceholder is the non-empty sentinel injected when a model
// requires reasoning_content to be present but the prior reasoning text is
// unavailable (empty or stripped by the client). Models may echo it; the
// response decoder strips it from user-visible output. Mirrors OmniRoute's
// NON_ANTHROPIC_THINKING_PLACEHOLDER.
const ReasoningPlaceholder = "(Continue from the current context.)"

// IsReasoningPlaceholder reports whether value is the internal replay
// sentinel. Use to avoid caching or re-replaying the placeholder (which
// causes an echo loop — OmniRoute #9573).
func IsReasoningPlaceholder(value string) bool {
	return strings.TrimSpace(value) == ReasoningPlaceholder
}
