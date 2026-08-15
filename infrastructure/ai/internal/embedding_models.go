package aiutil

import (
	"context"
	"net/http"
)

// FetchEmbeddingModels fetches the /embeddings/models endpoint that some
// gateways (e.g. OpenRouter) expose separately from /models. It returns the
// IDs of embedding models found. A 404 or any HTTP error returns nil — the
// caller should just use whatever /models returned.
//
// seen is an optional set of already-known model IDs (from /models) so we
// don't duplicate entries. If seen is nil, a fresh set is allocated. New
// embedding model IDs are also added to seen so callers can pass the same
// set to subsequent calls.
func FetchEmbeddingModels(ctx context.Context, client *http.Client, baseURL string, headers map[string]string, seen map[string]bool) []string {
	embURL := baseURL + "/embeddings/models"
	var embOut struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := DoJSON(ctx, client, http.MethodGet, embURL, headers, nil, &embOut); err != nil {
		return nil
	}
	if seen == nil {
		seen = make(map[string]bool, len(embOut.Data))
	}
	var ids []string
	for _, m := range embOut.Data {
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		ids = append(ids, m.ID)
	}
	return ids
}
