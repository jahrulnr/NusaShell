package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/retry"
)

const defaultUserAgent = "litellm-go/0.1"

type Provider struct {
	cfg  Config
	spec Spec
}

func New(cfg Config, spec Spec) (*Provider, error) {
	if spec.Name == "" {
		return nil, fmt.Errorf("compat: provider name is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = spec.Endpoint.BaseURL
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%s: base url is required", spec.providerName())
	}
	if spec.apiKeyRequired() && !cfg.APIKeyOptional && cfg.APIKey == "" && cfg.APIKeyFunc == nil {
		return nil, fmt.Errorf("%s: api key is required", spec.providerName())
	}
	if cfg.HTTPClient != nil && cfg.Transport != nil {
		return nil, fmt.Errorf("%s: HTTPClient and Transport are mutually exclusive", spec.providerName())
	}
	if cfg.HTTPClient != nil && cfg.Retry != nil {
		return nil, fmt.Errorf("%s: Retry cannot be used with a custom HTTPClient; use Transport or configure retry on the client", spec.providerName())
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
	return &Provider{cfg: cfg, spec: spec}, nil
}

func (p *Provider) Name() string {
	return p.spec.providerName()
}

func (p *Provider) Capabilities(model string) core.Capabilities {
	caps := p.spec.defaultCapabilities(p.Name(), model)
	if p.spec.Capabilities != nil {
		caps = p.spec.Capabilities(model, caps)
		if caps.Provider == "" {
			caps.Provider = p.Name()
		}
		if caps.Model == "" {
			caps.Model = model
		}
	}
	return caps
}

func (p *Provider) Chat(ctx context.Context, req *core.Request) (*core.Response, error) {
	body, warnings, err := p.buildRequest(req, false)
	if err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	resp, err := p.do(ctx, req, body, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, core.NewNetworkError(p.Name(), "read response failed", err)
	}
	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, core.NewProviderErrorWithCause(p.Name(), core.ErrorTypeProvider, fmt.Sprintf("%s: decode response", p.Name()), err)
	}
	out, err := p.convertResponse(&parsed, req)
	if err != nil {
		return nil, core.WrapError(err, p.Name())
	}
	stampWarnings(warnings, p.Name())
	out.Warnings = append(warnings, out.Warnings...)
	core.CaptureRawResponse(req, out, data)
	return out, nil
}

func (p *Provider) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	body, warnings, err := p.buildRequest(req, true)
	if err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	resp, err := p.do(ctx, req, body, true)
	if err != nil {
		return nil, err
	}
	stampWarnings(warnings, p.Name())
	return prependWarnings(newStream(resp, req, p.spec), warnings), nil
}

func (p *Provider) ListModels(ctx context.Context) ([]core.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url(p.spec.modelsPath()), nil)
	if err != nil {
		return nil, fmt.Errorf("%s: create models request: %w", p.Name(), err)
	}
	if err := p.setHeaders(ctx, httpReq, nil, false); err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, core.NewNetworkError(p.Name(), "models request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, core.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	var payload modelList
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, core.NewProviderErrorWithCause(p.Name(), core.ErrorTypeProvider, fmt.Sprintf("%s: decode models response", p.Name()), err)
	}
	models := make([]core.ModelInfo, 0, len(payload.Data))
	for _, item := range payload.Data {
		name := item.Name
		if name == "" {
			name = item.ID
		}
		models = append(models, core.ModelInfo{
			ID:            item.ID,
			Name:          name,
			Provider:      p.Name(),
			Description:   item.Description,
			Created:       item.Created,
			ContextLength: item.ContextLength,
		})
	}
	return models, nil
}

func (p *Provider) do(ctx context.Context, req *core.Request, body []byte, stream bool) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url(p.spec.chatPath()), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: create request: %w", p.Name(), err)
	}
	if err := p.setHeaders(ctx, httpReq, req, stream); err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, core.NewNetworkError(p.Name(), "request failed", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, core.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	return resp, nil
}

func (p *Provider) setHeaders(ctx context.Context, httpReq *http.Request, req *core.Request, stream bool) error {
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", p.cfg.UserAgent)
	key, err := p.apiKey(ctx)
	if err != nil {
		return err
	}
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range p.spec.Headers.Extra {
		httpReq.Header.Set(k, v)
	}
	for k, v := range p.cfg.Headers {
		httpReq.Header.Set(k, v)
	}
	if p.spec.Headers.Request != nil && req != nil {
		p.spec.Headers.Request(httpReq.Header, req)
	}
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
		for k, v := range p.spec.Headers.Stream {
			httpReq.Header.Set(k, v)
		}
	}
	return nil
}

func (p *Provider) apiKey(ctx context.Context) (string, error) {
	key := p.cfg.APIKey
	if p.cfg.APIKeyFunc != nil {
		resolved, err := p.cfg.APIKeyFunc(ctx)
		if err != nil {
			return "", fmt.Errorf("%s: resolve api key: %w", p.Name(), err)
		}
		key = resolved
	}
	if p.spec.apiKeyRequired() && !p.cfg.APIKeyOptional && key == "" {
		return "", fmt.Errorf("%s: api key is required", p.Name())
	}
	return key, nil
}

func stampWarnings(warnings []core.Warning, provider string) {
	for i := range warnings {
		if warnings[i].Provider == "" {
			warnings[i].Provider = provider
		}
	}
}

func prependWarnings(stream core.Stream, warnings []core.Warning) core.Stream {
	if len(warnings) == 0 {
		return stream
	}
	return &warningStream{warnings: append([]core.Warning(nil), warnings...), inner: stream}
}

type warningStream struct {
	warnings []core.Warning
	index    int
	inner    core.Stream
}

func (s *warningStream) Next() (core.Event, error) {
	if s.index < len(s.warnings) {
		warning := s.warnings[s.index]
		s.index++
		return core.WarningEvent{Warning: warning}, nil
	}
	return s.inner.Next()
}

func (s *warningStream) Close() error {
	return s.inner.Close()
}

func (p *Provider) url(path string) string {
	return strings.TrimRight(p.cfg.BaseURL, "/") + path
}
