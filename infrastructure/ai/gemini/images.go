package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"nusashell/application"
	aiutil "nusashell/infrastructure/ai/internal"
)

// ImagesClient implements application.ImageGenerator for Gemini image models
// (gemini-3.1-flash-image-preview, gemini-3-pro-image-preview, etc).
//
// Unlike OpenAI/Codex image APIs, Gemini image models use the regular
// generateContent endpoint — the model returns image bytes as inlineData
// parts in its response. There is no separate /images/generations path.
//
// Reference: https://ai.google.dev/gemini-api/docs/generate-content/image-generation
type ImagesClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func (c *ImagesClient) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *ImagesClient) headers() map[string]string {
	h := map[string]string{}
	if c.APIKey != "" {
		h["x-goog-api-key"] = c.APIKey
	}
	return h
}

func (c *ImagesClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Generate calls generateContent with the prompt (and optional reference
// images as inlineData parts) and extracts image parts from the response.
func (c *ImagesClient) Generate(ctx context.Context, req application.ImageGenRequest) (*application.ImageGenResult, error) {
	if req.Prompt == "" {
		return nil, fmt.Errorf("gemini: prompt is required")
	}
	body := c.buildImageRequest(req)
	var resp generateContentResponse
	url := c.baseURL() + "/models/" + req.Model + ":generateContent"
	if err := aiutil.DoJSON(ctx, c.httpClient(), "POST", url, c.headers(), body, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("gemini: %s", resp.Error.Message)
	}
	images := extractImages(resp.Candidates)
	if len(images) == 0 {
		return nil, fmt.Errorf("gemini: no image data in response")
	}
	result := &application.ImageGenResult{
		Images:   images,
		Provider: "gemini",
		Model:    req.Model,
	}
	if resp.UsageMetadata.TotalTokenCount > 0 {
		result.UsageTokens = resp.UsageMetadata.TotalTokenCount
	}
	return result, nil
}

func (c *ImagesClient) buildImageRequest(req application.ImageGenRequest) *generateContentRequest {
	parts := []Part{{Text: req.Prompt}}
	for _, ref := range req.References {
		mt := ref.MediaType
		if mt == "" {
			mt = "image/png"
		}
		parts = append(parts, Part{
			InlineData: &Blob{
				MimeType: mt,
				Data:     base64.StdEncoding.EncodeToString(ref.Data),
			},
		})
	}
	return &generateContentRequest{
		Contents: []Content{{Role: "user", Parts: parts}},
		GenerationConfig: &GenerationConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
		},
	}
}

func extractImages(cands []ResponseCandidate) []application.GeneratedImage {
	var out []application.GeneratedImage
	for _, cand := range cands {
		for _, part := range cand.Content.Parts {
			if part.InlineData != nil && part.InlineData.Data != "" {
				data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
				if err != nil {
					continue
				}
				mt := part.InlineData.MimeType
				if mt == "" {
					mt = "image/png"
				}
				out = append(out, application.GeneratedImage{Bytes: data, MediaType: mt})
			}
		}
	}
	return out
}

var _ = strings.TrimSpace
