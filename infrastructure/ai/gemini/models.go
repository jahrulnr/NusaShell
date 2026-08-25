package gemini

import (
	"context"
	"net/http"
	"strings"

	"nusashell/domain"
	aiutil "nusashell/infrastructure/ai/internal"
)

// ListModels implements application.ModelLister for the Gemini API.
// Gemini's /v1beta/models endpoint returns models with a different shape
// than OpenAI-compatible providers: each model has a `name` (e.g.
// "models/gemini-2.5-flash") and `supportedGenerationMethods`.
func (a *Adapter) ListModels(ctx context.Context, apiKey string) ([]domain.Model, error) {
	base := strings.TrimRight(a.baseURL(), "/")
	url := base + "/models"
	headers := map[string]string{}
	if apiKey != "" {
		headers["x-goog-api-key"] = apiKey
	}
	var resp struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			DisplayName                string   `json:"displayName"`
			Description                string   `json:"description"`
			InputTokenLimit            int      `json:"inputTokenLimit"`
			OutputTokenLimit           int      `json:"outputTokenLimit"`
		} `json:"models"`
	}
	if err := aiutil.DoJSON(ctx, a.Client, http.MethodGet, url, headers, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.Model, 0, len(resp.Models))
	for _, m := range resp.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		if id == "" {
			continue
		}
		dm := domain.Model{
			ID:          id,
			DisplayName: m.DisplayName,
			Context:     m.InputTokenLimit,
			MaxOutput:   m.OutputTokenLimit,
			Description: m.Description,
		}
		// Classify by supported generation methods
		for _, method := range m.SupportedGenerationMethods {
			switch method {
			case "generateContent":
				dm.Kind = domain.ModelKindChat
			case "predict":
				dm.Kind = domain.ModelKindImage
			case "predictLongRunning":
				dm.Kind = domain.ModelKindVideo
			}
		}
		out = append(out, dm)
	}
	return out, nil
}
