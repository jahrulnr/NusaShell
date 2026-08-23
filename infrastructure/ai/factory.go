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
	"nusashell/infrastructure/ai/imagegen"
	"nusashell/infrastructure/ai/messages"
	"nusashell/infrastructure/ai/ollama"
	"nusashell/infrastructure/ai/responses"
	ttsclient "nusashell/infrastructure/ai/tts"
	"nusashell/infrastructure/ai/videogen"
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

func resolveCodexToken(ctx context.Context, p *domain.Provider, storedJSON string, creds application.CredentialStore) (*codex.TokenJSON, error) {
	if storedJSON == "" {
		return &codex.TokenJSON{}, nil
	}
	tok, err := codex.UnmarshalToken(storedJSON)
	if err != nil {
		kind := "codex"
		if p != nil {
			kind = string(p.Kind)
		}
		return nil, &application.ErrUnsupportedProvider{Kind: kind}
	}
	// Auto-refresh if the access token is expired or will expire within 5 min.
	// The 5-min margin avoids mid-stream token expiry on long generations.
	if tok.RefreshToken != "" && tok.IsExpired(5*time.Minute) {
		refreshed, err := codex.Refresh(ctx, tok)
		if err != nil {
			// If refresh fails, fall back to the stored token — the API
			// call will fail with a clear auth error rather than a opaque
			// refresh error. The user can re-login from the UI.
			return tok, nil
		}
		// Persist the refreshed token so subsequent turns don't refresh again.
		// Write both the active provider key and the account-scoped key used
		// by multi-account routing; otherwise failover keeps serving the
		// expired token.
		if creds != nil {
			if newJSON, err := refreshed.Marshal(); err == nil {
				providerID := ""
				if p != nil {
					providerID = p.ID
				}
				_ = application.PersistCodexToken(creds, providerID, refreshed.AccountID, newJSON)
			}
		}
		return refreshed, nil
	}
	return tok, nil
}

// newCodexAdapter parses the stored OAuth token, refreshes it if expired,
// persists the refreshed token, and builds the Codex adapter.
func newCodexAdapter(ctx context.Context, p *domain.Provider, storedJSON string, client *http.Client, creds application.CredentialStore) (application.AIProvider, error) {
	tok, err := resolveCodexToken(ctx, p, storedJSON, creds)
	if err != nil {
		return nil, err
	}
	return &codex.Adapter{
		BaseURL:        p.BaseURL,
		AccessToken:    tok.AccessToken,
		AccountID:      tok.AccountID,
		InstallationID: codexInstallationID,
		Client:         client,
	}, nil
}

// NewImageGeneratorFactory routes OpenAI/OpenRouter hosts through the Images
// API and Codex OAuth through chatgpt.com/backend-api/codex/images/*. Token
// refresh uses the same CredentialStore path as chat Codex.
func NewImageGeneratorFactory(creds application.CredentialStore) application.ImageGeneratorFactory {
	openai := imagegen.NewFactory()
	return func(p *domain.Provider, apiKey string) (application.ImageGenerator, error) {
		if p != nil && p.Kind == domain.ProviderCodex {
			tok, err := resolveCodexToken(context.Background(), p, apiKey, creds)
			if err != nil {
				return nil, err
			}
			return &codex.ImagesClient{
				BaseURL:        p.BaseURL,
				AccessToken:    tok.AccessToken,
				AccountID:      tok.AccountID,
				InstallationID: codexInstallationID,
			}, nil
		}
		return openai(p, apiKey)
	}
}

// loadOrGenerateInstallationID loads a persistent installation UUID from
// <data-dir>/config/codex-installation-id, or generates a new one and
// persists it if none exists. This ID is sent as x-codex-installation-id
// so the Codex backend can route requests from the same install to the
// same cache shard. Respects NUSASHELL_DATA_DIR so the ID lives alongside
// the rest of the user's data, not in a hardcoded default location.
func loadOrGenerateInstallationID() string {
	dir := os.Getenv("NUSASHELL_DATA_DIR")
	if dir == "" {
		dir = config.DefaultDataDir()
	}
	path := filepath.Join(dir, "config", "codex-installation-id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}
	id := mustGenerateUUID()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
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

// NewImageModelListerFactory returns a factory that builds an ImageModelLister
// for OpenAI-compatible hosts. Messages (Anthropic) has no image catalog.
// Codex has no /images/models endpoint; gpt-image-2 is seeded at import/read
// time instead of probing a 404.
func NewImageModelListerFactory() application.ImageModelListerFactory {
	client := newProviderHTTPClient()
	return func(p *domain.Provider) application.ImageModelLister {
		if p == nil {
			return nil
		}
		switch p.Kind {
		case domain.ProviderChat, domain.ProviderResponses, domain.ProviderOllama:
			return imagegen.NewModelLister(embeddingBaseURL(p.BaseURL), client)
		default:
			return nil
		}
	}
}

// NewSpeechModelListerFactory returns a factory that builds a
// SpeechModelLister for OpenAI-compatible hosts. The lister hits
// GET <base>/models?output_modalities=speech (OpenRouter surfaces its TTS
// catalog only through that filter); hosts that reject it yield an empty
// list and the importer falls back to catalog tagging + allowlist.
func NewSpeechModelListerFactory() application.SpeechModelListerFactory {
	client := newProviderHTTPClient()
	return func(p *domain.Provider) application.SpeechModelLister {
		if p == nil {
			return nil
		}
		switch p.Kind {
		case domain.ProviderChat, domain.ProviderResponses, domain.ProviderOllama:
			return ttsclient.NewModelLister(embeddingBaseURL(p.BaseURL), client)
		default:
			return nil
		}
	}
}

// NewVideoGeneratorFactory builds online video-generation clients for
// OpenAI-compatible hosts serving the async /videos API (OpenRouter).
// Other kinds fail fast so callers surface a clear unavailability message.
func NewVideoGeneratorFactory() application.VideoGeneratorFactory {
	client := newProviderHTTPClient()
	return func(p *domain.Provider, apiKey string) (application.VideoGenerator, error) {
		if p == nil {
			return nil, fmt.Errorf("videogen: nil provider")
		}
		switch p.Kind {
		case domain.ProviderChat, domain.ProviderResponses, domain.ProviderOllama:
			base := strings.TrimRight(p.BaseURL, "/")
			if base == "" {
				return nil, fmt.Errorf("videogen: provider %q has no base URL", p.ID)
			}
			return &videogen.Client{BaseURL: base, APIKey: apiKey, HTTP: client}, nil
		default:
			return nil, fmt.Errorf("videogen: provider kind %q has no /videos endpoint", p.Kind)
		}
	}
}

// NewVideoModelListerFactory returns a factory that builds a
// VideoModelLister via GET <base>/videos/models.
func NewVideoModelListerFactory() application.VideoModelListerFactory {
	client := newProviderHTTPClient()
	return func(p *domain.Provider) application.VideoModelLister {
		if p == nil {
			return nil
		}
		switch p.Kind {
		case domain.ProviderChat, domain.ProviderResponses, domain.ProviderOllama:
			return videogen.NewModelLister(embeddingBaseURL(p.BaseURL), client)
		default:
			return nil
		}
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
