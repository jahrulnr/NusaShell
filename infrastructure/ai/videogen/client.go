// Package videogen implements the OpenRouter async video-generation API:
// POST /videos (submit) -> GET /videos/{id} (poll until terminal) ->
// GET /videos/{id}/content (download). Probe-verified 2026-08-23:
// alibaba/wan-2.6 rejected duration=1 under account guardrails while
// x-ai/grok-imagine-video accepted it and produced h264+aac mp4 in ~50s
// at $0.0495.
package videogen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nusashell/application"
)

const (
	// MaxVideoBytes caps the downloaded clip (100 MB — mirrors read_media).
	MaxVideoBytes = 100 << 20
	// pollInterval is the fixed delay between job-status checks. Doctrine:
	// no absolute wall-clock cap — polling ends on terminal status or ctx
	// cancellation only.
	pollInterval = 3 * time.Second
)

// Client talks to one OpenAI-compatible host serving the /videos API.
type Client struct {
	BaseURL string // e.g. https://openrouter.ai/api/v1
	APIKey  string
	HTTP    *http.Client
}

type submitRequest struct {
	Model         string       `json:"model"`
	Prompt        string       `json:"prompt"`
	Duration      int          `json:"duration,omitempty"`
	Resolution    string       `json:"resolution,omitempty"`
	AspectRatio   string       `json:"aspect_ratio,omitempty"`
	GenerateAudio *bool        `json:"generate_audio,omitempty"`
	FrameImages   []frameImage `json:"frame_images,omitempty"`
	InputRefs     []inputRef   `json:"input_references,omitempty"`
}

// frameImage is one entry in the OpenRouter frame_images array: an
// image_url part tagged with frame_type "first_frame" or "last_frame".
type frameImage struct {
	Type      string   `json:"type"` // always "image_url"
	ImageURL  imageURL `json:"image_url"`
	FrameType string   `json:"frame_type"` // "first_frame" | "last_frame"
}

type imageURL struct {
	URL string `json:"url"`
}

// inputRef is one entry in the OpenRouter input_references array, used
// for reference-to-video (style/identity guidance, not exact frames).
type inputRef struct {
	Type     string   `json:"type"` // "image_url"
	ImageURL imageURL `json:"image_url"`
}

func (c *Client) Generate(ctx context.Context, req application.VideoGenRequest) (*application.VideoGenResult, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("videogen: model is required")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("videogen: prompt is required")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	jobID, err := c.submit(ctx, httpClient, req)
	if err != nil {
		return nil, err
	}
	urls, cost, err := c.poll(ctx, httpClient, jobID)
	if err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("videogen: job %s completed but returned no content urls", jobID)
	}
	data, err := c.download(ctx, httpClient, urls[0])
	if err != nil {
		return nil, err
	}
	return &application.VideoGenResult{
		Video: data, MediaType: "video/mp4", Ext: "mp4",
		Provider: "openrouter-videos", Model: req.Model,
		JobID: jobID, CostUSD: cost,
	}, nil
}

func (c *Client) do(ctx context.Context, httpClient *http.Client, method, url string, body io.Reader, contentType string) ([]byte, int, error) {
	httpReq, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

// dataURL encodes image bytes as a base64 data URL for the OpenRouter
// frame_images/input_references fields. The API accepts both URLs and
// data URLs; data URLs avoid requiring a publicly hosted image.
func dataURL(mediaType string, data []byte) string {
	if mediaType == "" {
		mediaType = "image/png"
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// apiError converts a non-2xx JSON body ({"error":{"message":…}}) into a
// descriptive error, falling back to the raw body for non-JSON responses
// (guardrail/data-policy rejections arrive this way).
func apiError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	var errBody struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errBody) == nil && errBody.Error.Message != "" {
		msg = errBody.Error.Message
	}
	return fmt.Errorf("video endpoint returned HTTP %d: %s", status, msg)
}

func (c *Client) submit(ctx context.Context, httpClient *http.Client, req application.VideoGenRequest) (string, error) {
	body := submitRequest{
		Model: req.Model, Prompt: req.Prompt,
		Duration: req.DurationSec, Resolution: req.Resolution,
	}
	// Image-to-video: first reference becomes the first frame; any
	// additional references are sent as input_references for style/
	// identity guidance. OpenRouter auto-routes to the correct endpoint
	// based on which field is populated.
	if len(req.References) > 0 {
		first := req.References[0]
		body.FrameImages = []frameImage{{
			Type:      "image_url",
			ImageURL:  imageURL{URL: dataURL(first.MediaType, first.Data)},
			FrameType: "first_frame",
		}}
		if len(req.References) > 1 {
			body.InputRefs = make([]inputRef, 0, len(req.References)-1)
			for _, ref := range req.References[1:] {
				body.InputRefs = append(body.InputRefs, inputRef{
					Type:     "image_url",
					ImageURL: imageURL{URL: dataURL(ref.MediaType, ref.Data)},
				})
			}
		}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	data, status, err := c.do(ctx, httpClient, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/videos", bytes.NewReader(buf), "application/json")
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", apiError(status, data)
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("videogen: unexpected submit response: %.200s", data)
	}
	return out.ID, nil
}

type jobStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
	URLs  []string `json:"unsigned_urls"`
	Usage *struct {
		Cost float64 `json:"cost"`
	} `json:"usage"`
}

// poll blocks until the job reaches a terminal state. Only real upstream
// failures (failed/cancelled/error payload) or ctx cancellation end the
// wait early — pending jobs are never timed out by us.
func (c *Client) poll(ctx context.Context, httpClient *http.Client, jobID string) ([]string, float64, error) {
	url := strings.TrimRight(c.BaseURL, "/") + "/videos/" + jobID
	for {
		select {
		case <-ctx.Done():
			return nil, 0, fmt.Errorf("videogen: cancelled while waiting for job %s", jobID)
		case <-time.After(pollInterval):
		}
		data, status, err := c.do(ctx, httpClient, http.MethodGet, url, nil, "")
		if err != nil {
			return nil, 0, err
		}
		if status < 200 || status >= 300 {
			return nil, 0, apiError(status, data)
		}
		var js jobStatus
		if err := json.Unmarshal(data, &js); err != nil {
			return nil, 0, fmt.Errorf("videogen: unexpected poll response: %.200s", data)
		}
		switch strings.ToLower(js.Status) {
		case "completed":
			cost := 0.0
			if js.Usage != nil {
				cost = js.Usage.Cost
			}
			return js.URLs, cost, nil
		case "failed", "cancelled", "canceled":
			msg := js.Status
			if js.Error != nil && js.Error.Message != "" {
				msg = js.Error.Message
			}
			return nil, 0, fmt.Errorf("videogen: job %s %s: %s", jobID, js.Status, msg)
		default:
			// pending/in_progress/unknown — keep polling.
		}
	}
}

func (c *Client) download(ctx context.Context, httpClient *http.Client, url string) ([]byte, error) {
	data, status, err := c.do(ctx, httpClient, http.MethodGet, url, nil, "")
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, apiError(status, data)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("videogen: empty video download")
	}
	if len(data) > MaxVideoBytes {
		return nil, fmt.Errorf("videogen: video exceeds %d bytes (%d)", MaxVideoBytes, len(data))
	}
	return data, nil
}
