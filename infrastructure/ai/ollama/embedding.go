// Package ollama implements the application.Embedder port for a local
// Ollama instance via the native /api/embed endpoint. Ollama does not
// require an API key.
package ollama

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

// Embedder talks to an Ollama instance via /api/embed. Works with local
// (no API key) and remote/cloud deployments (optional API key via auth proxy).
// Default model: nomic-embed-text (768 dim, free).
type Embedder struct {
	BaseURL string
	Model   string
	APIKey  string // optional — set when Ollama is behind an auth proxy
	Client  *http.Client
	dim     int
}

// New creates an Ollama embedding provider.
// baseURL defaults to http://localhost:11434 if empty.
// model defaults to nomic-embed-text if empty.
// apiKey is optional — only needed when Ollama is behind an auth proxy.
func New(baseURL, model, apiKey string) *Embedder {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-embed-text"
	}
	return &Embedder{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		APIKey:  apiKey,
		Client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (o *Embedder) Name() string { return "ollama/" + o.Model }

func (o *Embedder) Dim() int {
	if o.dim > 0 {
		return o.dim
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	emb, err := o.Embed(ctx, "dimension probe")
	if err != nil || len(emb) == 0 {
		return 0
	}
	o.dim = len(emb)
	return o.dim
}

func (o *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	batch, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(batch) == 0 {
		return nil, fmt.Errorf("ollama embed: empty response")
	}
	return batch[0], nil
}

func (o *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	payload := map[string]any{"model": o.Model, "input": texts}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", o.BaseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed: %s: %s", resp.Status, b)
	}

	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama embed: decode: %w", err)
	}
	if len(result.Embeddings) > 0 && o.dim == 0 {
		o.dim = len(result.Embeddings[0])
	}
	return result.Embeddings, nil
}
