package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"nusashell/infrastructure/ai/core"
)

// ResponsesCompactRequest is the body sent to POST /responses/compact. Input
// is the canonical context window to compact (blob items + live message
// items); it is marshalled verbatim. Instructions is optional system text.
type ResponsesCompactRequest struct {
	Model        string
	Input        any
	Instructions string
}

// ResponsesCompactUsage mirrors the usage object returned by the compact
// endpoint.
type ResponsesCompactUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// ResponsesCompactResponse carries the compacted context window. Output is the
// opaque canonical next context window (an encrypted compaction item plus any
// retained items) kept as raw JSON so it can be replayed verbatim to the next
// /responses call without parsing or pruning.
type ResponsesCompactResponse struct {
	Output []json.RawMessage
	Usage  ResponsesCompactUsage
}

type responsesCompactWireRequest struct {
	Model        string `json:"model"`
	Input        any    `json:"input"`
	Instructions string `json:"instructions,omitempty"`
}

type responsesCompactWireResponse struct {
	ID     string                    `json:"id"`
	Object string                    `json:"object"`
	Output []json.RawMessage         `json:"output"`
	Usage  responsesCompactWireUsage `json:"usage"`
}

type responsesCompactWireUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Compact calls the standalone OpenAI server-side compaction endpoint
// (POST /responses/compact). The returned Output is the opaque next context
// window and must be passed as-is to the next /responses call. A non-2xx
// response is returned as a core.HTTPError so callers can detect capability
// rejection (404/400) and fall back to client-side summarization.
func (p *Provider) Compact(ctx context.Context, req ResponsesCompactRequest) (*ResponsesCompactResponse, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("openai: model is required for responses compact")
	}
	wire := responsesCompactWireRequest{
		Model:        req.Model,
		Input:        req.Input,
		Instructions: req.Instructions,
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal compact request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url("/responses/compact"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create compact request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, core.NewNetworkError(p.Name(), "compact request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, core.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, core.NewNetworkError(p.Name(), "read compact response failed", err)
	}
	var parsed responsesCompactWireResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, core.NewProviderErrorWithCause(p.Name(), core.ErrorTypeProvider, "openai: decode compact response", err)
	}
	return &ResponsesCompactResponse{
		Output: parsed.Output,
		Usage: ResponsesCompactUsage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
			TotalTokens:  parsed.Usage.TotalTokens,
		},
	}, nil
}
