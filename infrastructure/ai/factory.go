package ai

import (
	"context"
	"net/http"

	"nusashell/application"
	"nusashell/domain"
)

// Factory builds the provider adapter for a stored provider config. It
// satisfies application.ProviderFactory.
func Factory(_ context.Context, p *domain.Provider, apiKey string) (application.AIProvider, error) {
	// No wall-clock Timeout on the client: it would kill long SSE streams
	// that are actively sending data (a 90s generation > 60s timeout).
	// Stalled streams are detected by the per-chunk idle timeout in readSSE
	// (defaultIdleTimeout), and the caller's context deadline still bounds
	// non-streaming requests.
	client := &http.Client{}
	switch p.Kind {
	case domain.ProviderMessages:
		return &AnthropicAdapter{BaseURL: p.BaseURL, APIKey: apiKey, Client: client}, nil
	case domain.ProviderResponses:
		return &ResponsesAdapter{BaseURL: p.BaseURL, APIKey: apiKey, Client: client}, nil
	case domain.ProviderChat:
		return &OpenAIAdapter{BaseURL: p.BaseURL, APIKey: apiKey, Client: client}, nil
	default:
		return nil, &application.ErrUnsupportedProvider{Kind: string(p.Kind)}
	}
}
