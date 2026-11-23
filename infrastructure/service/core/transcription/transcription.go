// Package coretranscription is the speech-to-text family: the
// multipart /audio/transcriptions surface OpenAI, OpenRouter, and
// compatible hosts share, and Gemini's generateContent reading audio.
// The surface follows the model's wire style.
package coretranscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	coremedia "github.com/jahrulnr/nusashell/infrastructure/service/core/media"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

// geminiInstruction asks a generateContent model for a transcript only.
const geminiInstruction = "Transcribe the speech in this audio verbatim, in the language spoken. Answer with the transcript only — no commentary, labels, or timestamps."

type Service struct{ c *coremedia.Client }

func New(c *coremedia.Client) *Service { return &Service{c: c} }

func (s *Service) Transcribe(ctx context.Context, prov *aggregate.Provider, model entity.Model, req dservice.TranscriptionRequest) (string, error) {
	if len(req.Audio.Data) == 0 {
		return "", apperror.New(400, "transcription needs audio", nil)
	}
	style := model.WireStyle(prov.Style())
	switch style {
	case enum.StyleMessages:
		return "", apperror.New(400, "this provider's messages surface cannot transcribe audio", nil)
	case enum.StyleGenerateContent:
		instruction := geminiInstruction
		if req.Language != "" {
			instruction += " The language is " + req.Language + "."
		}
		if req.Prompt != "" {
			instruction += " Context: " + req.Prompt
		}
		body, err := coremedia.JSON(map[string]any{
			"contents": []any{map[string]any{"role": "user", "parts": []coremedia.GeminiPart{
				{Text: instruction},
				{InlineData: &coremedia.GeminiBlob{MIMEType: req.Audio.MediaType, Data: base64.StdEncoding.EncodeToString(req.Audio.Data)}},
			}}},
		})
		if err != nil {
			return "", err
		}
		resp, err := s.c.Do(ctx, prov, coremedia.Request{
			Method: http.MethodPost, Path: coremedia.GeminiURL(prov, model.ID(), "generateContent"),
			Body: body, ContentType: "application/json", Style: style, Log: "transcription",
		})
		if err != nil {
			return "", err
		}
		_, text, err := coremedia.DecodeGemini(resp.Body)
		if err != nil {
			return "", err
		}
		return text, nil
	}

	form := coremedia.NewForm()
	form.Field("model", model.ID())
	form.Field("language", req.Language)
	form.Field("prompt", req.Prompt)
	form.Field("response_format", "json")
	form.File("file", "audio."+audioExt(req.Audio.MediaType), req.Audio.MediaType, req.Audio.Data)
	body, contentType, err := form.Close()
	if err != nil {
		return "", err
	}
	resp, err := s.c.Do(ctx, prov, coremedia.Request{
		Method: http.MethodPost, Path: "audio/transcriptions", Body: body, ContentType: contentType,
		Style: style, Log: "transcription",
	})
	if err != nil {
		return "", err
	}
	if err := coremedia.EnvelopeError(resp.Body); err != nil {
		return "", err
	}
	var out struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal(resp.Body, &out) != nil || out.Text == nil {
		// A host that ignores response_format answers plain text.
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
			return strings.TrimSpace(string(resp.Body)), nil
		}
		return "", apperror.New(502, "malformed upstream body", nil)
	}
	return strings.TrimSpace(*out.Text), nil
}

// audioExt names the upload for its format — the OpenAI-shaped surface
// decides the decoder from the filename's extension.
func audioExt(mediaType string) string {
	t, _, _ := strings.Cut(mediaType, ";")
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "audio/x-m4a", "audio/m4a", "audio/mp4", "video/mp4":
		return "m4a"
	case "audio/x-flac", "audio/flac":
		return "flac"
	case "audio/webm", "video/webm":
		return "webm"
	case "audio/opus":
		return "ogg"
	}
	return wire.FormatName(t)
}
