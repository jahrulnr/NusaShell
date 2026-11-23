// Package corespeech is the text-to-speech family: the /audio/speech
// surface OpenAI, OpenRouter, and compatible hosts share, and Gemini's
// generateContent with audio output. The surface follows the model's
// wire style.
package corespeech

import (
	"context"
	"encoding/base64"
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

// geminiVoice is the prebuilt voice a Gemini speech model reads with
// when the call names none — the surface requires one.
const geminiVoice = "Kore"

// openAIVoice is OpenAI's default for the same reason.
const openAIVoice = "alloy"

type Service struct{ c *coremedia.Client }

func New(c *coremedia.Client) *Service { return &Service{c: c} }

func (s *Service) Speak(ctx context.Context, prov *aggregate.Provider, model entity.Model, req dservice.SpeechRequest) (dservice.Media, error) {
	if strings.TrimSpace(req.Text) == "" {
		return dservice.Media{}, apperror.New(400, "speech needs text to read", nil)
	}
	limit := wire.MaxAttachmentBytes(enum.ModalityAudio)
	style := model.WireStyle(prov.Style())
	switch style {
	case enum.StyleMessages:
		return dservice.Media{}, apperror.New(400, "this provider's messages surface cannot make speech", nil)
	case enum.StyleGenerateContent:
		voice := req.Voice
		if voice == "" {
			voice = geminiVoice
		}
		text := req.Text
		if req.Instructions != "" {
			// The surface reads delivery guidance from the prompt itself.
			text = req.Instructions + ": " + req.Text
		}
		body, err := coremedia.JSON(map[string]any{
			"contents": []any{map[string]any{"role": "user", "parts": []coremedia.GeminiPart{{Text: text}}}},
			"generationConfig": map[string]any{
				"responseModalities": []string{"AUDIO"},
				"speechConfig": map[string]any{
					"voiceConfig": map[string]any{"prebuiltVoiceConfig": map[string]string{"voiceName": voice}},
				},
			},
		})
		if err != nil {
			return dservice.Media{}, err
		}
		resp, err := s.c.Do(ctx, prov, coremedia.Request{
			Method: http.MethodPost, Path: coremedia.GeminiURL(prov, model.ID(), "generateContent"),
			Body: body, ContentType: "application/json", Style: style, Limit: limit * 4 / 3, Log: "speech",
		})
		if err != nil {
			return dservice.Media{}, err
		}
		parts, said, err := coremedia.DecodeGemini(resp.Body)
		if err != nil {
			return dservice.Media{}, err
		}
		for _, p := range parts {
			if p.InlineData == nil || !strings.HasPrefix(p.InlineData.MIMEType, "audio/") {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
			if err != nil {
				return dservice.Media{}, apperror.New(502, "provider answered undecodable audio", err)
			}
			if coremedia.IsPCM(p.InlineData.MIMEType) {
				return dservice.Media{MediaType: "audio/wav", Data: coremedia.WAV(raw, coremedia.PCMRate(p.InlineData.MIMEType), 1)}, nil
			}
			return dservice.Media{MediaType: coremedia.MediaType(p.InlineData.MIMEType, raw), Data: raw}, nil
		}
		if said != "" {
			return dservice.Media{}, apperror.New(502, "the model answered with text instead of speech", nil)
		}
		return dservice.Media{}, apperror.New(502, "the provider answered no audio", nil)
	}

	voice := req.Voice
	if voice == "" && enum.OpenAI.MatchesEndpoint(prov.Endpoint()) {
		voice = openAIVoice
	}
	body, err := coremedia.JSON(struct {
		Model          string   `json:"model"`
		Input          string   `json:"input"`
		Voice          string   `json:"voice,omitempty"`
		Instructions   string   `json:"instructions,omitempty"`
		Speed          *float64 `json:"speed,omitempty"`
		ResponseFormat string   `json:"response_format"`
	}{model.ID(), req.Text, voice, req.Instructions, req.Speed, "mp3"})
	if err != nil {
		return dservice.Media{}, err
	}
	resp, err := s.c.Do(ctx, prov, coremedia.Request{
		Method: http.MethodPost, Path: "audio/speech", Body: body, ContentType: "application/json",
		Style: style, Limit: limit, Log: "speech",
	})
	if err != nil {
		return dservice.Media{}, err
	}
	declared := resp.Header.Get("Content-Type")
	if strings.Contains(declared, "json") {
		// An error dressed as success, or a host that wraps the audio.
		if err := coremedia.EnvelopeError(resp.Body); err != nil {
			return dservice.Media{}, err
		}
		return dservice.Media{}, apperror.New(502, "the provider answered no audio", nil)
	}
	if coremedia.IsPCM(declared) {
		return dservice.Media{MediaType: "audio/wav", Data: coremedia.WAV(resp.Body, coremedia.PCMRate(declared), 1)}, nil
	}
	if len(resp.Body) == 0 {
		return dservice.Media{}, apperror.New(502, "the provider answered no audio", nil)
	}
	mt := coremedia.MediaType(declared, resp.Body)
	if !strings.HasPrefix(mt, "audio/") {
		mt = "audio/mpeg" // asked for mp3; a bare octet-stream is that
	}
	return dservice.Media{MediaType: mt, Data: resp.Body}, nil
}
