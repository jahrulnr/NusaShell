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
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
	aiutil "nusashell/infrastructure/ai/internal"
)

const (
	backendOpenAI     = "openai"
	backendOpenRouter = "openrouter"
)

// Client talks to one image-generation HTTP backend.
type Client struct {
	Backend string
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewFactory returns an ImageGeneratorFactory that routes OpenRouter hosts
// to POST /images and OpenAI-compatible chat/responses providers to
// /images/generations (or /images/edits when reference images are present).
func NewFactory() application.ImageGeneratorFactory {
	client := newImageHTTPClient()
	return func(p *domain.Provider, apiKey string) (application.ImageGenerator, error) {
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

func newImageHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   300 * time.Second,
				KeepAlive: 300 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   300 * time.Second,
			ResponseHeaderTimeout: 300 * time.Second,
			IdleConnTimeout:       300 * time.Second,
		},
	}
}

func (c *Client) Generate(ctx context.Context, req application.ImageGenRequest) (*application.ImageGenResult, error) {
	if c.HTTP == nil {
		c.HTTP = newImageHTTPClient()
	}
	if req.N <= 0 {
		req.N = 1
	}
	if req.N > 4 {
		req.N = 4
	}
	switch c.Backend {
	case backendOpenRouter:
		return c.generateOpenRouter(ctx, req)
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

func decodeImages(resp imagesResponse, provider, model string) (*application.ImageGenResult, error) {
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
			data, media, err := fetchImageURL(context.Background(), item.URL)
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
func fetchImageURL(ctx context.Context, url string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", aiutil.NusaShellUserAgent)
	resp, err := http.DefaultClient.Do(req)
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
	if err := aiutil.DoJSON(ctx, c.HTTP, http.MethodPost, url, c.headers(), body, &decoded); err != nil {
		return nil, err
	}
	return decodeImages(decoded, backendOpenAI, req.Model)
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
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, &application.UpstreamError{Kind: application.KindConnect, Temporary: true, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		retryAfter := time.Duration(0)
		if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds > 0 {
			retryAfter = time.Duration(seconds) * time.Second
		}
		return nil, &application.UpstreamError{
			Kind:       application.KindHTTPStatus,
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Err:        fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
		}
	}
	var decoded imagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	return decodeImages(decoded, backendOpenAI, req.Model)
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
	if err := aiutil.DoJSON(ctx, c.HTTP, http.MethodPost, url, c.headers(), body, &decoded); err != nil {
		return nil, err
	}
	return decodeImages(decoded, backendOpenRouter, req.Model)
}
