package videogen

import (
	"context"
	"net/http"
	"strings"

	aiutil "nusashell/infrastructure/ai/internal"
)

// ModelLister fetches video-generation model IDs from the dedicated
// /videos/models endpoint (OpenRouter's documented discovery route — it
// also carries pricing_skus and supported resolutions/durations). Plain
// /models lists these models too but without pricing detail; hosts without
// the endpoint yield an empty list and plain-/models ids are still tagged
// via the models.dev catalog carve-out (kind=video).
type ModelLister struct {
	BaseURL string
	Client  *http.Client
}

// NewModelLister creates a video-model lister. If client is nil a default
// HTTP client is used.
func NewModelLister(baseURL string, client *http.Client) *ModelLister {
	if client == nil {
		client = &http.Client{}
	}
	return &ModelLister{BaseURL: strings.TrimRight(baseURL, "/"), Client: client}
}

// ListVideoModels fetches video-capable model IDs.
func (l *ModelLister) ListVideoModels(ctx context.Context, apiKey string) ([]string, error) {
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	url := l.BaseURL + "/videos/models"
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := aiutil.DoJSON(ctx, l.Client, http.MethodGet, url, headers, nil, &out); err != nil {
		return nil, nil // tolerant like image/speech listers
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}
