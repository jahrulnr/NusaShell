package domain

// Prompt cache key policy constants.
//
// The prompt-cache key is a stable hash-derived identifier shared across
// turns of the same conversation (or background run) so the provider can
// reuse cached prefixes. The key is composed of a visible namespace prefix
// plus a hash suffix, capped at PromptCacheKeyLength bytes. Both namespaces
// are ASCII, so byte and character counts are identical and safe for
// provider key limits.
const (
	// PromptCacheKeyLength is the total byte budget for a prompt-cache key
	// (prefix + hash suffix).
	PromptCacheKeyLength = 32
	// PromptCacheConversationPrefix is the namespace for interactive
	// conversation runs.
	PromptCacheConversationPrefix = "nusashell_cv_"
	// PromptCacheBackgroundPrefix is the namespace for headless background
	// runs (review agent, automations).
	PromptCacheBackgroundPrefix = "nusashell_bg_"
)
