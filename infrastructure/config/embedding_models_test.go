package config

import "testing"

func TestIsKnownEmbeddingModel(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		// Gateway-style and direct names.
		{"text-embedding-3-small", true},
		{"openai/text-embedding-3-small", true},
		{"voyage-4-lite", true},
		{"bge-m3", true},
		{"e5-large-v2", true},
		{"gte-base", true},

		// Ollama tagged names (the local import path).
		{"nomic-embed-text", true},
		{"nomic-embed-text:latest", true},
		{"qwen3-embedding:0.6b", true},
		{"mxbai-embed-large:latest", true},
		{"snowflake-arctic-embed2:latest", true},
		{"embeddinggemma:latest", true},

		// llama.cpp GGUF filenames.
		{"Qwen3-Embedding-0.6B-Q8_0.gguf", true},
		{"bge-m3-Q8_0.gguf", true},
		{"nomic-embed-text-v1.5.Q4_K_M.gguf", true},
		{"snowflake-arctic-embed-l-v2.0-f16.gguf", true},

		// Custom llama.cpp aliases with the "embed" substring.
		{"my-local-embed", true},
		{"My-Embedding-Alias", true},

		// Non-embedding models must stay out of the embedding picker.
		{"gemma4:e2b", false},
		{"llama3.2:latest", false},
		{"gpt-5.5", false},
		{"claude-opus-4", false},
		{"deepseek/deepseek-v4-flash", false},
		{"llama-3.2-vision", false},
		{"", false},
		{"   ", false},
	}
	for _, c := range cases {
		if got := IsKnownEmbeddingModel(c.id); got != c.want {
			t.Errorf("IsKnownEmbeddingModel(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestNormalizeModelName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"nomic-embed-text:latest", "nomic-embed-text"},
		{"qwen3-embedding:0.6b", "qwen3-embedding"},
		{"Qwen3-Embedding-0.6B-Q8_0.gguf", "qwen3-embedding-0.6b"},
		{"bge-m3-Q8_0.gguf", "bge-m3"},
		{"nomic-embed-text-v1.5.Q4_K_M.gguf", "nomic-embed-text-v1.5"},
		{"openai/text-embedding-3-small", "text-embedding-3-small"},
		{"text-embedding-3-small", "text-embedding-3-small"},
		{"snowflake-arctic-embed-l-v2.0-f16.gguf", "snowflake-arctic-embed-l-v2.0"},
	}
	for _, c := range cases {
		if got := normalizeModelName(c.in); got != c.want {
			t.Errorf("normalizeModelName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
