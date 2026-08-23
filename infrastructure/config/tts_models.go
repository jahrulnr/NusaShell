package config

import "strings"

// KnownTTSModels is a provider-agnostic allowlist of public text-to-speech
// model names (without the provider prefix). OpenAI's /models list includes
// these; OpenRouter exposes them via the models.dev catalog instead. Tagging
// them as kind=tts keeps them out of the chat picker and in Settings →
// Speech generation, mirroring KnownImageModels.
var KnownTTSModels = []string{
	"tts-1",
	"tts-1-hd",
	"gpt-4o-mini-tts",
}

// looksLikeTTSName reports whether an identifier matches conservative TTS
// name patterns. Only suffix/segment patterns that unambiguously denote a
// speech-generation model are matched ("-tts", "tts-") so chat models with
// "audio" in their names (e.g. gpt-audio) are never reclassified.
func looksLikeTTSName(fullID, base string) bool {
	switch {
	case strings.HasSuffix(base, "-tts"), strings.HasPrefix(base, "tts-"), strings.Contains(base, "-tts-"):
		return true
	case strings.Contains(base, "/"):
		// Gateway ids like "fish-audio/s2.1-pro" carry no tts marker; do not
		// guess — the models.dev catalog owns those.
		return false
	default:
		return false
	}
}

// IsKnownTTSModel reports whether the identifier is a speech-generation
// model. Accepts gateway-style ids ("openai/tts-1") and direct names
// ("tts-1"). The base-name check runs after stripping the provider prefix;
// pattern matching covers the remaining full id conservatively.
func IsKnownTTSModel(modelID string) bool {
	modelID = strings.TrimSpace(strings.ToLower(modelID))
	if modelID == "" {
		return false
	}
	base := modelID
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	for _, m := range KnownTTSModels {
		if base == m {
			return true
		}
	}
	return looksLikeTTSName(modelID, base)
}
