package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Embedder talks to any OpenAI-compatible /v1/embeddings endpoint.
// Works with OpenAI Platform, OpenRouter, and other gateways that expose
// the standard embeddings API. Requires an API key with platform billing.
type Embedder struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
	dim     int
}

// NewEmbedder creates an OpenAI-compatible embedding provider.
// baseURL should be the API root (e.g. "https://api.openai.com/v1" or
// "https://openrouter.ai/api/v1"). model is the embedding model ID
// (e.g. "text-embedding-3-small" or "openai/text-embedding-3-small").
func NewEmbedder(baseURL, apiKey, model string) *Embedder {
	return &Embedder{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		Client:  &http.Client{Timeout: 300 * time.Second},
	}
}

func (e *Embedder) Name() string { return "openai-compat/" + e.Model }

func (e *Embedder) Dim() int {
	if e.dim > 0 {
		return e.dim
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	emb, err := e.Embed(ctx, "dimension probe")
	if err != nil || len(emb) == 0 {
		return 0
	}
	e.dim = len(emb)
	return e.dim
}

func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	batch, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(batch) == 0 {
		return nil, fmt.Errorf("embeddings: empty response")
	}
	return batch[0], nil
}

func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	payload := map[string]any{"model": e.Model, "input": texts}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", e.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embeddings: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.APIKey)

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embeddings: %s: %s", resp.Status, b)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("embeddings: decode: %w", err)
	}
	out := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		out[i] = d.Embedding
	}
	if len(out) > 0 && e.dim == 0 {
		e.dim = len(out[0])
	}
	return out, nil
}
