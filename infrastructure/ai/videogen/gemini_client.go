package videogen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/pkg/httpclient"
)

// defaultGeminiPollInterval is the fixed delay between Veo operation
// status checks. Veo's documented latency is 11s min, up to 6 minutes
// during peak. Doctrine: no absolute wall-clock cap — polling ends on
// terminal status or ctx cancellation only.
const defaultGeminiPollInterval = 10 * time.Second

// GeminiClient generates videos through Google's Veo
// :predictLongRunning surface (submit → poll operation → download
// signed URI). It is the Gemini-kind video backend, mirroring the
// imagegen.BackendGemini routing decision: video model ids come from
// the shared GET /v1beta/models catalog + classification, not a
// dedicated media lister.
type GeminiClient struct {
	BaseURL string // normalized to end with /v1beta
	APIKey  string
	HTTP    *http.Client
	// PollInterval overrides the default 10s poll delay. Zero uses the
	// default. Exposed for tests; production callers leave it zero.
	PollInterval time.Duration
}

// geminiSubmitRequest is the :predictLongRunning body shape. Veo uses
// {"instances":[{...}],"parameters":{...}} — NOT the contents/parts
// shape used by :generateContent.
type geminiSubmitRequest struct {
	Instances  []geminiInstance  `json:"instances"`
	Parameters *geminiParameters `json:"parameters,omitempty"`
}

type geminiInstance struct {
	Prompt          string                 `json:"prompt"`
	Image           *geminiInlineData      `json:"image,omitempty"`
	ReferenceImages []geminiReferenceImage `json:"referenceImages,omitempty"`
}

type geminiInlineData struct {
	InlineData geminiBlob `json:"inlineData"`
}

type geminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64-encoded
}

type geminiReferenceImage struct {
	Image         geminiInlineData `json:"image"`
	ReferenceType string           `json:"referenceType"` // "asset"
}

type geminiParameters struct {
	DurationSeconds string `json:"durationSeconds,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
}

// geminiOperation is the long-running operation returned by submit
// and polled until done.
type geminiOperation struct {
	Name  string `json:"name"`
	Done  bool   `json:"done"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
	Response *struct {
		GenerateVideoResponse *struct {
			GeneratedSamples []struct {
				Video *struct {
					URI string `json:"uri"`
				} `json:"video"`
			} `json:"generatedSamples"`
		} `json:"generateVideoResponse,omitempty"`
	} `json:"response,omitempty"`
}

func (c *GeminiClient) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return httpclient.Shared()
}

func (c *GeminiClient) pollDelay() time.Duration {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return defaultGeminiPollInterval
}

func (c *GeminiClient) Generate(ctx context.Context, req application.VideoGenRequest) (*application.VideoGenResult, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("videogen: model is required")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("videogen: prompt is required")
	}
	operationName, err := c.submit(ctx, req)
	if err != nil {
		return nil, err
	}
	videoURI, err := c.poll(ctx, operationName)
	if err != nil {
		return nil, err
	}
	data, err := c.download(ctx, videoURI)
	if err != nil {
		return nil, err
	}
	return &application.VideoGenResult{
		Video: data, MediaType: "video/mp4", Ext: "mp4",
		Provider: "gemini-veo", Model: req.Model,
		JobID: operationName,
	}, nil
}

func (c *GeminiClient) submit(ctx context.Context, req application.VideoGenRequest) (string, error) {
	instance := geminiInstance{Prompt: req.Prompt}
	if len(req.References) > 0 {
		first := req.References[0]
		media := strings.TrimSpace(first.MediaType)
		if media == "" {
			media = "image/png"
		}
		instance.Image = &geminiInlineData{
			InlineData: geminiBlob{
				MimeType: media,
				Data:     base64.StdEncoding.EncodeToString(first.Data),
			},
		}
		if len(req.References) > 1 {
			instance.ReferenceImages = make([]geminiReferenceImage, 0, len(req.References)-1)
			for _, ref := range req.References[1:] {
				refMedia := strings.TrimSpace(ref.MediaType)
				if refMedia == "" {
					refMedia = "image/png"
				}
				instance.ReferenceImages = append(instance.ReferenceImages, geminiReferenceImage{
					Image: geminiInlineData{
						InlineData: geminiBlob{
							MimeType: refMedia,
							Data:     base64.StdEncoding.EncodeToString(ref.Data),
						},
					},
					ReferenceType: "asset",
				})
			}
		}
	}
	params := &geminiParameters{}
	if req.DurationSec > 0 {
		params.DurationSeconds = strconv.Itoa(req.DurationSec)
	}
	// Veo supports 720p / 1080p / 4k. 480p has no Veo equivalent — omit
	// it so Veo defaults to 720p rather than rejecting the request.
	switch req.Resolution {
	case "720p", "1080p", "4k":
		params.Resolution = req.Resolution
	}
	body := geminiSubmitRequest{
		Instances:  []geminiInstance{instance},
		Parameters: params,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	model := normalizeGeminiVideoModelID(req.Model)
	url := strings.TrimRight(c.BaseURL, "/") + "/models/" + model + ":predictLongRunning"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.APIKey)

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return "", &domain.ProviderError{Kind: domain.KindConnect, Temporary: true, Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", mapGeminiVideoError(resp.StatusCode, raw)
	}
	var op geminiOperation
	if err := json.Unmarshal(raw, &op); err != nil || op.Name == "" {
		return "", fmt.Errorf("videogen: unexpected submit response: %.200s", raw)
	}
	return op.Name, nil
}

func (c *GeminiClient) poll(ctx context.Context, operationName string) (string, error) {
	url := strings.TrimRight(c.BaseURL, "/") + "/" + operationName
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("videogen: cancelled while waiting for Veo operation %s", operationName)
		case <-time.After(c.pollDelay()):
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		httpReq.Header.Set("x-goog-api-key", c.APIKey)
		resp, err := c.httpClient().Do(httpReq)
		if err != nil {
			return "", &domain.ProviderError{Kind: domain.KindConnect, Temporary: true, Err: err}
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		if resp.StatusCode >= 400 {
			return "", mapGeminiVideoError(resp.StatusCode, raw)
		}
		var op geminiOperation
		if err := json.Unmarshal(raw, &op); err != nil {
			return "", fmt.Errorf("videogen: unexpected poll response: %.200s", raw)
		}
		if !op.Done {
			continue
		}
		if op.Error != nil && strings.TrimSpace(op.Error.Message) != "" {
			return "", &domain.ProviderError{
				StatusCode: op.Error.Code,
				Err:        fmt.Errorf("%s", strings.TrimSpace(op.Error.Message)),
			}
		}
		if op.Response == nil || op.Response.GenerateVideoResponse == nil ||
			len(op.Response.GenerateVideoResponse.GeneratedSamples) == 0 ||
			op.Response.GenerateVideoResponse.GeneratedSamples[0].Video == nil ||
			op.Response.GenerateVideoResponse.GeneratedSamples[0].Video.URI == "" {
			return "", fmt.Errorf("videogen: Veo operation %s completed but returned no video", operationName)
		}
		return op.Response.GenerateVideoResponse.GeneratedSamples[0].Video.URI, nil
	}
}

func (c *GeminiClient) download(ctx context.Context, videoURI string) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURI, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-goog-api-key", c.APIKey)
	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, &domain.ProviderError{Kind: domain.KindConnect, Temporary: true, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, mapGeminiVideoError(resp.StatusCode, raw)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxVideoBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("videogen: empty video download")
	}
	if len(data) > MaxVideoBytes {
		return nil, fmt.Errorf("videogen: video exceeds %d bytes (%d)", MaxVideoBytes, len(data))
	}
	return data, nil
}

// normalizeGeminiVideoModelID strips "models/" and "<provider>/" prefixes
// so the operation path carries only the bare model id the API expects.
func normalizeGeminiVideoModelID(model string) string {
	name := strings.TrimSpace(model)
	segments := strings.Split(name, "/")
	if len(segments) > 1 {
		name = segments[len(segments)-1]
	}
	return name
}

// mapGeminiVideoError converts a non-2xx Veo response into a
// domain.ProviderError. 4xx are hard failures (validation/auth/billing);
// 5xx stay retriable for the caller's shared policy. Provider minimums
// (e.g. "durationSeconds must be 8 for 1080p") are surfaced verbatim.
func mapGeminiVideoError(status int, raw []byte) error {
	var parsed struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &parsed)
	detail := strings.TrimSpace(parsed.Error.Message)
	if detail == "" {
		detail = strings.TrimSpace(string(raw))
		if len(detail) > 800 {
			detail = detail[:800]
		}
	}
	if status >= 500 {
		return &domain.ProviderError{
			Kind:       domain.KindHTTPStatus,
			StatusCode: status,
			Temporary:  true,
			Err:        fmt.Errorf("veo video generation failed (HTTP %d): %s", status, detail),
		}
	}
	return &domain.ProviderError{
		StatusCode: status,
		Err:        fmt.Errorf("veo video generation failed (HTTP %d): %s", status, detail),
	}
}
