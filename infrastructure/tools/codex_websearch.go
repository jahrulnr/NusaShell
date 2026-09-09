package tools

import (
	"context"
	"strings"

	"nusashell/application"
	"nusashell/domain"
)

const codexWebSearchSummaryMaxBytes = 2048

func (t *Toolbox) codexWebSearch(ctx context.Context, query string, limit int) (application.CodexSearchResponse, bool, error) {
	if t == nil || t.CodexSearch == nil || t.Providers == nil {
		return application.CodexSearchResponse{}, false, nil
	}
	providerID := application.ProviderIDFromContext(ctx)
	if providerID == "" {
		return application.CodexSearchResponse{}, false, nil
	}
	provider, err := t.Providers.Get(providerID)
	if err != nil || provider == nil || provider.Kind != domain.ProviderCodex {
		return application.CodexSearchResponse{}, false, nil
	}
	response, err := t.CodexSearch.SearchCodexWeb(ctx, application.CodexSearchRequest{
		ProviderID:     providerID,
		ConversationID: application.ConversationIDFromContext(ctx),
		Model:          application.ModelFromContext(ctx),
		Query:          query,
		Limit:          limit,
	})
	return response, true, err
}

func formatCodexWebSearch(response application.CodexSearchResponse, limit int) string {
	if limit <= 0 {
		limit = 10
	}
	items := make([]any, 0, min(limit, len(response.Results)))
	for _, result := range response.Results {
		if len(items) >= limit {
			break
		}
		if strings.TrimSpace(result.URL) == "" {
			continue
		}
		items = append(items, map[string]any{
			"title":   result.Title,
			"url":     result.URL,
			"snippet": result.Snippet,
			"sources": []string{"codex"},
		})
	}
	meta := map[string]any{
		"count":    len(items),
		"provider": "codex",
	}
	if summary := strings.TrimSpace(response.Summary); summary != "" {
		if len(summary) > codexWebSearchSummaryMaxBytes {
			meta["summary_truncated"] = true
			summary = clipUTF8Prefix(summary, codexWebSearchSummaryMaxBytes)
		}
		meta["summary"] = summary
	}
	return capJSONL("web_search", meta, items)
}
