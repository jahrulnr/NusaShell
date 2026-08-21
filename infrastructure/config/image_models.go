package config

import "strings"

// KnownImageModels is a provider-agnostic allowlist of public image-generation
// model names (without the provider prefix). OpenAI's /models list includes
// these; OpenRouter exposes a separate /images/models catalog. Tagging them
// as kind=image keeps them out of the chat picker and in Settings → Image
// generation.
var KnownImageModels = []string{
	"gpt-image-1",
	"gpt-image-1.5",
	"gpt-image-1-mini",
	"gpt-image-2",
	"dall-e-2",
	"dall-e-3",
}

var knownImageModelSet map[string]struct{}

func init() {
	knownImageModelSet = make(map[string]struct{}, len(KnownImageModels))
	for _, m := range KnownImageModels {
		knownImageModelSet[m] = struct{}{}
	}
}

// IsKnownImageModel reports whether the identifier is an image generator.
// Accepts gateway-style ids ("openai/gpt-image-1") and direct names
// ("gpt-image-1"). Conservative name patterns cover Flux/Imagen-style ids
// without matching vision chat models (e.g. llama-3.2-vision).
func IsKnownImageModel(modelID string) bool {
	modelID = strings.TrimSpace(strings.ToLower(modelID))
	if modelID == "" {
		return false
	}
	base := modelID
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if _, ok := knownImageModelSet[base]; ok {
		return true
	}
	return looksLikeImageGenName(modelID, base)
}

func looksLikeImageGenName(fullID, base string) bool {
	switch {
	case strings.Contains(fullID, "gpt-image"), strings.Contains(base, "gpt-image"):
		return true
	case strings.Contains(fullID, "dall-e"), strings.Contains(base, "dall-e"):
		return true
	case strings.Contains(fullID, "stable-diffusion"), strings.Contains(base, "stable-diffusion"):
		return true
	case strings.Contains(fullID, "seedream"), strings.Contains(base, "seedream"):
		return true
	case strings.Contains(fullID, "ideogram"), strings.Contains(base, "ideogram"):
		return true
	case strings.Contains(fullID, "recraft"), strings.Contains(base, "recraft"):
		return true
	case strings.Contains(fullID, "riverflow"), strings.Contains(base, "riverflow"):
		return true
	case strings.Contains(fullID, "flash-image"), strings.Contains(base, "flash-image"):
		return true
	case strings.Contains(fullID, "imagen-"), strings.HasPrefix(base, "imagen-"):
		return true
	case strings.Contains(fullID, "flux.1"), strings.Contains(fullID, "flux-1"), strings.HasPrefix(base, "flux"):
		return true
	default:
		return false
	}
}
