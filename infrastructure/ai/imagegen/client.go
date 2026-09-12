// Package imagegen implements application.ImageGenerator for OpenAI Images
// and the OpenRouter dedicated Image API.
package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/codex"
	aiutil "nusashell/infrastructure/ai/internal"
	"nusashell/pkg/httpclient"
)

const (
	backendOpenAI     = "openai"
	backendOpenRouter = "openrouter"
	// BackendCodex is the ChatGPT Codex image backend (POST
	// images/generations and images/edits on the resolved provider base URL).
	BackendCodex = "codex"
)

// Client talks to one image-generation HTTP backend.
type Client struct {
	Backend string
	BaseURL string
	APIKey  string
	// AccountID is the ChatGPT account id sent as ChatGPT-Account-ID on the
	// Codex backend. Empty skips the header.
	AccountID string
	HTTP      *http.Client
}

// NewFactory returns an ImageGeneratorFactory that uses the central HTTP
// transport when no client is supplied by the composition root.
func NewFactory() application.ImageGeneratorFactory {
	return NewFactoryWithClient(nil)
}

// NewFactoryWithClient returns an ImageGeneratorFactory using client for all
// generated image requests and signed-URL downloads. A nil client uses a
// central client from pkg/httpclient.
func NewFactoryWithClient(client *http.Client) application.ImageGeneratorFactory {
	if client == nil {
		client = httpclient.New()
	}
	return func(_ context.Context, p *domain.Provider, apiKey string) (application.ImageGenerator, error) {
		if p == nil {
			return nil, fmt.Errorf("image provider is required")
		}
		if aiutil.IsOpenRouterURL(p.BaseURL) {
			return &Client{Backend: backendOpenRouter, BaseURL: p.BaseURL, APIKey: apiKey, HTTP: client}, nil
		}
		if !p.KindCapabilities().HasImageEndpoint {
			return nil, fmt.Errorf("provider kind %q has no image generation API — pick an OpenAI or OpenRouter image model in Settings → Image generation", p.Kind)
		}
		return &Client{Backend: backendOpenAI, BaseURL: p.BaseURL, APIKey: apiKey, HTTP: client}, nil
	}
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return httpclient.New()
}

func (c *Client) Generate(ctx context.Context, req application.ImageGenRequest) (*application.ImageGenResult, error) {
	if req.N <= 0 {
		req.N = 1
	}
	if req.N > 4 {
		req.N = 4
	}
	switch c.Backend {
	case backendOpenRouter:
		return c.generateOpenRouter(ctx, req)
	case BackendCodex:
		return c.generateCodex(ctx, req)
	default:
		return c.generateOpenAI(ctx, req)
	}
}

func (c *Client) headers() map[string]string {
	h := map[string]string{}
	if c.APIKey != "" {
		h["Authorization"] = "Bearer " + c.APIKey
	}
	if c.Backend == backendOpenRouter || aiutil.IsOpenRouterURL(c.BaseURL) {
		for k, v := range aiutil.OpenRouterAttributionHeaders() {
			h[k] = v
		}
	}
	return h
}

func omitAuto(value string) string {
	v := strings.TrimSpace(strings.ToLower(value))
	if v == "" || v == "auto" {
		return ""
	}
	return strings.TrimSpace(value)
}

type imageItem struct {
	B64JSON   string `json:"b64_json"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
}

type imagesResponse struct {
	Data  []imageItem `json:"data"`
	Usage struct {
		TotalTokens  int     `json:"total_tokens"`
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		Cost         float64 `json:"cost"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func decodeImages(ctx context.Context, client *http.Client, resp imagesResponse, provider, model string) (*application.ImageGenResult, error) {
	if resp.Error != nil && strings.TrimSpace(resp.Error.Message) != "" {
		return nil, fmt.Errorf("%s", strings.TrimSpace(resp.Error.Message))
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("image provider returned no images")
	}
	out := &application.ImageGenResult{
		Provider:    provider,
		Model:       model,
		UsageTokens: resp.Usage.TotalTokens,
		CostUSD:     resp.Usage.Cost,
	}
	if out.UsageTokens == 0 {
		out.UsageTokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}
	for i, item := range resp.Data {
		raw := strings.TrimSpace(item.B64JSON)
		if raw == "" {
			if item.URL == "" {
				return nil, fmt.Errorf("image provider returned empty b64_json (item %d)", i)
			}
			// Download the signed URL. Most image routers return URLs
			// (the default response_format); we always fetch the bytes
			// so the rest of the pipeline has image data to persist.
			data, media, err := fetchImageURL(ctx, client, item.URL)
			if err != nil {
				return nil, fmt.Errorf("download image url (item %d): %w", i, err)
			}
			out.Images = append(out.Images, application.GeneratedImage{Bytes: data, MediaType: media})
			continue
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("decode b64_json (item %d): %w", i, err)
		}
		media := strings.TrimSpace(item.MediaType)
		out.Images = append(out.Images, application.GeneratedImage{Bytes: data, MediaType: media})
	}
	return out, nil
}

// fetchImageURL downloads image bytes from a signed URL returned by an
// image provider. The media type is derived from the Content-Type header
// (falling back to image/png). A 30s timeout bounds the download so a
// slow CDN cannot stall the agent turn indefinitely.
func fetchImageURL(ctx context.Context, client *http.Client, url string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", aiutil.NusaShellUserAgent)
	if client == nil {
		client = httpclient.Shared()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("image url returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	media := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if media == "" {
		media = "image/png"
	}
	return data, media, nil
}

func (c *Client) generateOpenAI(ctx context.Context, req application.ImageGenRequest) (*application.ImageGenResult, error) {
	if len(req.References) > 0 {
		return c.openaiEdits(ctx, req)
	}
	body := map[string]any{
		"model":  req.Model,
		"prompt": req.Prompt,
		"n":      req.N,
	}
	if size := omitAuto(req.Size); size != "" {
		body["size"] = size
	}
	if quality := omitAuto(req.Quality); quality != "" {
		body["quality"] = quality
	}
	if background := omitAuto(req.Background); background != "" {
		body["background"] = background
	}
	url := aiutil.JoinEndpoint(c.BaseURL, "/images/generations")
	var decoded imagesResponse
	if err := aiutil.DoJSON(ctx, c.httpClient(), http.MethodPost, url, c.headers(), body, &decoded); err != nil {
		return nil, err
	}
	return decodeImages(ctx, c.httpClient(), decoded, backendOpenAI, req.Model)
}

func (c *Client) openaiEdits(ctx context.Context, req application.ImageGenRequest) (*application.ImageGenResult, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("model", req.Model)
	_ = writer.WriteField("prompt", req.Prompt)
	_ = writer.WriteField("n", fmt.Sprintf("%d", req.N))
	if size := omitAuto(req.Size); size != "" {
		_ = writer.WriteField("size", size)
	}
	if quality := omitAuto(req.Quality); quality != "" {
		_ = writer.WriteField("quality", quality)
	}
	if background := omitAuto(req.Background); background != "" {
		_ = writer.WriteField("background", background)
	}
	for i, ref := range req.References {
		var ext string
		switch strings.ToLower(strings.TrimSpace(ref.MediaType)) {
		case "image/jpeg":
			ext = ".jpg"
		case "image/webp":
			ext = ".webp"
		default:
			ext = ".png"
		}
		part, err := writer.CreateFormFile("image", fmt.Sprintf("ref-%d%s", i, ext))
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(ref.Data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	url := aiutil.JoinEndpoint(c.BaseURL, "/images/edits")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("User-Agent", aiutil.NusaShellUserAgent)
	httpReq.Header.Set("Accept", "application/json")
	for k, v := range c.headers() {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, &domain.ProviderError{Kind: domain.KindConnect, Temporary: true, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		retryAfter := time.Duration(0)
		if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds > 0 {
			retryAfter = time.Duration(seconds) * time.Second
		}
		return nil, &domain.ProviderError{
			Kind:       domain.KindHTTPStatus,
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Err:        fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
		}
	}
	var decoded imagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	return decodeImages(ctx, c.httpClient(), decoded, backendOpenAI, req.Model)
}

func (c *Client) generateOpenRouter(ctx context.Context, req application.ImageGenRequest) (*application.ImageGenResult, error) {
	body := map[string]any{
		"model":  req.Model,
		"prompt": req.Prompt,
		"n":      req.N,
	}
	if size := omitAuto(req.Size); size != "" {
		body["size"] = size
	}
	if quality := omitAuto(req.Quality); quality != "" {
		body["quality"] = quality
	}
	if background := omitAuto(req.Background); background != "" {
		body["background"] = background
	}
	if len(req.References) > 0 {
		refs := make([]map[string]any, 0, len(req.References))
		for _, ref := range req.References {
			media := ref.MediaType
			if media == "" {
				media = "image/png"
			}
			dataURL := "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(ref.Data)
			refs = append(refs, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": dataURL},
			})
		}
		body["input_references"] = refs
	}
	url := aiutil.JoinEndpoint(c.BaseURL, "/images")
	var decoded imagesResponse
	if err := aiutil.DoJSON(ctx, c.httpClient(), http.MethodPost, url, c.headers(), body, &decoded); err != nil {
		return nil, err
	}
	return decodeImages(ctx, c.httpClient(), decoded, backendOpenRouter, req.Model)
}

// maxCodexImageB64 is the base64 length of 32 MiB — the Codex executor's
// generated-image cap. Guarded before decoding so an oversized payload fails
// fast without allocating the decoded bytes.
const maxCodexImageB64 = 44_739_244 // 4 * ceil(32<<20 / 3)

// generateCodex posts to the ChatGPT Codex images endpoints. The request
// body mirrors the Codex DTOs (codex-api/src/images.rs): model, prompt,
// background/quality/size with "auto" defaults, and images[] data URLs for
// edit mode. n is intentionally omitted — the built-in Codex tool always
// requests a single image (clamped here) and every call may be billable.
func (c *Client) generateCodex(ctx context.Context, req application.ImageGenRequest) (*application.ImageGenResult, error) {
	req.N = 1 // Codex requests one image; multi-image generation is not evidenced.

	body := map[string]any{
		"model":      req.Model,
		"prompt":     req.Prompt,
		"background": defaultOr(req.Background, "auto"),
		"quality":    defaultOr(req.Quality, "auto"),
		"size":       defaultOr(req.Size, "auto"),
	}
	path := "/images/generations"
	if len(req.References) > 0 {
		path = "/images/edits"
		refs := make([]map[string]any, 0, len(req.References))
		for _, ref := range req.References {
			media := strings.TrimSpace(ref.MediaType)
			if media == "" {
				media = "image/png"
			}
			refs = append(refs, map[string]any{
				"image_url": "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(ref.Data),
			})
		}
		body["images"] = refs
	}
	url := aiutil.JoinEndpoint(c.BaseURL, path)
	headers := c.codexHeaders(req.TurnID)

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", codex.CodexUserAgent)
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, &domain.ProviderError{Kind: domain.KindConnect, Temporary: true, Err: err}
	}
	defer resp.Body.Close()
	// One valid response item holds up to 32 MiB of decoded bytes, whose
	// base64 form is maxCodexImageB64 chars. A 4 MiB slack covers the JSON
	// wrapper so any single valid image fits while a runaway body still
	// fails fast without unbounded allocation.
	limit := maxCodexImageB64 + 4<<20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)))
	if err != nil {
		return nil, err
	}
	if len(raw) == limit {
		return nil, fmt.Errorf("codex image response too large")
	}
	if resp.StatusCode >= 400 {
		return nil, mapCodexError(resp.StatusCode, raw)
	}
	var decoded imagesResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode codex image response: %w", err)
	}
	for i, item := range decoded.Data {
		if len(strings.TrimSpace(item.B64JSON)) > maxCodexImageB64 {
			return nil, fmt.Errorf("generated image %d exceeds the 32 MiB executor limit", i+1)
		}
	}
	return decodeImages(ctx, c.httpClient(), decoded, BackendCodex, req.Model)
}

func defaultOr(value, def string) string {
	if v := strings.TrimSpace(value); v != "" {
		return v
	}
	return def
}

// codexHeaders builds the Codex image request headers. A plain bearer token
// alone is not enough — the real-test evidence required the originator and
// x-codex-image-turn-id metadata (plus the account id when present) to pass
// the 403 gate.
func (c *Client) codexHeaders(turnID string) map[string]string {
	h := map[string]string{
		"originator": codex.DefaultOriginator,
		"User-Agent": codex.CodexUserAgent,
	}
	if c.APIKey != "" {
		h["Authorization"] = "Bearer " + c.APIKey
	}
	if c.AccountID != "" {
		h["ChatGPT-Account-ID"] = c.AccountID
	}
	if turnID != "" {
		h["x-codex-image-turn-id"] = turnID
	}
	return h
}

// mapCodexError converts a non-2xx Codex image response into a
// domain.ProviderError. Image generation may be billable, so 429 responses
// are surfaced as hard failures (RetryAfter=0) instead of a blind retry
// loop; 5xx stay retriable for the caller's shared policy. No credential
// value is ever copied into the error.
func mapCodexError(status int, raw []byte) error {
	msg := strings.TrimSpace(string(raw))
	if len(msg) > 800 {
		msg = msg[:800]
	}
	var parsed struct {
		Error struct {
			Type     string      `json:"type"`
			Message  string      `json:"message"`
			ResetsAt json.Number `json:"resets_at"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &parsed)
	detail := strings.TrimSpace(parsed.Error.Message)
	if detail == "" {
		detail = msg
	}
	switch status {
	case http.StatusTooManyRequests: // 429
		if strings.Contains(parsed.Error.Type, "usage_limit_reached") {
			resetAt := codexResetsAtTime(parsed.Error.ResetsAt)
			msg := "image generation usage limit reached"
			if !resetAt.IsZero() {
				msg += fmt.Sprintf(" (resets at %s)", resetAt.Format(time.RFC3339))
			}
			return &domain.ProviderError{
				StatusCode:        http.StatusTooManyRequests,
				Err:               fmt.Errorf("%s", msg),
				UsageLimitResetAt: resetAt,
			}
		}
		return &domain.ProviderError{
			StatusCode: http.StatusTooManyRequests,
			Err:        fmt.Errorf("image generation rate limited: %s", detail),
		}
	default:
		if status >= 500 {
			return &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: status, Temporary: true, Err: fmt.Errorf("image generation failed (HTTP %d): %s", status, detail)}
		}
		return &domain.ProviderError{StatusCode: status, Err: fmt.Errorf("image generation failed (HTTP %d): %s", status, detail)}
	}
}

// codexResetsAtTime parses the optional usage-limit resets_at unix
// timestamp, returning the zero time when absent or invalid.
func codexResetsAtTime(sec json.Number) time.Time {
	if sec == "" {
		return time.Time{}
	}
	n, err := sec.Int64()
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}
