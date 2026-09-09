package ai

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/codex"
	"nusashell/infrastructure/ai/embeddings"
	"nusashell/infrastructure/ai/imagegen"
	ttsclient "nusashell/infrastructure/ai/tts"
	"nusashell/infrastructure/ai/videogen"
)

// codexInstallationID is a persistent UUID identifying this NusaShell
// install for Codex backend routing.
var codexInstallationID = codex.LoadOrGenerateInstallationID()

var codexRefreshMu = make(chan struct{}, 1)

// NewFactory returns a ProviderFactory closure that builds the single
// provider Adapter for a stored provider config. For chat-kind providers:
//   - Genuine OpenRouter hosts (openrouter.ai) use the OpenRouter adapter
//     (OpenRouter wire: reasoning object, reasoning_details, cache_retention,
//     provider routing, attribution headers).
//   - Every other chat-kind host (direct OpenAI, OpenAI-compatible
//     aggregators like TokenRouter/9Router/OpenCode, local endpoints) uses
//     the vanilla OpenAI Chat adapter, even when the stored driver is
//     openrouter (the custom-provider default). Aggregators implement the
//     OpenAI wire and reject OpenRouter-specific params — OpenCode Console
//     Go 400s without `reasoning_content`; TokenRouter 400s on the
//     OpenRouter `reasoning` object.
//   - Codex providers (ProviderCodex) use the Codex Responses transport.
//     Stored OAuth JSON is refreshed when expired; AccountID and
//     InstallationID headers are attached for ChatGPT multi-account routing.
func NewFactory(creds application.CredentialStore) application.ProviderFactory {
	return func(ctx context.Context, p *domain.Provider, apiKey string) (application.AIProvider, error) {
		if !domain.ValidKind(p.Kind) {
			return nil, &application.ErrUnsupportedProvider{Kind: string(p.Kind)}
		}
		client := newProviderHTTPClient()
		driver := p.EffectiveDriver()
		resolvedKey := apiKey
		accountID := ""
		installationID := ""
		if p.Kind == domain.ProviderCodex {
			tok, err := resolveCodexToken(ctx, p, apiKey, creds)
			if err != nil {
				return nil, err
			}
			resolvedKey = tok.AccessToken
			accountID = tok.AccountID
			installationID = codexInstallationID
			client = withCodexCookieJar(client)
		}
		return &Adapter{
			ProviderKind:   p.Kind,
			Driver:         driver,
			OpenRouter:     domain.UsesOpenRouterWire(p.Kind, driver, p.BaseURL),
			BaseURL:        p.BaseURL,
			APIKey:         resolvedKey,
			Client:         client,
			AccountID:      accountID,
			InstallationID: installationID,
		}, nil
	}
}

func resolveCodexToken(ctx context.Context, p *domain.Provider, storedJSON string, creds application.CredentialStore) (*codex.TokenJSON, error) {
	if storedJSON == "" {
		return &codex.TokenJSON{}, nil
	}
	tok, err := codex.UnmarshalToken(storedJSON)
	if err != nil {
		// Plain access-token paste (non-JSON) — use as-is without refresh.
		trimmed := strings.TrimSpace(storedJSON)
		if trimmed != "" && !strings.HasPrefix(trimmed, "{") {
			return &codex.TokenJSON{AccessToken: trimmed}, nil
		}
		kind := "codex"
		if p != nil {
			kind = string(p.Kind)
		}
		return nil, &application.ErrUnsupportedProvider{Kind: kind}
	}
	// Auto-refresh if the access token is expired or will expire within 5 min.
	// The 5-min margin avoids mid-stream token expiry on long generations.
	if tok.RefreshToken != "" && tok.IsExpired(5*time.Minute) {
		select {
		case codexRefreshMu <- struct{}{}:
			defer func() { <-codexRefreshMu }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if creds != nil && p != nil && p.ID != "" {
			if latestJSON, has, _ := creds.Get(p.ID); has && latestJSON != storedJSON {
				if latest, parseErr := codex.UnmarshalToken(latestJSON); parseErr == nil && latest.AccountID == tok.AccountID && latest.AccountID != "" && !latest.IsExpired(5*time.Minute) {
					return latest, nil
				}
			}
		}
		refreshed, err := codex.Refresh(ctx, tok)
		if err != nil {
			// If refresh fails, fall back to the stored token — the API
			// call will fail with a clear auth error rather than an opaque
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

// withCodexCookieJar returns a shallow copy of client with the shared
// Cloudflare cookie jar attached. The transport is reused so connection
// pooling is preserved. If the client already has a jar, it is left untouched.
func withCodexCookieJar(client *http.Client) *http.Client {
	if client == nil || client.Jar != nil {
		return client
	}
	return &http.Client{
		Transport:     client.Transport,
		CheckRedirect: client.CheckRedirect,
		Jar:           codex.SharedCloudflareCookieJar(),
		Timeout:       client.Timeout,
	}
}

// NewImageGeneratorFactory returns an ImageGeneratorFactory for the
// OpenAI/OpenRouter images API plus the Codex ChatGPT plan image backend.
// The Codex path resolves (and refreshes) the stored OAuth token, attaches
// the shared Cloudflare cookie jar, and targets the provider base URL
// (default https://chatgpt.com/backend-api/codex). OpenAI/OpenRouter hosts
// are served by imagegen.NewFactory.
func NewImageGeneratorFactory(creds application.CredentialStore) application.ImageGeneratorFactory {
	openai := imagegen.NewFactory()
	return func(ctx context.Context, p *domain.Provider, apiKey string) (application.ImageGenerator, error) {
		if p != nil && p.Kind == domain.ProviderCodex {
			tok, err := resolveCodexToken(ctx, p, apiKey, creds)
			if err != nil {
				return nil, err
			}
			base := strings.TrimRight(p.BaseURL, "/")
			if base == "" {
				base = codex.DefaultBaseURL
			}
			return &imagegen.Client{
				Backend:   imagegen.BackendCodex,
				BaseURL:   base,
				APIKey:    tok.AccessToken,
				AccountID: tok.AccountID,
				HTTP:      withCodexCookieJar(newProviderHTTPClient()),
			}, nil
		}
		return openai(ctx, p, apiKey)
	}
}

// NewEmbedderFactory returns an EmbedderFactory that builds an Embedder for
// OpenAI-compatible providers (chat, responses, messages kinds all expose
// embeddings on a single endpoint regardless of the chat wire format).
// Returns nil, nil for provider kinds that cannot produce embeddings —
// the caller falls back to BM25-only search in that case.
//
// The factory needs the provider's model list to find an embedding-capable
// model. If the provider has no embedding model in its Models slice, the
// factory returns nil, nil.
func NewEmbedderFactory() application.EmbedderFactory {
	return func(p *domain.Provider, apiKey string) (application.Embedder, error) {
		if !p.KindCapabilities().HasEmbeddings {
			return nil, nil
		}
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
		// Respect the model's documented input context when the catalog
		// knows it (e.g. text-embedding-3-small = 8191); 0 falls back to
		// the Embedder's conservative default for small/self-hosted models.
		maxTokens := 0
		if m := p.FindModel(model); m != nil && m.Context > 0 {
			maxTokens = m.Context
		}
		base := embeddingBaseURL(p.BaseURL)
		return embeddings.NewEmbedder(base, apiKey, model, maxTokens), nil
	}
}

// NewEmbeddingModelListerFactory returns a factory that builds an
// EmbeddingModelLister for any provider kind that exposes an OpenAI-compatible
// /embeddings/models endpoint. AI gateways support multiple chat APIs while
// exposing embeddings on a single endpoint, so the same lister works
// regardless of which chat API the provider is configured to use.
func NewEmbeddingModelListerFactory() application.EmbeddingModelListerFactory {
	client := newProviderHTTPClient()
	return func(p *domain.Provider) application.EmbeddingModelLister {
		base := embeddingBaseURL(p.BaseURL)
		return embeddings.NewModelLister(base, client)
	}
}

// NewImageModelListerFactory returns a factory that builds an ImageModelLister
// for OpenAI-compatible hosts. Anthropic Messages has no image catalog.
func NewImageModelListerFactory() application.ImageModelListerFactory {
	client := newProviderHTTPClient()
	return func(p *domain.Provider) application.ImageModelLister {
		if p == nil {
			return nil
		}
		if !p.KindCapabilities().HasImageEndpoint {
			return nil
		}
		return imagegen.NewModelLister(embeddingBaseURL(p.BaseURL), client)
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
		if !p.KindCapabilities().HasSpeechEndpoint {
			return nil
		}
		return ttsclient.NewModelLister(embeddingBaseURL(p.BaseURL), client)
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
		if !p.KindCapabilities().HasVideoEndpoint {
			return nil, fmt.Errorf("videogen: provider kind %q has no /videos endpoint", p.Kind)
		}
		base := strings.TrimRight(p.BaseURL, "/")
		if base == "" {
			return nil, fmt.Errorf("videogen: provider %q has no base URL", p.ID)
		}
		return &videogen.Client{BaseURL: base, APIKey: apiKey, HTTP: client}, nil
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
		if !p.KindCapabilities().HasVideoEndpoint {
			return nil
		}
		return videogen.NewModelLister(embeddingBaseURL(p.BaseURL), client)
	}
}

// newProviderHTTPClient bounds dial and response headers, but not the body
// read, so long SSE generations are not killed at 300s.
func newProviderHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   300 * time.Second,
				KeepAlive: 300 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   300 * time.Second,
			ResponseHeaderTimeout: 300 * time.Second,
			IdleConnTimeout:       300 * time.Second,
		},
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
