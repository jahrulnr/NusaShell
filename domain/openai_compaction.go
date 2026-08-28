package domain

import (
	"regexp"
	"strconv"
)

// openAIMajorVersionRe captures the major version number from an OpenAI
// gpt-style model ID (e.g. "gpt-5.2" → "5"). It intentionally matches only
// the leading "gpt-" family so o-series, claude, and other vendors are not
// misclassified.
var openAIMajorVersionRe = regexp.MustCompile(`^gpt-(\d+)`)

// OpenAISupportsNativeCompaction reports whether a model ID is eligible for
// the OpenAI-native server-side compaction endpoint (POST /responses/compact).
// Only gpt-family models with major version >= 5 advertise the standalone
// compact endpoint; older gpt-4.x, o-series, and non-OpenAI models fall back
// to the client-side summarization path.
func OpenAISupportsNativeCompaction(modelID string) bool {
	m := openAIMajorVersionRe.FindStringSubmatch(modelID)
	if m == nil {
		return false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return false
	}
	return major >= 5
}
