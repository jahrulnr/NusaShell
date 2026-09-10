package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nusashell/infrastructure/ai/core"
)

func (p *Provider) Chat(ctx context.Context, req *core.Request) (*core.Response, error) {
	wire, warnings, err := p.buildRequest(req, false)
	if err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.generateURL(req.Model, false), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq, req.ProviderOptions); err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, core.NewNetworkError(p.Name(), "request failed", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, core.NewNetworkError(p.Name(), "read response failed", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, core.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	var parsed generateContentResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, core.NewProviderErrorWithCause(p.Name(), core.ErrorTypeProvider, "gemini: decode response", err)
	}
	if parsed.Error != nil {
		return nil, core.NewHTTPError(p.Name(), errorStatus(parsed.Error), string(data))
	}
	out, err := convertResponse(&parsed, req.Model)
	if err != nil {
		return nil, core.WrapError(err, p.Name())
	}
	out.Warnings = append(warnings, out.Warnings...)
	core.CaptureRawResponse(req, out, data)
	return out, nil
}

func (p *Provider) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	wire, warnings, err := p.buildRequest(req, true)
	if err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal stream request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.generateURL(req.Model, true), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create stream request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq, req.ProviderOptions); err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, core.NewNetworkError(p.Name(), "stream request failed", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, core.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	return newStream(resp, req, warnings), nil
}

// ListModels returns the models the API exposes for generateContent, dropping
// embedding, image, and video-only entries so the chat picker stays clean.
func (p *Provider) ListModels(ctx context.Context) ([]core.ModelInfo, error) {
	var out []core.ModelInfo
	pageToken := ""
	for page := 0; page < 10; page++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.modelsURL(pageToken), nil)
		if err != nil {
			return nil, fmt.Errorf("gemini: create models request: %w", err)
		}
		if err := p.setHeaders(ctx, httpReq, nil); err != nil {
			return nil, core.WrapValidationError(p.Name(), err)
		}
		resp, err := p.cfg.HTTPClient.Do(httpReq)
		if err != nil {
			return nil, core.NewNetworkError(p.Name(), "models request failed", err)
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, core.NewNetworkError(p.Name(), "read models response failed", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, core.NewHTTPError(p.Name(), resp.StatusCode, string(data))
		}
		var payload modelList
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, core.NewProviderErrorWithCause(p.Name(), core.ErrorTypeProvider, "gemini: decode models response", err)
		}
		for _, item := range payload.Models {
			id := strings.TrimPrefix(strings.TrimSpace(item.Name), "models/")
			if id == "" {
				continue
			}
			if !supportsGenerateContent(item.SupportedGenerationMethods) || !isChatModelID(id) {
				continue
			}
			out = append(out, core.ModelInfo{
				ID:               id,
				Name:             firstNonEmpty(item.DisplayName, id),
				Provider:         p.Name(),
				Description:      item.Description,
				InputTokenLimit:  item.InputTokenLimit,
				OutputTokenLimit: item.OutputTokenLimit,
			})
		}
		if payload.NextPageToken == "" {
			break
		}
		pageToken = payload.NextPageToken
	}
	return out, nil
}

// supportsGenerateContent keeps entries that can serve chat. An empty method
// list (some Gemini-compatible gateways omit it) is treated as chat-capable.
func supportsGenerateContent(methods []string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, method := range methods {
		if method == "generateContent" {
			return true
		}
	}
	return false
}

// isChatModelID excludes model IDs that are not chat models even though they
// expose generateContent. Image generation, TTS, and embedding variants
// advertise generateContent but serve media or embeddings, not chat, so they
// are filtered from the chat picker by their final path segment.
func isChatModelID(id string) bool {
	segment := id
	if idx := strings.LastIndexByte(segment, '/'); idx >= 0 {
		segment = segment[idx+1:]
	}
	segment = strings.ToLower(segment)
	for _, keyword := range []string{"image", "tts", "embedding"} {
		if strings.Contains(segment, keyword) {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// errorStatus maps an embedded error payload onto the HTTP status core uses to
// classify retryability. Gemini reports 200 responses with an error body for
// some mid-stream failures, so the embedded code is authoritative there.
func errorStatus(body *apiErrorBody) int {
	if body == nil {
		return http.StatusInternalServerError
	}
	if body.Code >= 100 && body.Code <= 599 {
		return body.Code
	}
	return http.StatusInternalServerError
}
