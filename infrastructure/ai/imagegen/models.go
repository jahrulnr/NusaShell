package imagegen

import (
	"context"
	"net/http"
	"strings"

	aiutil "nusashell/infrastructure/ai/internal"
)

// ModelLister fetches image-generation model IDs from a provider's
// /images/models endpoint (OpenRouter Image API discovery). A 404 or any
// HTTP error returns an empty slice so OpenAI-compatible hosts that lack
// the endpoint still work via /models + the known-image allowlist.
type ModelLister struct {
	BaseURL string
	Client  *http.Client
}

// NewModelLister creates an image-model lister. If client is nil a default
// HTTP client is used.
func NewModelLister(baseURL string, client *http.Client) *ModelLister {
	if client == nil {
		client = &http.Client{}
	}
	return &ModelLister{BaseURL: strings.TrimRight(baseURL, "/"), Client: client}
}

// ListImageModels fetches image model IDs from /images/models.
func (l *ModelLister) ListImageModels(ctx context.Context, apiKey string) ([]string, error) {
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	return aiutil.FetchImageModels(ctx, l.Client, l.BaseURL, headers, nil), nil
}
