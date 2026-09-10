package ai

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"

	"nusashell/domain"
	"nusashell/infrastructure/ai/gemini"
	aiutil "nusashell/infrastructure/ai/internal"
)

// openRouterDefaultBaseURL is the fallback host for OpenRouter gateways
// when a provider record carries no explicit BaseURL.
const openRouterDefaultBaseURL = "https://openrouter.ai/api/v1"

// listOpenAIModels fetches the OpenAI-compatible /models catalog
// (id, context window, pricing, reasoning efforts) used by chat,
// responses, and OpenRouter providers.
func listOpenAIModels(ctx context.Context, baseURL string, headers map[string]string, client *http.Client) ([]domain.Model, error) {
	base := strings.TrimRight(baseURL, "/")
	url := base + "/models"
	var out struct {
		Data []struct {
			ID            string `json:"id"`
			CanonicalSlug string `json:"canonical_slug"`
			ContextLength int    `json:"context_length"`
			MaxTokens     int    `json:"max_tokens"`
			Description   string `json:"description"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
			Reasoning struct {
				SupportedEfforts []string `json:"supported_efforts"`
				DefaultEffort    string   `json:"default_effort"`
			} `json:"reasoning"`
		} `json:"data"`
	}
	if err := aiutil.DoJSON(ctx, client, http.MethodGet, url, headers, nil, &out); err != nil {
		return nil, err
	}
	models := make([]domain.Model, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		canonical := m.CanonicalSlug
		if canonical == "" {
			canonical = m.ID
		}
		model := domain.Model{
			ID:               m.ID,
			CanonicalSlug:    canonical,
			Context:          m.ContextLength,
			MaxOutput:        m.MaxTokens,
			Description:      m.Description,
			SupportedEfforts: aiutil.NormalizeEfforts(m.Reasoning.SupportedEfforts),
			DefaultEffort:    aiutil.NormalizeEffort(m.Reasoning.DefaultEffort),
		}
		if v, err := aiutil.ParseFloat(m.Pricing.Prompt); err == nil {
			model.InputCost = v * 1_000_000
		}
		models = append(models, model)
	}
	return models, nil
}

// listOpenRouterEndpoints fetches the upstream providers that can serve a
// model: GET /models/{author}/{slug}/endpoints. slug is the canonical
// identity plus any request variant (:free, :batch). One HTTP request per
// model; there is no bulk endpoint, so callers cache aggressively. Returns
// an empty slice (not an error) when the gateway returns no endpoints or
// answers HTTP 4xx (hosts such as OpenCode that do not implement this API).
func listOpenRouterEndpoints(ctx context.Context, baseURL string, headers map[string]string, client *http.Client, canonicalSlug string) ([]domain.ModelRoute, error) {
	base := strings.TrimRight(baseURL, "/")
	segments := strings.Split(canonicalSlug, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	url := base + "/models/" + strings.Join(segments, "/") + "/endpoints"
	var out struct {
		Data struct {
			Endpoints []struct {
				ProviderName string          `json:"provider_name"`
				Tag          string          `json:"tag"`
				Quantization string          `json:"quantization"`
				Status       int             `json:"status"`
				Latency      json.RawMessage `json:"latency_last_30m"`
				Throughput   json.RawMessage `json:"throughput_last_30m"`
				Pricing      struct {
					Prompt     string `json:"prompt"`
					Completion string `json:"completion"`
				} `json:"pricing"`
			} `json:"endpoints"`
		} `json:"data"`
	}
	if err := aiutil.DoJSON(ctx, client, http.MethodGet, url, headers, nil, &out); err != nil {
		if code := domain.HTTPStatusCode(err); code >= 400 && code <= 499 {
			return []domain.ModelRoute{}, nil
		}
		return nil, err
	}
	routes := make([]domain.ModelRoute, 0, len(out.Data.Endpoints))
	for _, e := range out.Data.Endpoints {
		slug := strings.TrimSpace(e.Tag)
		if slug == "" {
			slug = routeSlugFallback(e.ProviderName)
		}
		if slug == "" {
			continue
		}
		routes = append(routes, domain.ModelRoute{
			Slug:         slug,
			Name:         e.ProviderName,
			Quantization: e.Quantization,
			Status:       e.Status,
			Latency:      parseRouteMetric(e.Latency),
			Throughput:   parseRouteMetric(e.Throughput),
			InputCost:    parseRoutePrice(e.Pricing.Prompt),
			OutputCost:   parseRoutePrice(e.Pricing.Completion),
		})
	}
	return routes, nil
}

// parseRoutePrice converts an OpenRouter per-token price string to USD per
// 1M tokens. Invalid, negative, and non-finite values are treated as unknown;
// a valid zero remains a non-nil pointer so free routes are distinguishable
// from routes where pricing was omitted.
func parseRoutePrice(raw string) *float64 {
	v, err := aiutil.ParseFloat(raw)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	v *= 1_000_000
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// parseRouteMetric decodes a rolling-metric field that OpenRouter serves
// in two shapes: a bare number, or an object of percentiles like
// {"p50": 787, "p75": 1292, "p90": 2382, "p99": 7492}. We take the p50
// (median) as the representative value. null/absent → nil. Latency is
// milliseconds, throughput tokens/sec.
func parseRouteMetric(raw json.RawMessage) *float64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		return &num
	}
	var obj struct {
		P50 float64 `json:"p50"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return &obj.P50
	}
	return nil
}

// routeSlugFallback derives a routing slug from a provider display name
// when the gateway omits the tag field (rare). Lowercased, spaces → dashes.
func routeSlugFallback(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

// listGeminiModels fetches the Generative Language model catalog
// (GET /v1beta/models) that the Gemini wire serves. Chat-capable entries carry
// the token limits the model picker shows; embedding and media-only entries are
// filtered out by the provider.
func listGeminiModels(ctx context.Context, baseURL, apiKey string, client *http.Client) ([]domain.Model, error) {
	provider, err := gemini.New(gemini.Config{
		APIKey:         apiKey,
		BaseURL:        baseURL,
		HTTPClient:     client,
		APIKeyOptional: true,
	})
	if err != nil {
		return nil, err
	}
	infos, err := provider.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]domain.Model, 0, len(infos))
	for _, info := range infos {
		if info.ID == "" {
			continue
		}
		models = append(models, domain.Model{
			ID:          info.ID,
			DisplayName: info.Name,
			Context:     info.InputTokenLimit,
			MaxOutput:   info.OutputTokenLimit,
			Description: info.Description,
		})
	}
	return models, nil
}

// listAnthropicModels fetches the Anthropic model catalog
// (id, context window, pricing) from the /v1/models endpoint.
func listAnthropicModels(ctx context.Context, baseURL, apiKey string, client *http.Client) ([]domain.Model, error) {
	base := strings.TrimRight(baseURL, "/")
	url := base
	if !strings.HasSuffix(url, "/models") {
		if strings.HasSuffix(url, "/v1") {
			url += "/models"
		} else {
			url += "/v1/models"
		}
	}
	headers := map[string]string{}
	if apiKey != "" {
		headers["x-api-key"] = apiKey
	}
	var out struct {
		Data []struct {
			ID            string `json:"id"`
			DisplayName   string `json:"display_name"`
			ContextWindow int    `json:"context_window"`
			Pricing       struct {
				Input string `json:"input"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := aiutil.DoJSON(ctx, client, http.MethodGet, url, headers, nil, &out); err != nil {
		return nil, err
	}
	models := make([]domain.Model, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		model := domain.Model{ID: m.ID, Context: m.ContextWindow, Description: m.DisplayName}
		// pricing strings look like "3.0" (USD per MTok)
		if v, err := aiutil.ParseFloat(m.Pricing.Input); err == nil {
			model.InputCost = v
		}
		models = append(models, model)
	}
	return models, nil
}
