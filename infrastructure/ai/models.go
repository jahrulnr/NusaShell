package ai

import (
	"context"
	"net/http"
	"strings"

	"nusashell/domain"
	aiutil "nusashell/infrastructure/ai/internal"
)

// listOpenAIModels fetches the OpenAI-compatible /models catalog
// (id, context window, pricing, reasoning efforts) used by chat,
// responses, and OpenRouter providers.
func listOpenAIModels(ctx context.Context, baseURL string, headers map[string]string, client *http.Client) ([]domain.Model, error) {
	base := strings.TrimRight(baseURL, "/")
	url := base + "/models"
	var out struct {
		Data []struct {
			ID            string `json:"id"`
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
		model := domain.Model{
			ID:               m.ID,
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
