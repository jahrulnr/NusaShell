// Package coreimage is the image-generation family. One service speaks
// every surface a provider may expose for it: OpenRouter's /images, the
// OpenAI-shaped /images/generations and /images/edits the compatible
// long tail serves, and Gemini's generateContent with image output. The
// surface follows the model's wire style; OpenRouter's own endpoint is
// keyed off its host.
package coreimage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	coremedia "github.com/jahrulnr/nusashell/infrastructure/service/core/media"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

// maxCount bounds images per call — every surface caps n near here.
const maxCount = 4

// bodyLimit holds maxCount images at the image attachment cap, base64'd.
var bodyLimit = int64(maxCount) * wire.MaxAttachmentBytes(enum.ModalityImage) * 4 / 3

type Service struct{ c *coremedia.Client }

func New(c *coremedia.Client) *Service { return &Service{c: c} }

func (s *Service) GenerateImage(ctx context.Context, prov *aggregate.Provider, model entity.Model, req dservice.ImageRequest) ([]dservice.Media, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, apperror.New(400, "an image needs a prompt", nil)
	}
	count := min(max(req.Count, 1), maxCount)
	style := model.WireStyle(prov.Style())
	switch {
	case style == enum.StyleGenerateContent:
		return s.generateContent(ctx, prov, model, req, count)
	case style == enum.StyleMessages:
		return nil, apperror.New(400, "this provider's messages surface cannot make images", nil)
	}

	var body []byte
	var contentType, path string
	var err error
	switch {
	case enum.OpenRouter.MatchesEndpoint(prov.Endpoint()):
		// One endpoint does both: references turn it into an edit.
		type imageURL struct {
			URL string `json:"url"`
		}
		type ref struct {
			Type     string   `json:"type"`
			ImageURL imageURL `json:"image_url"`
		}
		refs := make([]ref, 0, len(req.References))
		for _, r := range req.References {
			refs = append(refs, ref{Type: "image_url", ImageURL: imageURL{URL: wire.DataURI(r.MediaType, r.Data)}})
		}
		body, err = coremedia.JSON(struct {
			Model          string `json:"model"`
			Prompt         string `json:"prompt"`
			N              int    `json:"n,omitempty"`
			AspectRatio    string `json:"aspect_ratio,omitempty"`
			Size           string `json:"size,omitempty"`
			References     []ref  `json:"input_references,omitempty"`
			ResponseFormat string `json:"response_format"`
		}{model.ID(), req.Prompt, count, req.AspectRatio, req.Size, refs, "b64_json"})
		contentType, path = "application/json", "images"
	case len(req.References) > 0:
		// The OpenAI-shaped edit surface takes its sources as files.
		form := coremedia.NewForm()
		form.Field("model", model.ID())
		form.Field("prompt", req.Prompt)
		form.Field("n", strconv.Itoa(count))
		form.Field("size", openAISize(prov, model, req))
		field := "image"
		if len(req.References) > 1 {
			field = "image[]"
		}
		for i, r := range req.References {
			form.File(field, fmt.Sprintf("reference-%d%s", i+1, ext(r.MediaType)), r.MediaType, r.Data)
		}
		body, contentType, err = form.Close()
		path = "images/edits"
	default:
		body, err = coremedia.JSON(struct {
			Model       string `json:"model"`
			Prompt      string `json:"prompt"`
			N           int    `json:"n,omitempty"`
			Size        string `json:"size,omitempty"`
			AspectRatio string `json:"aspect_ratio,omitempty"`
		}{model.ID(), req.Prompt, count, openAISize(prov, model, req), openAIAspect(prov, req)})
		contentType, path = "application/json", "images/generations"
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.c.Do(ctx, prov, coremedia.Request{
		Method: http.MethodPost, Path: path, Body: body, ContentType: contentType,
		Style: style, Limit: bodyLimit, Log: "image",
	})
	if err != nil {
		return nil, err
	}
	if err := coremedia.EnvelopeError(resp.Body); err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			B64JSON   string `json:"b64_json"`
			URL       string `json:"url"`
			MediaType string `json:"media_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, apperror.New(502, "malformed upstream body", err)
	}
	media := make([]dservice.Media, 0, len(out.Data))
	for _, d := range out.Data {
		var raw []byte
		switch {
		case d.B64JSON != "":
			// Tolerate a data: URI where raw base64 was promised.
			b64 := d.B64JSON
			if _, after, ok := strings.Cut(b64, ";base64,"); ok {
				b64 = after
			}
			raw, err = base64.StdEncoding.DecodeString(b64)
			if err != nil {
				return nil, apperror.New(502, "provider answered undecodable image data", err)
			}
		case d.URL != "":
			got, err := s.c.Download(ctx, prov, style, d.URL, wire.MaxAttachmentBytes(enum.ModalityImage))
			if err != nil {
				return nil, err
			}
			raw = got.Body
			if d.MediaType == "" {
				d.MediaType = got.Header.Get("Content-Type")
			}
		default:
			continue
		}
		media = append(media, dservice.Media{MediaType: coremedia.MediaType(d.MediaType, raw), Data: raw})
	}
	if len(media) == 0 {
		return nil, apperror.New(502, "the provider answered no image", nil)
	}
	return media, nil
}

// generateContent asks a Gemini model for image output: the prompt and
// references form one user turn, and every inline image part of the
// answer is a result. The surface makes one image per call.
func (s *Service) generateContent(ctx context.Context, prov *aggregate.Provider, model entity.Model, req dservice.ImageRequest, count int) ([]dservice.Media, error) {
	parts := []coremedia.GeminiPart{{Text: req.Prompt}}
	for _, r := range req.References {
		parts = append(parts, coremedia.GeminiPart{InlineData: &coremedia.GeminiBlob{
			MIMEType: r.MediaType, Data: base64.StdEncoding.EncodeToString(r.Data),
		}})
	}
	type imageConfig struct {
		AspectRatio string `json:"aspectRatio,omitempty"`
		ImageSize   string `json:"imageSize,omitempty"`
	}
	gen := map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}}
	if req.AspectRatio != "" || req.Size != "" {
		gen["imageConfig"] = imageConfig{AspectRatio: req.AspectRatio, ImageSize: req.Size}
	}
	body, err := coremedia.JSON(map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": parts}},
		"generationConfig": gen,
	})
	if err != nil {
		return nil, err
	}
	var media []dservice.Media
	var said string
	for range count {
		resp, err := s.c.Do(ctx, prov, coremedia.Request{
			Method: http.MethodPost, Path: coremedia.GeminiURL(prov, model.ID(), "generateContent"),
			Body: body, ContentType: "application/json", Style: enum.StyleGenerateContent,
			Limit: bodyLimit, Log: "image",
		})
		if err != nil {
			return nil, err
		}
		got, text, err := coremedia.DecodeGemini(resp.Body)
		if err != nil {
			return nil, err
		}
		said = text
		for _, p := range got {
			if p.InlineData == nil || !strings.HasPrefix(p.InlineData.MIMEType, "image/") {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
			if err != nil {
				return nil, apperror.New(502, "provider answered undecodable image data", err)
			}
			media = append(media, dservice.Media{MediaType: coremedia.MediaType(p.InlineData.MIMEType, raw), Data: raw})
		}
	}
	if len(media) == 0 {
		if said != "" {
			return nil, apperror.New(502, "the model answered with text instead of an image: "+clip(said, 300), nil)
		}
		return nil, apperror.New(502, "the provider answered no image", nil)
	}
	return media, nil
}

// openAISize turns an aspect ratio into the pixel size OpenAI's own image
// models take when no size was given; other hosts receive the ratio as is.
func openAISize(prov *aggregate.Provider, model entity.Model, req dservice.ImageRequest) string {
	if req.Size != "" || req.AspectRatio == "" || !enum.OpenAI.MatchesEndpoint(prov.Endpoint()) {
		return req.Size
	}
	w, h, ok := ratio(req.AspectRatio)
	if !ok {
		return ""
	}
	wide, tall := "1536x1024", "1024x1536"
	if strings.Contains(model.ID(), "dall-e-3") {
		wide, tall = "1792x1024", "1024x1792"
	}
	switch {
	case w > h:
		return wide
	case w < h:
		return tall
	}
	return "1024x1024"
}

// openAIAspect forwards the ratio to compatible hosts that take one;
// OpenAI itself rejects the field, so the ratio went into the size.
func openAIAspect(prov *aggregate.Provider, req dservice.ImageRequest) string {
	if enum.OpenAI.MatchesEndpoint(prov.Endpoint()) {
		return ""
	}
	return req.AspectRatio
}

func ratio(s string) (w, h float64, ok bool) {
	a, b, found := strings.Cut(s, ":")
	if !found {
		return 0, 0, false
	}
	w, errW := strconv.ParseFloat(strings.TrimSpace(a), 64)
	h, errH := strconv.ParseFloat(strings.TrimSpace(b), 64)
	return w, h, errW == nil && errH == nil && w > 0 && h > 0
}

func ext(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	return ".png"
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
