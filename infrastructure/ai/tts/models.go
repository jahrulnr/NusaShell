package tts

import (
	"context"
	"net/http"
	"strings"

	aiutil "nusashell/infrastructure/ai/internal"
)

// ModelLister fetches speech-generation model IDs from a provider's /models
// endpoint filtered by output_modalities=speech (OpenRouter's documented
// TTS discovery route — plain /models hides these models entirely). Any
// HTTP error returns an empty slice so hosts that reject the filter (the
// OpenAI platform among them) still work via plain /models + models.dev
// catalog tagging + the known-TTS allowlist.
type ModelLister struct {
	BaseURL string
	Client  *http.Client
}

// NewModelLister creates a speech-model lister. If client is nil a default
// HTTP client is used.
func NewModelLister(baseURL string, client *http.Client) *ModelLister {
	if client == nil {
		client = &http.Client{}
	}
	return &ModelLister{BaseURL: strings.TrimRight(baseURL, "/"), Client: client}
}

// ListSpeechModels fetches speech-capable model IDs.
func (l *ModelLister) ListSpeechModels(ctx context.Context, apiKey string) ([]string, error) {
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	return aiutil.FetchSpeechModels(ctx, l.Client, l.BaseURL, headers, nil), nil
}
