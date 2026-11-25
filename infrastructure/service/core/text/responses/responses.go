// Package textresponses is the OpenAI /v1/responses adapter. The wire is
// typed output items end to end: reasoning blobs and server-tool items
// replay verbatim (encrypted reasoning is bound to the exact bytes),
// function_call/function_call_output pair on call_id, and usage counters
// are inclusive — input_tokens already contains the cached share.
package textresponses

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	coretext "github.com/jahrulnr/nusashell/infrastructure/service/core/text"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

// Service is the stateless translator for this wire — endpoint,
// credentials, and stream preference are read from the provider argument
// of every Send, so one instance serves every responses-style provider.
type Service struct {
	client    *http.Client
	log       drepo.Logger
	referer   string
	title     string
	fileRoot  string
	fileStore drepo.Workspace
	readers   wire.Readers
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
		readers:   opts.MediaReaders,
		idle:      time.Duration(svcCfg.IdleTimeout),
	}
}

// Send performs one responses attempt — request marshal, POST, decode,
// then append the assistant message and usage onto conv. Stream mode is
// selected by prov; events reach onEvent uniformly either way.
func (s *Service) Send(ctx context.Context, conv *aggregate.Conversation, prov *aggregate.Provider, model entity.Model, onEvent event.TurnEventHandler) (retErr error) {
	stream := prov.StreamMode()
	body, err := encodeRequest(conv, model, stream, wire.MediaRoot{Dir: coretext.FileRoot(conv, s.fileRoot), Store: s.fileStore, Readers: s.readers})
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	s.logAppend(ctx, "provider.request", json.RawMessage(raw))

	att := coretext.NewAttempt(conv, prov, model.ID(), stream, len(raw), len(body.Input), len(body.Tools), body.Tools)
	defer func() { att.LogStats(ctx, s.log, conv, retErr) }()

	// prov.Endpoint() is the provider's base URL (e.g.
	// https://api.openai.com/v1); this wire lives at /responses under it.
	url := strings.TrimRight(prov.Endpoint(), "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	// Bearer auth is canonical on this surface.
	req.Header.Set("Authorization", "Bearer "+prov.ApiKey())
	req.Header.Set("Content-Type", "application/json")
	if enum.OpenCode.MatchesEndpoint(prov.Endpoint()) {
		req.Header.Set("x-opencode-session", conv.ID())
	}
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
		return s.streamResponse(ctx, resp, conv, onEvent, att)
	}
	return s.unaryResponse(ctx, resp, conv, onEvent)
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
	if rb.Status == "failed" {
		// Same payload shape as the streamed response.failed event —
		// streamFailure owns the routing remaps for both.
		return wire.WithRetryAfter(streamFailure(&rb, raw), wire.RetryAfter(resp.Header.Get("Retry-After")))
	}
	msg, meta, err := decodeResponse(&rb)
	if err != nil {
		return err
	}
	if err := coretext.EmptyAnswerError(msg, meta.artifacts, "", []json.RawMessage{raw}); err != nil {
		return err
	}
	usage, err := decodeUsage(rb.Usage)
	if err != nil {
		return err
	}
	conv.Append(msg)
	conv.AddUsage(usage)
	if meta.reasoning != "" {
		// decodeResponse collects summary_text parts from this response.
		emit(onEvent, event.NewReasoningSummaryEvent(meta.reasoning))
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
