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
	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/embeddings"
	"nusashell/infrastructure/ai/imagegen"
	ttsclient "nusashell/infrastructure/ai/tts"
	"nusashell/infrastructure/ai/videogen"
)

// NewFactory returns a ProviderFactory closure that builds the single
// provider Adapter for a stored provider config. For chat-kind providers:
//   - Genuine OpenRouter hosts (openrouter.ai) or an explicit
//     ProviderDriverOpenRouter use the OpenRouter adapter (OpenRouter wire
//     format: reasoning object, reasoning_details, cache_retention,
//     provider routing, attribution headers).
//   - Every other chat-kind host (direct OpenAI, OpenAI-compatible
//     aggregators like TokenRouter/9Router/OpenCode, local endpoints) uses
//     the vanilla OpenAI Chat adapter. Aggregators implement the OpenAI
//     wire and reject OpenRouter-specific params — e.g. TokenRouter returns
//     HTTP 400 "Unknown parameter: 'reasoning'" for the OpenRouter
//     reasoning object — so they must NOT receive the OpenRouter format.
func NewFactory(_ application.CredentialStore) application.ProviderFactory {
	return func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error) {
		if !domain.ValidKind(p.Kind) {
			return nil, &application.ErrUnsupportedProvider{Kind: string(p.Kind)}
		}
		client := newProviderHTTPClient()
		driver := p.EffectiveDriver()
		return &Adapter{
			ProviderKind: p.Kind,
			Driver:       driver,
			OpenRouter:   driver == domain.ProviderDriverOpenRouter || domain.IsOpenRouterHost(p.Kind, p.BaseURL),
			BaseURL:      p.BaseURL,
			APIKey:       apiKey,
			Client:       client,
		}, nil
	}
}

// NewImageGeneratorFactory returns an ImageGeneratorFactory for the
// OpenAI-compatible images API. The Codex and Gemini image backends were
// removed with their providers; only OpenAI/OpenRouter hosts remain.
func NewImageGeneratorFactory(creds application.CredentialStore) application.ImageGeneratorFactory {
	openai := imagegen.NewFactory()
	return func(p *domain.Provider, apiKey string) (application.ImageGenerator, error) {
		return openai(p, apiKey)
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
