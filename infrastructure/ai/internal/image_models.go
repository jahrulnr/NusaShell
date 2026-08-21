package aiutil

import (
	"context"
	"net/http"
)

// FetchImageModels fetches the /images/models endpoint that some gateways
// (e.g. OpenRouter) expose separately from /models. A 404 or any HTTP error
// returns nil — the caller should use whatever /models returned plus the
// known image-model allowlist.
func FetchImageModels(ctx context.Context, client *http.Client, baseURL string, headers map[string]string, seen map[string]bool) []string {
	url := baseURL + "/images/models"
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := DoJSON(ctx, client, http.MethodGet, url, headers, nil, &out); err != nil {
		return nil
	}
	if seen == nil {
		seen = make(map[string]bool, len(out.Data))
	}
	var ids []string
	for _, m := range out.Data {
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		ids = append(ids, m.ID)
	}
	return ids
}
