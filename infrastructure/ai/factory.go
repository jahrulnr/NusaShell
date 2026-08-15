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
	"nusashell/infrastructure/ai/embeddings"
	"nusashell/infrastructure/ai/messages"
	"nusashell/infrastructure/ai/ollama"
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
		case domain.ProviderOllama:
			// Ollama exposes an OpenAI-compatible /v1/chat/completions endpoint.
			// Reuse the chatcompletion adapter with the /v1 suffix appended.
			base := strings.TrimRight(p.BaseURL, "/")
			if !strings.HasSuffix(base, "/v1") {
				base += "/v1"
			}
			return &chatcompletion.Adapter{BaseURL: base, APIKey: apiKey, Client: client}, nil
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
		// Write both the active provider key and the account-scoped key used
		// by multi-account routing; otherwise failover keeps serving the
		// expired token.
		if newJSON, err := refreshed.Marshal(); err == nil {
			_ = application.PersistCodexToken(creds, p.ID, refreshed.AccountID, newJSON)
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

// NewEmbedderFactory returns an EmbedderFactory that builds an Embedder for
// providers that support embeddings. Returns nil, nil for provider kinds
// that cannot produce embeddings (Anthropic Messages, Codex OAuth) — the
// caller falls back to BM25-only search in that case.
//
// The factory needs the provider's model list to find an embedding-capable
// model. If the provider has no embedding model in its Models slice, the
// factory returns nil, nil.
func NewEmbedderFactory() application.EmbedderFactory {
	return func(p *domain.Provider, apiKey string) (application.Embedder, error) {
		switch p.Kind {
		case domain.ProviderOllama:
			// Ollama: find an embedding model in the provider's model list,
			// fall back to nomic-embed-text if none tagged. API key is
			// optional — only needed when Ollama is behind an auth proxy.
			model := ""
			for _, m := range p.Models {
				if m.Kind == domain.ModelKindEmbedding {
					model = m.ID
					break
				}
			}
			return ollama.New(p.BaseURL, model, apiKey), nil
		case domain.ProviderChat, domain.ProviderResponses, domain.ProviderMessages:
			// OpenAI-compatible: find an embedding model in the provider's
			// model list. If none tagged, return nil — can't guess the model.
			// Works for any gateway kind (chat, responses, messages) since
			// AI gateways expose embeddings on a single OpenAI-compatible
			// endpoint regardless of which chat API is configured.
			model := ""
			for _, m := range p.Models {
				if m.Kind == domain.ModelKindEmbedding {
					model = m.ID
					break
				}
			}
			if model == "" {
				return nil, nil
			}
			base := embeddingBaseURL(p.BaseURL)
			return chatcompletion.NewEmbedder(base, apiKey, model), nil
		default:
			// Codex OAuth does not support embeddings.
			return nil, nil
		}
	}
}

// NewEmbeddingModelListerFactory returns a factory that builds an
// EmbeddingModelLister for any provider kind that exposes an OpenAI-compatible
// /embeddings/models endpoint. Returns nil for Codex (no such endpoint).
//
// The lister is provider-kind agnostic — AI gateways (OpenRouter, TokenRouter,
// OmniRoute) support multiple chat APIs (messages, responses, chat) while
// exposing embeddings on a single endpoint, so the same lister works
// regardless of which chat API the provider is configured to use.
func NewEmbeddingModelListerFactory() application.EmbeddingModelListerFactory {
	return func(p *domain.Provider) application.EmbeddingModelLister {
		if p.Kind == domain.ProviderCodex {
			return nil
		}
		base := embeddingBaseURL(p.BaseURL)
		return embeddings.NewModelLister(base, newProviderHTTPClient())
	}
}

// embeddingBaseURL normalizes a provider BaseURL to the OpenAI-compatible
// API root (ending with /v1) for embedding endpoints. If the BaseURL already
// ends with /v1, it is used as-is; otherwise /v1 is appended.
func embeddingBaseURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base
}
