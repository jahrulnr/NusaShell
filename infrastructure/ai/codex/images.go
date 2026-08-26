package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"nusashell/application"
	aiutil "nusashell/infrastructure/ai/internal"
)

// DefaultImageModel is the model the official Codex CLI always sends
// (codex-rs/ext/image-generation). ChatGPT plan image generation has no
// /images/models catalog; NusaShell seeds this id as kind=image.
const DefaultImageModel = "gpt-image-2"

const (
	codexImageTurnIDHeader   = "x-codex-image-turn-id"
	codexActiveLimitHeader   = "x-codex-active-limit"
	codexImageGenLimitID     = "image_gen"
	codexUsageLimitErrorType = "usage_limit_reached"
)

// ImagesClient talks to the Codex ChatGPT image HTTP API:
// POST {base}/images/generations and POST {base}/images/edits.
// Ported from openai/codex codex-rs/codex-api ImagesClient — JSON bodies
// (not OpenAI multipart edits), OAuth bearer auth, and usage-limit 429s.
type ImagesClient struct {
	BaseURL        string
	AccessToken    string
	AccountID      string
	Originator     string
	InstallationID string
	HTTP           *http.Client
}

func (c *ImagesClient) baseURL() string {
	if c != nil && strings.TrimSpace(c.BaseURL) != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *ImagesClient) originator() string {
	if c != nil && strings.TrimSpace(c.Originator) != "" {
		return c.Originator
	}
	return DefaultOriginator
}

func (c *ImagesClient) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return newImageHTTPClient()
}

func newImageHTTPClient() *http.Client {
	return &http.Client{
		Jar: SharedCloudflareCookieJar(),
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 180 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

func (c *ImagesClient) headers(turnID string) map[string]string {
	h := map[string]string{
		"originator": c.originator(),
	}
	if c != nil && c.AccessToken != "" {
		h["Authorization"] = "Bearer " + c.AccessToken
	}
	if c != nil && c.AccountID != "" {
		h["ChatGPT-Account-ID"] = c.AccountID
	}
	if c != nil && c.InstallationID != "" {
		h["x-codex-installation-id"] = c.InstallationID
	}
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		h[codexImageTurnIDHeader] = turnID
	}
	return h
}

// Generate implements application.ImageGenerator for the Codex images API.
func (c *ImagesClient) Generate(ctx context.Context, req application.ImageGenRequest) (*application.ImageGenResult, error) {
	if req.N <= 0 {
		req.N = 1
	}
	if req.N > 4 {
		req.N = 4
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = DefaultImageModel
	}
	body := map[string]any{
		"prompt":     req.Prompt,
		"model":      model,
		"background": codexImageParam(req.Background),
		"quality":    codexImageParam(req.Quality),
		"size":       codexImageParam(req.Size),
	}
	if req.N > 1 {
		body["n"] = req.N
	}
	path := "/images/generations"
	if len(req.References) > 0 {
		path = "/images/edits"
		images := make([]map[string]string, 0, len(req.References))
		for _, ref := range req.References {
			media := strings.TrimSpace(ref.MediaType)
			if media == "" {
				media = "image/png"
			}
			images = append(images, map[string]string{
				"image_url": "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(ref.Data),
			})
		}
		body["images"] = images
	}
	url := aiutil.JoinEndpoint(c.baseURL(), path)
	var decoded codexImagesResponse
	if err := c.postJSON(ctx, url, c.headers(req.TurnID), body, &decoded); err != nil {
		return nil, err
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return nil, fmt.Errorf("%s", strings.TrimSpace(decoded.Error.Message))
	}
	if len(decoded.Data) == 0 {
		return nil, fmt.Errorf("image provider returned no images")
	}
	var media string
	switch strings.ToLower(strings.TrimSpace(decoded.OutputFormat)) {
	case "jpeg", "jpg":
		media = "image/jpeg"
	case "webp":
		media = "image/webp"
	case "png", "":
		media = "image/png"
	}
	out := &application.ImageGenResult{
		Provider:    "codex",
		Model:       model,
		UsageTokens: decoded.Usage.TotalTokens,
	}
	if out.UsageTokens == 0 {
		out.UsageTokens = decoded.Usage.InputTokens + decoded.Usage.OutputTokens
	}
	for i, item := range decoded.Data {
		raw := strings.TrimSpace(item.B64JSON)
		if raw == "" {
			return nil, fmt.Errorf("image provider returned empty b64_json (item %d)", i)
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("decode b64_json (item %d): %w", i, err)
		}
		out.Images = append(out.Images, application.GeneratedImage{Bytes: data, MediaType: media})
	}
	return out, nil
}

func codexImageParam(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return "auto"
	}
	return v
}

type codexImagesResponse struct {
	Data         []codexImageData `json:"data"`
	OutputFormat string           `json:"output_format"`
	Usage        struct {
		TotalTokens  int `json:"total_tokens"`
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type codexImageData struct {
	B64JSON string `json:"b64_json"`
}

func (c *ImagesClient) postJSON(ctx context.Context, url string, headers map[string]string, body any, out *codexImagesResponse) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", CodexUserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return &application.UpstreamError{Kind: application.KindConnect, Temporary: true, Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		bodySnippet := strings.TrimSpace(string(raw[:min(len(raw), 4096)]))
		// Log diagnostic details for 403/401 to help distinguish Cloudflare
		// region blocks, plan restrictions, and policy violations — the
		// raw body is often empty or HTML for Cloudflare blocks.
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			cfRay := resp.Header.Get("cf-ray")
			server := resp.Header.Get("Server")
			contentType := resp.Header.Get("Content-Type")
			fmt.Fprintf(os.Stderr, "[codex-images] HTTP %d: server=%s cf-ray=%s content-type=%s body_len=%d body=%q\n",
				resp.StatusCode, server, cfRay, contentType, len(raw), bodySnippet)
		}
		return &application.UpstreamError{
			Kind:       application.KindHTTPStatus,
			StatusCode: resp.StatusCode,
			RetryAfter: parseCodexImageRetryAfter(resp.Header, raw, time.Now()),
			Temporary:  resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500,
			Err:        fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, bodySnippet),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("failed to decode image response: %w", err)
	}
	return nil
}

type usageLimitBody struct {
	Error struct {
		Type     string `json:"type"`
		ResetsAt *int64 `json:"resets_at"`
		Message  string `json:"message"`
	} `json:"error"`
}

func parseCodexImageRetryAfter(headers http.Header, body []byte, now time.Time) time.Duration {
	retryAfter := time.Duration(0)
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		retryAfter = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(value); err == nil && when.After(now) {
		retryAfter = when.Sub(now)
	}
	var parsed usageLimitBody
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.ResetsAt != nil && *parsed.Error.ResetsAt > 0 {
		if until := time.Unix(*parsed.Error.ResetsAt, 0); until.After(now) {
			if d := until.Sub(now); d > retryAfter {
				retryAfter = d
			}
		}
	}
	limitID := strings.ToLower(strings.TrimSpace(headers.Get(codexActiveLimitHeader)))
	if limitID == "" && parsed.Error.Type == codexUsageLimitErrorType {
		limitID = codexImageGenLimitID
	}
	if limitID != "" {
		prefix := "x-" + strings.ReplaceAll(limitID, "_", "-")
		for _, name := range []string{prefix + "-primary-reset-at", prefix + "-secondary-reset-at"} {
			if ts, err := strconv.ParseInt(strings.TrimSpace(headers.Get(name)), 10, 64); err == nil && ts > 0 {
				if until := time.Unix(ts, 0); until.After(now) {
					if d := until.Sub(now); d > retryAfter {
						retryAfter = d
					}
				}
			}
		}
	}
	return retryAfter
}
