package application

import (
	"context"
	"fmt"
	"strings"

	"nusashell/domain"
)

// SearchCodexWeb resolves the active Codex account for a conversation and
// delegates one query to the provider-bound Codex search adapter. Non-Codex
// providers are rejected so Toolbox can keep the existing searchwire path.
func (a *App) SearchCodexWeb(ctx context.Context, req CodexSearchRequest) (CodexSearchResponse, error) {
	if err := ctx.Err(); err != nil {
		return CodexSearchResponse{}, err
	}
	if a == nil || a.Providers == nil || a.Credentials == nil || a.CodexSearchFactory == nil {
		return CodexSearchResponse{}, fmt.Errorf("Codex web search is not configured")
	}
	if strings.TrimSpace(req.ProviderID) == "" {
		return CodexSearchResponse{}, fmt.Errorf("Codex web search provider is missing")
	}
	provider, err := a.Providers.Get(req.ProviderID)
	if err != nil {
		return CodexSearchResponse{}, fmt.Errorf("resolve Codex provider: %w", err)
	}
	if provider == nil || provider.Kind != domain.ProviderCodex {
		return CodexSearchResponse{}, fmt.Errorf("Codex web search requires an active Codex provider")
	}
	if strings.TrimSpace(req.Query) == "" {
		return CodexSearchResponse{}, fmt.Errorf("query is required")
	}
	if err := ctx.Err(); err != nil {
		return CodexSearchResponse{}, err
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" {
		accountID = a.selectedCodexAccount(req.ConversationID)
	}
	var apiKey string
	var has bool
	if accountID != "" {
		apiKey, has, err = a.Credentials.Get(accountKey(provider.ID, accountID))
		if err != nil {
			return CodexSearchResponse{}, fmt.Errorf("read Codex account credential: %w", err)
		}
		if !has || strings.TrimSpace(apiKey) == "" {
			return CodexSearchResponse{}, fmt.Errorf("Codex account %q credential is not configured", accountID)
		}
		req.AccountID = accountID
	} else {
		apiKey, has, err = a.Credentials.Get(provider.ID)
		if err != nil {
			return CodexSearchResponse{}, fmt.Errorf("read Codex credential: %w", err)
		}
		if !has || strings.TrimSpace(apiKey) == "" {
			return CodexSearchResponse{}, fmt.Errorf("Codex credential is not configured")
		}
		if err := ctx.Err(); err != nil {
			return CodexSearchResponse{}, err
		}
		apiKey, err = a.prepareCodexTurnAPIKey(req.ConversationID, provider, apiKey)
		if err != nil {
			return CodexSearchResponse{}, err
		}
		if err := ctx.Err(); err != nil {
			return CodexSearchResponse{}, err
		}
		if a.CodexRouter != nil {
			req.AccountID = a.CodexRouter.StickyAccount(req.ConversationID)
			if req.AccountID != "" {
				accountKeyValue, accountHas, accountErr := a.Credentials.Get(accountKey(provider.ID, req.AccountID))
				if accountErr != nil {
					return CodexSearchResponse{}, fmt.Errorf("read Codex account credential: %w", accountErr)
				}
				if !accountHas || strings.TrimSpace(accountKeyValue) == "" {
					return CodexSearchResponse{}, fmt.Errorf("Codex account %q credential is not configured", req.AccountID)
				}
				apiKey = accountKeyValue
			}
		}
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}
	backend, err := a.CodexSearchFactory(ctx, provider, apiKey)
	if err != nil {
		return CodexSearchResponse{}, err
	}
	if backend == nil {
		return CodexSearchResponse{}, fmt.Errorf("Codex web search backend is unavailable")
	}
	return backend.Search(ctx, req)
}
