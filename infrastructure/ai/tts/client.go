package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nusashell/application"
)

// Client posts text to an OpenAI-compatible POST /audio/speech endpoint and
// returns raw audio bytes (mp3/wav/opus as requested).
type Client struct {
	BaseURL string // e.g. https://api.openai.com/v1
	APIKey  string
	HTTP    *http.Client
}

type speechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          float64 `json:"speed,omitempty"`
}

func (c *Client) Synthesize(ctx context.Context, req application.TTSRequest) (*application.TTSResult, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("tts: model is required")
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("tts: text is required")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	body := speechRequest{
		Model: req.Model, Input: req.Text, Voice: req.Voice,
		ResponseFormat: req.Format, Speed: req.Speed,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+"/audio/speech", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := string(data)
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &errBody) == nil && errBody.Error.Message != "" {
			msg = errBody.Error.Message
		}
		return nil, fmt.Errorf("speech endpoint returned HTTP %d: %s", resp.StatusCode, msg)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("tts: empty audio response")
	}

	format := strings.TrimSpace(req.Format)
	if format == "" {
		format = "mp3"
	}
	mediaType := map[string]string{"mp3": "audio/mpeg", "wav": "audio/wav", "opus": "audio/ogg"}[format]
	voice := strings.TrimSpace(req.Voice)
	if voice == "" {
		voice = "alloy"
	}
	return &application.TTSResult{
		Audio: data, MediaType: mediaType, Ext: format,
		Provider: "openai-compatible", Model: req.Model, Voice: voice,
	}, nil
}
