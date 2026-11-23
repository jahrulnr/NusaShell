// Package coreembedding is the embedding family: the /embeddings surface
// OpenAI, OpenRouter, and compatible hosts share, and Gemini's
// batchEmbedContents. The surface follows the model's wire style.
package coreembedding

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	coremedia "github.com/jahrulnr/nusashell/infrastructure/service/core/media"
)

// maxBatch bounds texts per request — every surface caps a batch near here.
const maxBatch = 96

type Service struct{ c *coremedia.Client }

func New(c *coremedia.Client) *Service { return &Service{c: c} }

func (s *Service) Embed(ctx context.Context, prov *aggregate.Provider, model entity.Model, texts []string, purpose dservice.EmbedPurpose) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += maxBatch {
		batch := texts[start:min(start+maxBatch, len(texts))]
		vecs, err := s.embed(ctx, prov, model, batch, purpose)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (s *Service) embed(ctx context.Context, prov *aggregate.Provider, model entity.Model, texts []string, purpose dservice.EmbedPurpose) ([][]float32, error) {
	style := model.WireStyle(prov.Style())
	switch style {
	case enum.StyleMessages:
		return nil, apperror.New(400, "this provider's messages surface cannot embed text", nil)
	case enum.StyleGenerateContent:
		task := "RETRIEVAL_DOCUMENT"
		if purpose == dservice.EmbedQuery {
			task = "RETRIEVAL_QUERY"
		}
		id := model.ID()
		if i := strings.LastIndex(id, "/"); i >= 0 {
			id = id[i+1:]
		}
		reqs := make([]map[string]any, len(texts))
		for i, t := range texts {
			reqs[i] = map[string]any{
				"model":    "models/" + id,
				"content":  map[string]any{"parts": []map[string]string{{"text": t}}},
				"taskType": task,
			}
		}
		body, err := coremedia.JSON(map[string]any{"requests": reqs})
		if err != nil {
			return nil, err
		}
		resp, err := s.c.Do(ctx, prov, coremedia.Request{
			Method: http.MethodPost, Path: coremedia.GeminiURL(prov, model.ID(), "batchEmbedContents"),
			Body: body, ContentType: "application/json", Style: style, Log: "embedding",
		})
		if err != nil {
			return nil, err
		}
		var got struct {
			Embeddings []struct {
				Values []float32 `json:"values"`
			} `json:"embeddings"`
		}
		if err := json.Unmarshal(resp.Body, &got); err != nil {
			return nil, apperror.New(502, "malformed upstream body", err)
		}
		if len(got.Embeddings) != len(texts) {
			return nil, apperror.New(502, "the provider answered a different number of embeddings", nil)
		}
		out := make([][]float32, len(texts))
		for i, e := range got.Embeddings {
			if len(e.Values) == 0 {
				return nil, apperror.New(502, "the provider answered an empty embedding", nil)
			}
			out[i] = e.Values
		}
		return out, nil
	}

	inputType := ""
	if enum.OpenRouter.MatchesEndpoint(prov.Endpoint()) {
		// Retrieval models embed queries and documents apart; OpenAI
		// itself rejects the field.
		inputType = "search_document"
		if purpose == dservice.EmbedQuery {
			inputType = "search_query"
		}
	}
	body, err := coremedia.JSON(struct {
		Model          string   `json:"model"`
		Input          []string `json:"input"`
		EncodingFormat string   `json:"encoding_format"`
		InputType      string   `json:"input_type,omitempty"`
	}{model.ID(), texts, "float", inputType})
	if err != nil {
		return nil, err
	}
	resp, err := s.c.Do(ctx, prov, coremedia.Request{
		Method: http.MethodPost, Path: "embeddings", Body: body, ContentType: "application/json",
		Style: style, Log: "embedding",
	})
	if err != nil {
		return nil, err
	}
	if err := coremedia.EnvelopeError(resp.Body); err != nil {
		return nil, err
	}
	var got struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		return nil, apperror.New(502, "malformed upstream body", err)
	}
	if len(got.Data) != len(texts) {
		return nil, apperror.New(502, "the provider answered a different number of embeddings", nil)
	}
	out := make([][]float32, len(texts))
	for i, d := range got.Data {
		at := d.Index
		if at < 0 || at >= len(texts) || out[at] != nil {
			at = i // a host that leaves out index keeps input order
		}
		if len(d.Embedding) == 0 {
			return nil, apperror.New(502, "the provider answered an empty embedding", nil)
		}
		out[at] = d.Embedding
	}
	for _, v := range out {
		if v == nil {
			return nil, apperror.New(502, "the provider answered a different number of embeddings", nil)
		}
	}
	return out, nil
}
