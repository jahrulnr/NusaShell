package config

import (
	"regexp"
	"strings"
)

// KnownEmbeddingModels is a provider-agnostic allowlist of public embedding
// model names (without the provider prefix). If a provider's /models or
// /embeddings/models response contains one of these names, NusaShell can
// pre-mark the model as an embedding model without relying on fragile name
// heuristics alone.
//
// Sources (community, open-source — all runnable on llama.cpp GGUF and/or
// Ollama): ollama.com/search?c=embedding, OpenRouter /api/v1/embeddings/models,
// and the models.dev catalog. Names are stored bare so they match both
// gateway-style ids ("openai/text-embedding-3-small") and direct-provider
// names ("text-embedding-3-small").
//
// Matching is normalized: Ollama tags ("nomic-embed-text:latest",
// "qwen3-embedding:0.6b"), GGUF filenames ("Qwen3-Embedding-0.6B-Q8_0.gguf")
// and quantization suffixes are stripped before lookup. Models whose base
// name contains "embed" are also detected by pattern, covering custom
// llama.cpp aliases without an explicit catalog entry.
var KnownEmbeddingModels = []string{
	// OpenAI
	"text-embedding-ada-002",
	"text-embedding-3-small",
	"text-embedding-3-large",
	// Google
	"gemini-embedding-001",
	"gemini-embedding-2",
	"gemini-embedding-2-preview",
	"text-embedding-005",
	"text-multilingual-embedding-002",
	// Voyage AI
	"voyage-3",
	"voyage-3-lite",
	"voyage-3-large",
	"voyage-3.5",
	"voyage-3.5-lite",
	"voyage-4",
	"voyage-4-lite",
	"voyage-4-large",
	"voyage-code-2",
	"voyage-code-3",
	"voyage-code-4",
	"voyage-finance-2",
	"voyage-law-2",
	"voyage-multimodal-3.5",
	// Mistral
	"mistral-embed",
	"mistral-embed-2312",
	"codestral-embed",
	"codestral-embed-2505",
	// Cohere
	"embed-v4.0",
	"cohere-embed-v-4-0",
	"embed-english-v3.0",
	"embed-multilingual-v3.0",
	"cohere-embed-v3-english",
	"cohere-embed-v3-multilingual",
	// Qwen3 Embedding (Ollama tags :0.6b/:4b/:8b; GGUF per size)
	"qwen3-embedding-0.6b",
	"qwen3-embedding-4b",
	"qwen3-embedding-8b",
	"qwen3-vl-embedding-8b",
	// BAAI BGE family
	"bge-m3",
	"bge-small-en-v1.5",
	"bge-base-en-v1.5",
	"bge-large-en-v1.5",
	"bge-small-zh-v1.5",
	"bge-base-zh-v1.5",
	"bge-large-zh-v1.5",
	"bge-multilingual-gemma2",
	"bge_multilingual_gemma2",
	// E5 family (intfloat)
	"e5-small-v2",
	"e5-base-v2",
	"e5-large-v2",
	"multilingual-e5-small",
	"multilingual-e5-base",
	"multilingual-e5-large",
	"multilingual-e5-large-instruct",
	"e5-mistral-7b-instruct",
	// GTE family
	"gte-base",
	"gte-large",
	"gte-large-en-v1.5",
	"gte-multilingual-base",
	"gte-modernbert-base",
	// sentence-transformers classics
	"all-minilm-l6-v2",
	"all-mini-lm-l6-v2",
	"all-minilm-l12-v2",
	"mini_lm_l12_v2",
	"all-mpnet-base-v2",
	"multi-qa-mpnet-base-dot-v1",
	"paraphrase-minilm-l6-v2",
	"paraphrase-multilingual-minilm-l12-v2",
	// Snowflake Arctic Embed
	"snowflake-arctic-embed-xs",
	"snowflake-arctic-embed-s",
	"snowflake-arctic-embed-m",
	"snowflake-arctic-embed-l",
	"snowflake-arctic-embed-m-l-v2.0",
	"snowflake-arctic-embed-l-v2.0",
	// IBM Granite
	"granite-embedding-125m-english",
	"granite-embedding-275m-multilingual",
	"granite-embedding-338m-multilingual",
	"granite-embedding-r2-english",
	// Jina
	"jina-embeddings-v2-base-code",
	"jina-embeddings-v2-base-zh",
	"jina-embeddings-v3",
	// NVIDIA
	"nv-embed-v1",
	"nv-embedcode-7b-v1",
	"llama-3.2-nv-embedqa-1b",
	"llama-3_2-nemoretriever-300m-embed-v1",
	"llama-nemotron-embed-vl-1b-v2",
	"nemotron-3-embed-1b:free",
	// Perplexity
	"pplx-embed-v1-0.6b",
	"pplx-embed-v1-4b",
	// Amazon
	"titan-embed-text-v2",
}

var knownEmbeddingModelSet map[string]struct{}

// ggufQuantSuffix matches GGUF quantization suffixes commonly appended to
// embedding model filenames served via llama.cpp ("...-Q8_0.gguf",
// "..._q4_k_m.gguf", "...-F16.gguf", "...-iq4_xs.gguf"). Applied repeatedly
// because some filenames stack size + quant ("-0.6B-Q8_0").
var ggufQuantSuffix = regexp.MustCompile(`[-_.](iq[1-4]_[a-z0-9]+|q[1-8][_a-z0-9]*|f(?:p)?(?:16|32)|bf16)$`)

func init() {
	knownEmbeddingModelSet = make(map[string]struct{}, len(KnownEmbeddingModels))
	for _, m := range KnownEmbeddingModels {
		knownEmbeddingModelSet[m] = struct{}{}
	}
}

// normalizeModelName lowercases the identifier and strips everything that
// hides the base model name: gateway prefixes ("openai/…"), Ollama tags
// ("nomic-embed-text:latest"), and GGUF filename artifacts
// ("Qwen3-Embedding-0.6B-Q8_0.gguf" → "qwen3-embedding-0.6b").
func normalizeModelName(modelID string) string {
	s := strings.ToLower(strings.TrimSpace(modelID))
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if strings.HasSuffix(s, ".gguf") {
		s = strings.TrimSuffix(s, ".gguf")
	}
	// Strip Ollama-style tag after the last colon ("name:latest", ":free").
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[i+1:], "/") {
		s = s[:i]
	}
	// Strip GGUF quantization suffixes ("...-q8_0", "...-f16"); repeat in
	// case size + quant stack ("-0.6b-q8_0").
	for {
		stripped := ggufQuantSuffix.ReplaceAllString(s, "")
		if stripped == s {
			break
		}
		s = strings.TrimRight(stripped, "-_.")
	}
	return strings.Trim(s, "-_. ")
}

// IsKnownEmbeddingModel reports whether the given model identifier is a known
// embedding model. It accepts:
//   - gateway-style identifiers ("openai/text-embedding-3-small"),
//   - direct-provider names ("nomic-embed-text"),
//   - Ollama tagged names ("nomic-embed-text:latest", "qwen3-embedding:0.6b"),
//   - llama.cpp GGUF filenames ("Qwen3-Embedding-0.6B-Q8_0.gguf"),
//   - any base name containing "embed" (custom llama.cpp aliases such as
//     "my-local-embed" — no chat LLM uses that substring).
//
// Unknown or empty identifiers return false.
func IsKnownEmbeddingModel(modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}
	base := normalizeModelName(modelID)
	if base == "" {
		return false
	}
	if _, ok := knownEmbeddingModelSet[base]; ok {
		return true
	}
	// Pattern fallback: embedding models across every community source
	// carry "embed" in their base name; chat/image/TTS models do not.
	return strings.Contains(base, "embed")
}
