package embeddings

import (
	"context"
	"net/http"
	"strings"

	aiutil "nusashell/infrastructure/ai/internal"
)

// OllamaTagLister enumerates embedding models from Ollama's native
// /api/tags endpoint. Ollama exposes no /embeddings/models route; instead
// every installed model in /api/tags carries a capabilities array where
// "embedding" marks models that can serve /api/embed and the
// OpenAI-compatible /v1/embeddings. This is the authoritative signal — it
// detects embedding models regardless of their names (no allowlist needed).
type OllamaTagLister struct {
	BaseURL string // Ollama root, e.g. "http://localhost:11434"
	Client  *http.Client
}

// NewOllamaTagLister creates a lister for Ollama's /api/tags endpoint.
// If client is nil a default HTTP client is used.
func NewOllamaTagLister(baseURL string, client *http.Client) *OllamaTagLister {
	if client == nil {
		client = &http.Client{}
	}
	return &OllamaTagLister{BaseURL: NormalizeOllamaBase(baseURL), Client: client}
}

// NormalizeOllamaBase strips trailing slashes and any "/v1" suffix so both
// "http://localhost:11434" and "http://localhost:11434/v1" reach /api/tags.
func NormalizeOllamaBase(base string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	return strings.TrimSuffix(b, "/v1")
}

// ListEmbeddingModels returns the IDs of all installed Ollama models whose
// capabilities include "embedding". A missing or failing endpoint returns an
// empty slice without error — the caller keeps whatever /v1/models returned.
func (l *OllamaTagLister) ListEmbeddingModels(ctx context.Context, apiKey string) ([]string, error) {
	var out struct {
		Models []struct {
			Name         string   `json:"name"`
			Capabilities []string `json:"capabilities"`
		} `json:"models"`
	}
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	if err := aiutil.DoJSON(ctx, l.Client, http.MethodGet, l.BaseURL+"/api/tags", headers, nil, &out); err != nil {
		return nil, nil
	}
	var ids []string
	for _, m := range out.Models {
		if m.Name == "" {
			continue
		}
		for _, c := range m.Capabilities {
			if c == "embedding" {
				ids = append(ids, m.Name)
				break
			}
		}
	}
	return ids, nil
}
