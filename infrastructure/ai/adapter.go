// Package ai is the composition root for AI provider adapters. It wires
// the ported litellm provider subpackages (anthropic, openai, openrouter)
// into core.Provider via a single Adapter that switches on the provider
// kind.
//
// The litellm providers speak the shared core.Request/Response model
// (Blocks-based). Boundary translation (application.ChatRequest ←>
// core.Request/Response) and error mapping live in application/provider.
package ai

import (
	"context"
	"net/http"
	"strings"

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
// core.Request/Response is handled in application/provider.
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

const openCodeSessionHeader = "x-opencode-session"

// openCodeSessionHeaders copies the conversation prompt-cache key onto
// OpenCode's documented cache-affinity header. Official Go docs require
// x-opencode-session so Console Go can optimize prompt caching; they do
// not document OpenAI prompt_cache_options.ttl=30m (HTTP 422: Input
// should be '5m' or '1h').
func openCodeSessionHeaders(h http.Header, opts core.ProviderOptions) {
	if h == nil || opts == nil {
		return
	}
	key, _ := opts["prompt_cache_key"].(string)
	if strings.TrimSpace(key) == "" {
		key, _ = opts["session_id"].(string)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	h.Set(openCodeSessionHeader, key)
}

func (a *Adapter) requestHeaders() func(http.Header, core.ProviderOptions) {
	if domain.IsOpenCodeHost(a.BaseURL) {
		return openCodeSessionHeaders
	}
	return nil
}

// providerFor builds the litellm provider for this adapter's kind.
//
// Codex wiring belongs at this explicit selection seam when its runtime
// provider is introduced. The future adapter must implement core.Provider,
// preserve the Responses-compatible request/stream contract, and pass the
// opaque compaction_items and context_management values through
// core.Request.ProviderOptions without decoding or silently falling back.
// Its transport/auth boundary is the ChatGPT Codex backend
// (https://chatgpt.com/backend-api/codex), not this wire-only package.
func (a *Adapter) providerFor() (core.Provider, error) {
	optional := strings.TrimSpace(a.APIKey) == ""
	headers := a.requestHeaders()
	switch a.Driver {
	case domain.ProviderDriverAnthropic:
		if a.ProviderKind != domain.ProviderMessages {
			return nil, &application.ErrUnsupportedProvider{Kind: string(a.ProviderKind)}
		}
		return anthropic.New(anthropic.Config{APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: optional, RequestHeaders: headers})
	case domain.ProviderDriverOpenAI:
		if a.ProviderKind != domain.ProviderResponses {
			return nil, &application.ErrUnsupportedProvider{Kind: string(a.ProviderKind)}
		}
		return openai.New(openai.Config{API: openai.APIResponses, APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: optional, RequestHeaders: headers})
	case domain.ProviderDriverOpenRouter:
		if a.ProviderKind == domain.ProviderChat && !a.OpenRouter {
			return openai.New(openai.Config{API: openai.APIChat, APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: optional, RequestHeaders: headers})
		}
		return openrouter.NewForAPI(openrouter.Config{
			APIKey:         a.APIKey,
			BaseURL:        a.BaseURL,
			HTTPClient:     a.Client,
			APIKeyOptional: optional,
		}, string(a.ProviderKind))
	}
	switch {
	case a.ProviderKind == domain.ProviderMessages:
		return anthropic.New(anthropic.Config{APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: optional, RequestHeaders: headers})
	case a.ProviderKind == domain.ProviderResponses:
		return openai.New(openai.Config{API: openai.APIResponses, APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: optional, RequestHeaders: headers})
	case a.ProviderKind == domain.ProviderChat && a.OpenRouter:
		return openrouter.New(openrouter.Config{APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: optional})
	case a.ProviderKind == domain.ProviderChat:
		return openai.New(openai.Config{API: openai.APIChat, APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: optional, RequestHeaders: headers})
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
		if a.OpenRouter {
			for k, v := range aiutil.OpenRouterAttributionHeaders() {
				headers[k] = v
			}
		}
		return listOpenAIModels(ctx, a.BaseURL, headers, a.Client)
	}
}

// ListModelEndpoints implements application.ModelEndpointsLister. Only
// OpenRouter gateways have the concept of upstream providers; direct
// providers (Anthropic, OpenAI, local chat) return an empty list so
// callers never branch on gateway type. slug is the canonical identity
// plus any request variant (:free, :batch).
func (a *Adapter) ListModelEndpoints(ctx context.Context, slug string) ([]domain.ModelRoute, error) {
	if !a.OpenRouter {
		return nil, nil
	}
	headers := map[string]string{}
	if a.APIKey != "" {
		headers["Authorization"] = "Bearer " + a.APIKey
	}
	for k, v := range aiutil.OpenRouterAttributionHeaders() {
		headers[k] = v
	}
	base := strings.TrimRight(a.BaseURL, "/")
	if base == "" {
		base = openRouterDefaultBaseURL
	}
	return listOpenRouterEndpoints(ctx, base, headers, a.Client, slug)
}
