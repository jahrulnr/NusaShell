// Package ai is the composition root for AI provider adapters. It wires
// the ported litellm provider subpackages (anthropic, openai, openrouter)
// into core.Provider via a single Adapter that switches on the provider
// kind.
//
// The litellm providers speak the shared core.Request/Response model
// (Blocks-based). Boundary translation (application.ChatRequest ←>
// core.Request/Response) and error mapping live in application/ai_convert.go.
package ai

import (
	"context"
	"net/http"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/anthropic"
	"nusashell/infrastructure/ai/core"
	aiutil "nusashell/infrastructure/ai/internal"
	"nusashell/infrastructure/ai/openai"
	"nusashell/infrastructure/ai/openrouter"
)

// Adapter implements core.Provider for every supported provider kind. The
// provider-specific adapter is selected per call by Driver and Kind.
// Providers without an explicit Driver retain host-detected routing.
//
// Conversion between application.ChatRequest/ChatResponse and
// core.Request/Response is handled in application/ai_convert.go.
// Error mapping is handled by application.MapCoreError.
type Adapter struct {
	ProviderKind domain.ProviderKind
	Driver       domain.ProviderDriver
	OpenRouter   bool
	BaseURL      string
	APIKey       string
	Client       *http.Client
}

// Name returns the provider kind string for diagnostics.
func (a *Adapter) Name() string { return string(a.ProviderKind) }

// providerFor builds the litellm provider for this adapter's kind.
func (a *Adapter) providerFor() (core.Provider, error) {
	switch a.Driver {
	case domain.ProviderDriverAnthropic:
		if a.ProviderKind != domain.ProviderMessages {
			return nil, &application.ErrUnsupportedProvider{Kind: string(a.ProviderKind)}
		}
		return anthropic.New(anthropic.Config{APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client})
	case domain.ProviderDriverOpenAI:
		if a.ProviderKind != domain.ProviderResponses {
			return nil, &application.ErrUnsupportedProvider{Kind: string(a.ProviderKind)}
		}
		return openai.New(openai.Config{API: openai.APIResponses, APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client})
	case domain.ProviderDriverOpenRouter:
		return openrouter.NewForAPI(openrouter.Config{
			APIKey:         a.APIKey,
			BaseURL:        a.BaseURL,
			HTTPClient:     a.Client,
			APIKeyOptional: a.ProviderKind == domain.ProviderChat && a.APIKey == "",
		}, string(a.ProviderKind))
	}
	switch {
	case a.ProviderKind == domain.ProviderMessages:
		return anthropic.New(anthropic.Config{APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client})
	case a.ProviderKind == domain.ProviderResponses:
		return openai.New(openai.Config{API: openai.APIResponses, APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client})
	case a.ProviderKind == domain.ProviderChat && a.OpenRouter:
		return openrouter.New(openrouter.Config{APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: a.APIKey == ""})
	case a.ProviderKind == domain.ProviderChat:
		return openai.New(openai.Config{API: openai.APIChat, APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: a.APIKey == ""})
	default:
		return nil, &application.ErrUnsupportedProvider{Kind: string(a.ProviderKind)}
	}
}

// Chat implements core.Provider.
func (a *Adapter) Chat(ctx context.Context, req *core.Request) (*core.Response, error) {
	provider, err := a.providerFor()
	if err != nil {
		return nil, err
	}
	return provider.Chat(ctx, req)
}

// Stream implements core.Provider.
func (a *Adapter) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	provider, err := a.providerFor()
	if err != nil {
		return nil, err
	}
	return provider.Stream(ctx, req)
}

// ListModels implements application.ModelLister.
func (a *Adapter) ListModels(ctx context.Context, apiKey string) ([]domain.Model, error) {
	switch {
	case a.ProviderKind == domain.ProviderMessages && a.Driver != domain.ProviderDriverOpenRouter:
		return listAnthropicModels(ctx, a.BaseURL, a.APIKey, a.Client)
	default:
		headers := map[string]string{}
		if apiKey != "" {
			headers["Authorization"] = "Bearer " + apiKey
		}
		if a.OpenRouter || a.Driver == domain.ProviderDriverOpenRouter {
			for k, v := range aiutil.OpenRouterAttributionHeaders() {
				headers[k] = v
			}
		}
		return listOpenAIModels(ctx, a.BaseURL, headers, a.Client)
	}
}
