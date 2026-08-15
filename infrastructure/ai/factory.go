package ai

import (
	"context"
	"net"
	"net/http"
	"time"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/chatcompletion"
	"nusashell/infrastructure/ai/codex"
	"nusashell/infrastructure/ai/messages"
	"nusashell/infrastructure/ai/responses"
)

// NewFactory returns a ProviderFactory closure that can refresh Codex OAuth
// tokens before building the adapter. The closure captures the CredentialStore
// so Codex token refresh is transparent to the application layer — the app
// never needs to know about OAuth details.
func NewFactory(creds application.CredentialStore) application.ProviderFactory {
	return func(ctx context.Context, p *domain.Provider, apiKey string) (application.AIProvider, error) {
		client := newProviderHTTPClient()
		switch p.Kind {
		case domain.ProviderMessages:
			return &messages.Adapter{BaseURL: p.BaseURL, APIKey: apiKey, Client: client}, nil
		case domain.ProviderResponses:
			return &responses.Adapter{BaseURL: p.BaseURL, APIKey: apiKey, Client: client}, nil
		case domain.ProviderChat:
			return &chatcompletion.Adapter{BaseURL: p.BaseURL, APIKey: apiKey, Client: client}, nil
		case domain.ProviderCodex:
			return newCodexAdapter(ctx, p, apiKey, client, creds)
		default:
			return nil, &application.ErrUnsupportedProvider{Kind: string(p.Kind)}
		}
	}
}

// newCodexAdapter parses the stored OAuth token, refreshes it if expired,
// persists the refreshed token, and builds the Codex adapter.
func newCodexAdapter(ctx context.Context, p *domain.Provider, storedJSON string, client *http.Client, creds application.CredentialStore) (application.AIProvider, error) {
	if storedJSON == "" {
		return &codex.Adapter{
			BaseURL: p.BaseURL,
			Client:  client,
		}, nil
	}
	tok, err := codex.UnmarshalToken(storedJSON)
	if err != nil {
		return nil, &application.ErrUnsupportedProvider{Kind: string(p.Kind)}
	}
	// Auto-refresh if the access token is expired or will expire within 5 min.
	// The 5-min margin avoids mid-stream token expiry on long generations.
	if tok.RefreshToken != "" && tok.IsExpired(5*time.Minute) {
		refreshed, err := codex.Refresh(ctx, tok)
		if err != nil {
			// If refresh fails, fall back to the stored token — the API
			// call will fail with a clear auth error rather than a opaque
			// refresh error. The user can re-login from the UI.
			return &codex.Adapter{
				BaseURL:     p.BaseURL,
				AccessToken: tok.AccessToken,
				AccountID:   tok.AccountID,
				Client:      client,
			}, nil
		}
		// Persist the refreshed token so subsequent turns don't refresh again.
		if newJSON, err := refreshed.Marshal(); err == nil {
			_ = creds.Set(p.ID, newJSON)
		}
		return &codex.Adapter{
			BaseURL:     p.BaseURL,
			AccessToken: refreshed.AccessToken,
			AccountID:   refreshed.AccountID,
			Client:      client,
		}, nil
	}
	return &codex.Adapter{
		BaseURL:     p.BaseURL,
		AccessToken: tok.AccessToken,
		AccountID:   tok.AccountID,
		Client:      client,
	}, nil
}

// newProviderHTTPClient bounds dial and response headers, but not the body
// read, so long SSE generations are not killed at 60s.
func newProviderHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}
