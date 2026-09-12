package ai

import (
	"context"
	"fmt"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/codex"
	"nusashell/pkg/httpclient"
)

type codexSearchBackend struct {
	client *codex.SearchClient
}

// NewCodexSearchFactory creates the provider-bound standalone Codex search
// adapter. Token refresh and account selection happen before this factory is
// called; the adapter reuses the same Codex cookie jar and installation ID as
// the chat provider.
func NewCodexSearchFactory(creds application.CredentialStore) application.CodexSearchFactory {
	client := httpclient.New()
	return func(ctx context.Context, provider *domain.Provider, apiKey string) (application.CodexSearchBackend, error) {
		if provider == nil || provider.Kind != domain.ProviderCodex {
			return nil, fmt.Errorf("Codex web search requires a Codex provider")
		}
		tok, err := resolveCodexToken(ctx, provider, apiKey, creds)
		if err != nil {
			return nil, err
		}
		baseURL := strings.TrimSpace(provider.BaseURL)
		if baseURL == "" {
			baseURL = codex.DefaultBaseURL
		}
		searchClient, err := codex.NewSearchClient(codex.SearchConfig{
			APIKey:         tok.AccessToken,
			BaseURL:        baseURL,
			HTTPClient:     withCodexCookieJar(client),
			AccountID:      tok.AccountID,
			InstallationID: codexInstallationID,
		})
		if err != nil {
			return nil, err
		}
		return &codexSearchBackend{client: searchClient}, nil
	}
}

func (b *codexSearchBackend) Search(ctx context.Context, req application.CodexSearchRequest) (application.CodexSearchResponse, error) {
	if b == nil || b.client == nil {
		return application.CodexSearchResponse{}, fmt.Errorf("Codex web search backend is unavailable")
	}
	response, err := b.client.Search(ctx, codex.SearchRequest{
		ID:        req.ConversationID,
		AccountID: req.AccountID,
		Model:     req.Model,
		Commands: &codex.SearchCommands{SearchQuery: []codex.SearchQuery{{
			Q: req.Query,
		}}},
	})
	if err != nil {
		return application.CodexSearchResponse{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	results := make([]application.CodexSearchResult, 0, min(limit, len(response.Results)))
	for _, result := range response.Results {
		if len(results) >= limit {
			break
		}
		if strings.TrimSpace(result.URL) == "" {
			continue
		}
		if result.Type != "" && result.Type != "text_result" {
			continue
		}
		results = append(results, application.CodexSearchResult{
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Snippet,
		})
	}
	return application.CodexSearchResponse{Summary: response.Output, Results: results}, nil
}
