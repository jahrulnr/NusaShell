package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	aiutil "nusashell/infrastructure/ai/internal"
)

// EmbeddingMaxTokens is the default token cap applied to embedding inputs.
// Many small and self-hosted embedding models (voyage-3-lite, Ollama models,
// llama.cpp builds) cap inputs at 512 tokens; providers with a larger
// documented context pass their real limit via NewEmbedder. Sending more
// tokens than the model accepts makes the whole batch fail with HTTP 400.
const EmbeddingMaxTokens = 512

// embeddingCharsPerToken is the conservative chars-per-token ratio used to
// fit embedding inputs under the token cap without a local tokenizer.
// Latin text tokenizes at ~3.5-4.5 chars/token, so 3 leaves headroom.
const embeddingCharsPerToken = 3

// Retry a provider-reported token overflow at most twice. The response gives
// the tokenizer's actual count, so the retry can shrink proportionally
// without imposing an overly conservative byte cap on every normal request.
const embeddingOverflowMaxRetries = 2

var embeddingTokenOverflowRE = regexp.MustCompile(`(?i)Embedding input has\s+(\d+)\s+tokens?,\s+exceeding the model maximum of\s+(\d+)`)

// Embedder talks to any OpenAI-compatible /v1/embeddings endpoint.
// Works with OpenAI Platform, OpenRouter, and other gateways that expose
// the standard embeddings API. Requires an API key with platform billing.
type Embedder struct {
	BaseURL   string
	APIKey    string
	Model     string
	Client    *http.Client
	MaxTokens int // per-input token cap; 0 = EmbeddingMaxTokens
	dim       int
}

// NewEmbedder creates an OpenAI-compatible embedding provider.
// baseURL should be the API root (e.g. "https://api.openai.com/v1" or
// "https://openrouter.ai/api/v1"). model is the embedding model ID
// (e.g. "text-embedding-3-small" or "openai/text-embedding-3-small").
// maxTokens caps each input (0 = EmbeddingMaxTokens).
func NewEmbedder(baseURL, apiKey, model string, maxTokens int) *Embedder {
	return &Embedder{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		APIKey:    apiKey,
		Model:     model,
		Client:    &http.Client{Timeout: 300 * time.Second},
		MaxTokens: maxTokens,
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
	// Fit every input under the model's token cap up front: one oversized
	// entry fails the whole batch (HTTP 400 "exceeding the model maximum").
	maxTokens := e.MaxTokens
	if maxTokens <= 0 {
		maxTokens = EmbeddingMaxTokens
	}
	inputs := make([]string, len(texts))
	for i, t := range texts {
		inputs[i] = truncateInput(t, maxTokens)
	}
	for attempt := 0; ; attempt++ {
		payload := map[string]any{"model": e.Model, "input": inputs}
		body, _ := json.Marshal(payload)

		req, err := http.NewRequestWithContext(ctx, "POST", e.BaseURL+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("embeddings: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
		req.Header.Set("User-Agent", aiutil.NusaShellUserAgent)
		if aiutil.IsOpenRouterURL(e.BaseURL) {
			for name, value := range aiutil.OpenRouterAttributionHeaders() {
				req.Header.Set(name, value)
			}
		}

		resp, err := e.Client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("embeddings: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			responseBody, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			actual, limit, overflow := parseEmbeddingTokenOverflow(string(responseBody))
			if overflow && attempt < embeddingOverflowMaxRetries {
				inputs = shrinkEmbeddingInputs(inputs, actual, limit)
				continue
			}
			return nil, fmt.Errorf("embeddings: %s: %s", resp.Status, responseBody)
		}

		var result struct {
			Data []struct {
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("embeddings: decode: %w", decodeErr)
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
}

// truncationMarker signals that an embedding input was cut; it is counted
// inside the token budget so the sent text never exceeds the cap.
const truncationMarker = "…[truncated]"

// truncateInput keeps the first maxTokens tokens (estimated at
// embeddingCharsPerToken chars each) of s and appends an omission marker so
// callers can tell the input was cut. Rune-safe for non-ASCII content.
func truncateInput(s string, maxTokens int) string {
	return truncateInputRunes(s, maxTokens*embeddingCharsPerToken)
}

func truncateInputRunes(s string, limit int) string {
	runes := []rune(s)
	if limit <= 0 {
		return ""
	}
	if len(runes) <= limit {
		return s
	}
	marker := []rune(truncationMarker)
	if limit <= len(marker) {
		return string(runes[:limit])
	}
	head := limit - len(marker)
	return string(runes[:head]) + truncationMarker
}

func parseEmbeddingTokenOverflow(body string) (actual, limit int, ok bool) {
	match := embeddingTokenOverflowRE.FindStringSubmatch(body)
	if len(match) != 3 {
		return 0, 0, false
	}
	actual, actualErr := strconv.Atoi(match[1])
	limit, limitErr := strconv.Atoi(match[2])
	if actualErr != nil || limitErr != nil || actual <= limit || limit <= 0 {
		return 0, 0, false
	}
	return actual, limit, true
}

func shrinkEmbeddingInputs(inputs []string, actualTokens, maxTokens int) []string {
	// Leave up to eight tokens for provider-added BOS/EOS or other special
	// tokens and for integer rounding. Applying the provider's measured ratio
	// retains substantially more content than a universal one-byte-per-token
	// limit while still correcting tokenizer-specific underestimation.
	reserve := max(1, min(8, maxTokens/64))
	targetTokens := max(1, maxTokens-reserve)
	out := make([]string, len(inputs))
	for i, input := range inputs {
		limit := len([]rune(input)) * targetTokens / actualTokens
		out[i] = truncateInputRunes(input, limit)
	}
	return out
}
