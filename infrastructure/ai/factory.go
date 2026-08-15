package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/chatcompletion"
	"nusashell/infrastructure/ai/codex"
	"nusashell/infrastructure/ai/messages"
	"nusashell/infrastructure/ai/responses"
	"nusashell/infrastructure/config"
)

// codexInstallationID is a persistent UUID identifying this NusaShell
// install for Codex backend routing. It's stored in the user config dir
// so it survives restarts. The backend uses it together with session-id
// and thread-id to derive a stable prompt cache key.
var codexInstallationID = loadOrGenerateInstallationID()

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
			BaseURL:        p.BaseURL,
			InstallationID: codexInstallationID,
			Client:         client,
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
				BaseURL:        p.BaseURL,
				AccessToken:    tok.AccessToken,
				AccountID:      tok.AccountID,
				InstallationID: codexInstallationID,
				Client:         client,
			}, nil
		}
		// Persist the refreshed token so subsequent turns don't refresh again.
		if newJSON, err := refreshed.Marshal(); err == nil {
			_ = creds.Set(p.ID, newJSON)
		}
		return &codex.Adapter{
			BaseURL:        p.BaseURL,
			AccessToken:    refreshed.AccessToken,
			AccountID:      refreshed.AccountID,
			InstallationID: codexInstallationID,
			Client:         client,
		}, nil
	}
	return &codex.Adapter{
		BaseURL:        p.BaseURL,
		AccessToken:    tok.AccessToken,
		AccountID:      tok.AccountID,
		InstallationID: codexInstallationID,
		Client:         client,
	}, nil
}

// loadOrGenerateInstallationID loads a persistent installation UUID from
// <data-dir>/codex-installation-id, or generates a new one and persists it
// if none exists. This ID is sent as x-codex-installation-id so the Codex
// backend can route requests from the same install to the same cache shard.
func loadOrGenerateInstallationID() string {
	dir := config.DefaultDataDir()
	path := filepath.Join(dir, "codex-installation-id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}
	id := mustGenerateUUID()
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(path, []byte(id), 0o600)
	return id
}

// mustGenerateUUID generates a random UUID v4 string. Panics on read failure
// (should never happen with crypto/rand).
func mustGenerateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
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
