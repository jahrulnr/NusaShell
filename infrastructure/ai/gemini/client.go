// Package gemini implements the Google Gemini wire format
// (models/{model}:generateContent and :streamGenerateContent) for the shared
// core.Provider contract.
//
// It is ported from litellm's Gemini providers — litellm/llms/gemini
// (GoogleAIStudioGeminiConfig, common_utils.py) and
// litellm/llms/vertex_ai/gemini (VertexGeminiConfig, transformation.py) —
// keeping the upstream wire shapes:
//
//   - messages -> contents + systemInstruction, with parts for text, media
//     (inlineData / fileData), functionCall, functionResponse, and thought
//     parts carrying thoughtSignature
//   - tools -> functionDeclarations using the OpenAPI-subset schema Gemini
//     accepts (upper-case types, no additionalProperties, propertyOrdering)
//   - thinking -> generationConfig.thinkingConfig (thinkingBudget for Gemini
//     2.x and older, thinkingLevel for Gemini 3+) with includeThoughts
//   - response_format -> generationConfig.responseMimeType + responseSchema
//   - usageMetadata -> core.Usage (prompt / candidates / thoughts / cached)
//
// Like the other ported wire packages this package imports only
// infrastructure/ai/core; boundary translation lives in infrastructure/ai's
// Adapter and application/provider.
package gemini

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/retry"
)

const (
	defaultBaseURL   = "https://generativelanguage.googleapis.com"
	defaultUserAgent = "litellm-go/0.1"
	// apiVersion is the Google Generative Language API version used when the
	// configured base URL carries no version segment of its own.
	apiVersion = "v1beta"
	// filesAPIPrefix identifies Google Files API URIs. Parts pointing at an
	// already-uploaded file pass through as fileData instead of being inlined.
	filesAPIPrefix = defaultBaseURL + "/" + apiVersion + "/files/"
	// dummyThoughtSignature mirrors litellm's _get_dummy_thought_signature():
	// the base64 encoding of "skip_thought_signature_validator". Gemini 3
	// requires a thought signature on the first functionCall part of a batch;
	// Google documents this sentinel as the last-resort placeholder when no
	// real signature is available (e.g. history migrated from another model).
	dummyThoughtSignature = "c2tpcF90aG91Z2h0X3NpZ25hdHVyZV92YWxpZGF0b3I="
)

type Config struct {
	APIKey     string
	APIKeyFunc func(context.Context) (string, error)
	BaseURL    string
	HTTPClient HTTPClient
	Transport  http.RoundTripper
	Retry      *retry.Policy
	UserAgent  string
	Headers    map[string]string
	// RequestHeaders adds per-request transport headers derived from provider
	// options. The Gemini wire itself needs none, so this stays nil unless a
	// caller needs an extra transport header.
	RequestHeaders func(http.Header, core.ProviderOptions)
	// APIKeyOptional allows construction and request sending without an API
	// key. Used by Gemini-compatible gateways that authenticate out of band.
	APIKeyOptional bool
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Provider struct {
	cfg Config
}

func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" && cfg.APIKeyFunc == nil && !cfg.APIKeyOptional {
		return nil, fmt.Errorf("gemini: api key is required")
	}
	if cfg.HTTPClient != nil && cfg.Transport != nil {
		return nil, fmt.Errorf("gemini: HTTPClient and Transport are mutually exclusive")
	}
	if cfg.HTTPClient != nil && cfg.Retry != nil {
		return nil, fmt.Errorf("gemini: Retry cannot be used with a custom HTTPClient; use Transport or configure retry on the client")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.HTTPClient == nil {
		base := cfg.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		cfg.HTTPClient = &http.Client{Transport: retry.NewTransport(base, cfg.Retry)}
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	return &Provider{cfg: cfg}, nil
}

func Factory(cfg Config) (core.Provider, error) {
	return New(cfg)
}

func (p *Provider) Name() string {
	return "gemini"
}

// apiRoot returns the configured base URL with the API version segment
// appended when the caller supplied a bare origin or gateway root. A base URL
// that already carries a version (v1, v1beta, or any other segment) is used
// verbatim so gateways serving Gemini-compatible routes keep working.
func (p *Provider) apiRoot() string {
	base := strings.TrimRight(p.cfg.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}
	if strings.HasSuffix(base, "/"+apiVersion) || strings.Contains(base, "/"+apiVersion+"/") {
		return base
	}
	return base + "/" + apiVersion
}

// generateURL builds the operation URL for one model. Model IDs may arrive as
// "gemini-2.5-flash", "models/gemini-2.5-flash", or with a "gemini/" prefix
// (litellm's provider-scoped form); the prefixes are stripped because the
// operation path already carries the models collection.
func (p *Provider) generateURL(model string, stream bool) string {
	name := normalizeModelID(model)
	operation := ":generateContent"
	if stream {
		operation = ":streamGenerateContent?alt=sse"
	}
	return p.apiRoot() + "/models/" + name + operation
}

func normalizeModelID(model string) string {
	name := strings.TrimSpace(model)
	segments := strings.Split(name, "/")
	if len(segments) > 1 {
		// Keep only the final segment: both "models/<id>" and "<provider>/<id>"
		// forms resolve to the bare model id the API expects.
		name = segments[len(segments)-1]
	}
	return name
}

func (p *Provider) modelsURL(pageToken string) string {
	url := p.apiRoot() + "/models?pageSize=200"
	if pageToken != "" {
		url += "&pageToken=" + pageToken
	}
	return url
}

func (p *Provider) setHeaders(ctx context.Context, req *http.Request, options core.ProviderOptions) error {
	key := p.cfg.APIKey
	if p.cfg.APIKeyFunc != nil {
		resolved, err := p.cfg.APIKeyFunc(ctx)
		if err != nil {
			return fmt.Errorf("gemini: resolve api key: %w", err)
		}
		key = resolved
	}
	if key == "" && !p.cfg.APIKeyOptional {
		return fmt.Errorf("gemini: api key is required")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Google AI Studio authenticates with x-goog-api-key. The key is never
	// placed in the query string so it cannot leak through request logs.
	if key != "" {
		req.Header.Set("x-goog-api-key", key)
	}
	req.Header.Set("User-Agent", p.cfg.UserAgent)
	for name, value := range p.cfg.Headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			return fmt.Errorf("gemini: header name cannot be empty")
		}
		if value == "" {
			continue
		}
		req.Header.Set(name, value)
	}
	if p.cfg.RequestHeaders != nil {
		p.cfg.RequestHeaders(req.Header, options)
	}
	return nil
}
