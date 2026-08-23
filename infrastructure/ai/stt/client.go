package stt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"nusashell/application"
)

// Client posts audio to an OpenAI-compatible POST /audio/transcriptions
// endpoint (multipart/form-data) and returns the transcript text.
type Client struct {
	BaseURL string // e.g. https://api.openai.com/v1
	APIKey  string
	HTTP    *http.Client
}

func (c *Client) Transcribe(ctx context.Context, req application.STTRequest) (string, error) {
	if strings.TrimSpace(req.Model) == "" {
		return "", errors.New("stt: model is required")
	}
	if len(req.Data) == 0 {
		return "", errors.New("stt: audio data is empty")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	body := &strings.Builder{}
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("model", req.Model)
	if req.Language != "" {
		_ = mw.WriteField("language", req.Language)
	}
	if req.Prompt != "" {
		_ = mw.WriteField("prompt", req.Prompt)
	}
	part, err := mw.CreateFormFile("file", req.Filename)
	if err != nil {
		return "", fmt.Errorf("stt: form file: %w", err)
	}
	_, _ = part.Write(req.Data)
	_ = mw.Close()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+"/audio/transcriptions",
		strings.NewReader(body.String()))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
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
		return "", fmt.Errorf("transcription endpoint returned HTTP %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("stt: decode response: %w", err)
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		return "", errors.New("stt: empty transcript")
	}
	return text, nil
}
