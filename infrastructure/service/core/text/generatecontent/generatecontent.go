// Package textgeneratecontent is the Gemini generateContent adapter. The
// wire is role-alternated contents of typed parts: thoughtSignature fields
// on model parts must replay verbatim (they bind to the exact bytes),
// functionCall/functionResponse pair on provider call ids or names, media
// ride inline as inlineData, and usage counters are inclusive —
// promptTokenCount already contains the cached share.
package textgeneratecontent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	coretext "github.com/jahrulnr/nusashell/infrastructure/service/core/text"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

// Service is the stateless translator for this wire — endpoint,
// credentials, and stream preference are read from the provider argument
// of every Send, so one instance serves every generateContent-style
// provider.
type Service struct {
	client    *http.Client
	log       drepo.Logger
	referer   string
	title     string
	fileRoot  string
	fileStore drepo.Workspace
	idle      time.Duration
}

// New builds the service — Options only; providers arrive per Send.
func New(opts coretext.Options) *Service {
	svcCfg := config.ServiceConfig{
		HeaderTimeout: config.Duration(opts.HeaderTimeout),
		IdleTimeout:   config.Duration(opts.IdleTimeout),
	}
	svcCfg.Normalize()
	client := opts.Client
	if client == nil {
		client = &http.Client{Transport: &http.Transport{
			ResponseHeaderTimeout: time.Duration(svcCfg.HeaderTimeout),
		}}
	}
	return &Service{
		client:    client,
		log:       opts.Logger,
		referer:   opts.Referer,
		title:     opts.Title,
		fileRoot:  opts.FileRoot,
		fileStore: opts.FileStore,
		idle:      time.Duration(svcCfg.IdleTimeout),
	}
}

// Send performs one generateContent attempt — request marshal, POST,
// decode, then append the assistant message and usage onto conv. Stream
// mode selects :streamGenerateContent?alt=sse; events reach onEvent
// uniformly either way.
func (s *Service) Send(ctx context.Context, conv *aggregate.Conversation, prov *aggregate.Provider, model entity.Model, onEvent event.TurnEventHandler) error {
	stream := prov.StreamMode()
	body, err := encodeRequest(conv, model, wire.MediaRoot{Dir: coretext.FileRoot(conv, s.fileRoot), Store: s.fileStore})
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	s.logAppend(ctx, "provider.request", json.RawMessage(raw))

	// prov.Endpoint() is the API base (e.g.
	// https://generativelanguage.googleapis.com/v1beta); the model id
	// lives in the path, prefixed ids ("google/gemini-x", "models/x")
	// normalize to the bare id.
	// The id becomes one path segment — escape it so a query or fragment
	// delimiter inside the id ("gemini?x") can't rewrite the URL's shape
	// and address a different model.
	u := strings.TrimRight(prov.Endpoint(), "/") + "/models/" + url.PathEscape(modelPath(model.ID()))
	if stream {
		u += ":streamGenerateContent?alt=sse"
	} else {
		u += ":generateContent"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	// x-goog-api-key is canonical on this surface — Bearer often fails.
	req.Header.Set("x-goog-api-key", prov.ApiKey())
	req.Header.Set("Content-Type", "application/json")
	if s.referer != "" {
		req.Header.Set("HTTP-Referer", s.referer)
	}
	if s.title != "" {
		req.Header.Set("X-Title", s.title)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil { // caller gave up — not an upstream failure
			return apperror.New(408, "request canceled or timed out", ctx.Err())
		}
		return apperror.New(502, "upstream unreachable", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := wire.ReadBody(ctx, resp.Body, s.idle)
		s.logAppend(ctx, "provider.error", map[string]any{
			"status": resp.StatusCode, "request_id": resp.Header.Get("x-request-id"), "body": json.RawMessage(raw),
		})
		return wire.WithRetryAfter(statusError(resp.StatusCode, raw), wire.RetryAfter(resp.Header.Get("Retry-After")))
	}

	if stream {
		return s.streamResponse(ctx, resp, conv, onEvent)
	}
	return s.unaryResponse(ctx, resp, conv, onEvent)
}

// modelPath strips provider prefixes from the model id — native ids are
// bare ("gemini-3.6-flash"), routed ones carry "google/" or "models/".
func modelPath(id string) string {
	id = strings.TrimPrefix(id, "models/")
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:] // google/gemini-x → gemini-x (Vertex keeps its own path)
	}
	return id
}

// unaryResponse decodes the full body, appends the message, and emits the
// synthetic delta+finished pair the port promises non-stream callers.
func (s *Service) unaryResponse(ctx context.Context, resp *http.Response, conv *aggregate.Conversation, onEvent event.TurnEventHandler) error {
	raw, err := wire.ReadBody(ctx, resp.Body, s.idle)
	if err != nil {
		return err
	}
	s.logAppend(ctx, "provider.response", map[string]any{
		"request_id": resp.Header.Get("x-request-id"),
		"body":       json.RawMessage(raw),
	})
	var rb ResponseBody
	if err := json.Unmarshal(raw, &rb); err != nil {
		return apperror.New(502, "malformed upstream body", err)
	}
	if rb.Error != nil {
		return wire.WithRetryAfter(streamError(rb.Error, raw), wire.RetryAfter(resp.Header.Get("Retry-After")))
	}
	if len(rb.Candidates) == 0 {
		if rb.PromptFeedback != nil && rb.PromptFeedback.BlockReason != "" {
			return apperror.New(400, "prompt blocked: "+rb.PromptFeedback.BlockReason, wire.UpstreamError(raw))
		}
		return apperror.New(502, "upstream returned no candidates", wire.UpstreamError(raw))
	}
	// A complete JSON body is this wire's settled terminal — a candidate
	// without finishReason still committed, so the verdict is stop.
	finish := rb.Candidates[0].FinishReason
	if finish == "" {
		finish = "STOP"
	}
	msg, meta, err := decodeParts(rb.Candidates[0].Content.Parts, finish)
	if err != nil {
		return err
	}
	blocked := ""
	if rb.PromptFeedback != nil {
		blocked = rb.PromptFeedback.BlockReason
	}
	if err := coretext.EmptyAnswerError(msg, meta.artifacts, blocked, []json.RawMessage{raw}); err != nil {
		return err
	}
	usage, err := decodeUsage(rb.Usage)
	if err != nil {
		return err
	}
	conv.Append(msg)
	conv.AddUsage(usage)
	if meta.reasoning != "" {
		emit(onEvent, event.NewReasoningEvent(meta.reasoning))
	}
	emit(onEvent, event.NewDeltaEvent(msg.Content()))
	for _, tc := range msg.ToolCalls() {
		emit(onEvent, event.NewToolCallEvent(tc))
	}
	emit(onEvent, event.NewFinishedEvent(msg.FinishReason(), usage))
	return nil
}

func emit(h event.TurnEventHandler, e event.TurnEvent) {
	if h != nil {
		h(e)
	}
}

func (s *Service) logAppend(ctx context.Context, kind string, v any) {
	if s.log != nil {
		_ = s.log.Append(ctx, kind, v) // diagnostics must never break a send
	}
}
