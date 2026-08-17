package config

import "strings"

// KnownEmbeddingModels is a provider-agnostic allowlist of public embedding
// model names (without the provider prefix). If a provider's /models or
// /embeddings/models response contains one of these names, NusaShell can
// pre-mark the model as an embedding model without relying on fragile name
// heuristics (e.g. looking for the substring "embed").
//
// The initial list is sourced from OpenRouter's /api/v1/embeddings/models.
// OpenRouter uses the form "provider/model" (e.g. "openai/text-embedding-3-small"),
// but the official providers use just the model name (e.g. "text-embedding-3-small").
// This list stores only the model name so it works for both gateway and
// direct-provider lookups.
var KnownEmbeddingModels = []string{
	"voyage-code-4",
	"voyage-multimodal-3.5",
	"voyage-4-lite",
	"voyage-4",
	"voyage-4-large",
	"nemotron-3-embed-1b:free",
	"gemini-embedding-2",
	"gemini-embedding-2-preview",
	"pplx-embed-v1-4b",
	"pplx-embed-v1-0.6b",
	"llama-nemotron-embed-vl-1b-v2:free",
	"gte-base",
	"gte-large",
	"e5-large-v2",
	"e5-base-v2",
	"multilingual-e5-large",
	"paraphrase-minilm-l6-v2",
	"all-minilm-l12-v2",
	"bge-base-en-v1.5",
	"multi-qa-mpnet-base-dot-v1",
	"bge-large-en-v1.5",
	"bge-m3",
	"all-mpnet-base-v2",
	"all-minilm-l6-v2",
	"mistral-embed-2312",
	"gemini-embedding-001",
	"text-embedding-ada-002",
	"codestral-embed-2505",
	"text-embedding-3-large",
	"text-embedding-3-small",
	"qwen3-embedding-8b",
	"qwen3-embedding-4b",
}

var knownEmbeddingModelSet map[string]struct{}

func init() {
	knownEmbeddingModelSet = make(map[string]struct{}, len(KnownEmbeddingModels))
	for _, m := range KnownEmbeddingModels {
		knownEmbeddingModelSet[m] = struct{}{}
	}
}

// IsKnownEmbeddingModel reports whether the given model identifier is a known
// embedding model. It accepts both gateway-style identifiers (e.g.
// "openai/text-embedding-3-small") and direct-provider names (e.g.
// "text-embedding-3-small"). Unknown or empty identifiers return false.
func IsKnownEmbeddingModel(modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}
	// Strip any provider prefix (e.g. "openai/text-embedding-3-small").
	if i := strings.LastIndex(modelID, "/"); i >= 0 {
		modelID = modelID[i+1:]
	}
	_, ok := knownEmbeddingModelSet[modelID]
	return ok
}
