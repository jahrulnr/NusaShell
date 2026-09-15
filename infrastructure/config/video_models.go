package config

import "strings"

// IsKnownVideoModel reports whether the identifier is a video-generation
// model served by a long-running predict/poll surface. Accepts
// gateway-style ids ("google/veo-3.1-generate-preview") and direct names
// ("veo-3.1-generate-preview").
//
// Only the Veo family is classified: the :predictLongRunning surface is
// documented for veo-* ids (veo-3.1-*, veo-3.0-*, including the -lite-
// variant). The gemini-omni-* family appears in the Gemini model list but
// is not confirmed to be served by :predictLongRunning, so it is left
// unclassified — it will surface as a chat model until the wire contract
// is verified.
func IsKnownVideoModel(modelID string) bool {
	modelID = strings.TrimSpace(strings.ToLower(modelID))
	if modelID == "" {
		return false
	}
	base := modelID
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return strings.HasPrefix(base, "veo-")
}
