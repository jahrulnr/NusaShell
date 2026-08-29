// Package embeddings provides adapters for fetching embedding models and
// producing embedding vectors from AI providers. It is intentionally
// separate from the chat/responses/messages adapters so that embedding
// concerns are not coupled to a specific chat protocol — AI gateways often
// support multiple chat APIs (messages, responses, chat) while exposing
// embeddings on a single OpenAI-compatible /v1/embeddings endpoint.
package embeddings

import (
	"context"
	"net/http"
	"strings"

	aiutil "nusashell/infrastructure/ai/internal"
)

// ModelLister fetches the list of embedding model IDs from a provider's
// /embeddings/models endpoint. It works with any OpenAI-compatible gateway
// (OpenRouter, TokenRouter, OmniRoute, OpenAI platform) regardless of which
// chat API the provider is configured to use.
//
// The base URL should be the provider's API root (e.g.
// "https://openrouter.ai/api/v1" or "http://localhost:20130/v1"). A 404 or
// any HTTP error returns an empty slice — the caller should just use
// whatever /models returned.
type ModelLister struct {
	BaseURL string
	Client  *http.Client
}

// NewModelLister creates an embedding model lister. If client is nil a
// default HTTP client is used.
func NewModelLister(baseURL string, client *http.Client) *ModelLister {
	if client == nil {
		client = &http.Client{}
	}
	return &ModelLister{BaseURL: strings.TrimRight(baseURL, "/"), Client: client}
}

// ListEmbeddingModels fetches embedding model IDs from /embeddings/models.
// Returns an empty slice (not an error) if the endpoint is not available.
func (l *ModelLister) ListEmbeddingModels(ctx context.Context, apiKey string) ([]string, error) {
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	return aiutil.FetchEmbeddingModels(ctx, l.Client, l.BaseURL, headers, nil), nil
}
