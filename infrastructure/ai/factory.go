package ai

import (
	"context"
	"net/http"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

// Factory builds the provider adapter for a stored provider config. It
// satisfies application.ProviderFactory.
func Factory(_ context.Context, p *domain.Provider, apiKey string) (application.AIProvider, error) {
	client := &http.Client{Timeout: 60 * time.Second}
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
