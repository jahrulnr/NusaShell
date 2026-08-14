package ai

import (
	"context"
	"net"
	"net/http"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

// Factory builds the provider adapter for a stored provider config. It
// satisfies application.ProviderFactory.
func Factory(_ context.Context, p *domain.Provider, apiKey string) (application.AIProvider, error) {
	client := newProviderHTTPClient()
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
