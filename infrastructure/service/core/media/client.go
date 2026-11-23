// Package coremedia holds the HTTP mechanics the generation families
// (image, speech, transcription, video) share: credentials and
// attribution headers per wire style, capped and idle-guarded body
// reads, and the shared status→apperror convention. Request and response
// shapes stay inside each capability's adapter.
package coremedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

// Options mirror the text adapters' — zero fields take the service
// config defaults.
type Options struct {
	Client        *http.Client
	Logger        drepo.Logger
	Referer       string // → HTTP-Referer
	Title         string // → X-Title
	HeaderTimeout time.Duration
	IdleTimeout   time.Duration
}

// Client sends one generation request at a time; it is stateless and
// safe for concurrent use.
type Client struct {
	http    *http.Client
	log     drepo.Logger
	referer string
	title   string
	idle    time.Duration
}

func New(opts Options) *Client {
	svcCfg := config.ServiceConfig{
		HeaderTimeout: config.Duration(opts.HeaderTimeout),
		IdleTimeout:   config.Duration(opts.IdleTimeout),
	}
	svcCfg.Normalize()
	client := opts.Client
	if client == nil {
		// Generation can take minutes before the first byte (an image
		// model renders, then answers); the header wait is longer than a
		// chat's, and the idle guard still catches a dead body.
		client = &http.Client{Transport: &http.Transport{
			ResponseHeaderTimeout: max(time.Duration(svcCfg.HeaderTimeout), 5*time.Minute),
		}}
	}
	return &Client{
		http:    client,
		log:     opts.Logger,
		referer: opts.Referer,
		title:   opts.Title,
		idle:    time.Duration(svcCfg.IdleTimeout),
	}
}

// Request is one call: Path is joined to the provider's endpoint unless
// it is already an absolute URL. Limit caps the response body.
type Request struct {
	Method      string
	Path        string
	Body        []byte
	ContentType string
	Style       enum.Style // decides the credential header
	Limit       int64
	// Log names the call in the diagnostic log; request and response
	// bodies are not logged — they carry media payloads.
	Log string
}

// Response is a successful (2xx) answer.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// URL addresses path on the provider: an absolute URL stays as is, a
// host-absolute path ("/api/v1/videos/x/content", as providers answer
// download links) resolves against the endpoint's origin, and anything
// else is joined onto the endpoint base.
func URL(prov *aggregate.Provider, path string) string {
	if strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "http://") {
		return path
	}
	if strings.HasPrefix(path, "/") {
		if base, err := url.Parse(prov.Endpoint()); err == nil {
			return base.Scheme + "://" + base.Host + path
		}
	}
	return strings.TrimRight(prov.Endpoint(), "/") + "/" + path
}

// Do sends r with prov's credentials. A non-2xx status, an error envelope,
// or a failed transport returns an apperror the caller can surface.
func (c *Client) Do(ctx context.Context, prov *aggregate.Provider, r Request) (Response, error) {
	u := URL(prov, r.Path)
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
	if err != nil {
		return Response{}, apperror.New(400, "invalid provider endpoint", err)
	}
	if r.Style == enum.StyleGenerateContent {
		// x-goog-api-key is canonical on this surface — Bearer often fails.
		req.Header.Set("x-goog-api-key", prov.ApiKey())
	} else {
		req.Header.Set("Authorization", "Bearer "+prov.ApiKey())
	}
	if r.ContentType != "" {
		req.Header.Set("Content-Type", r.ContentType)
	}
	if c.referer != "" {
		req.Header.Set("HTTP-Referer", c.referer)
	}
	if c.title != "" {
		req.Header.Set("X-Title", c.title)
	}
	c.logAppend(ctx, "provider.media.request", map[string]any{
		"call": r.Log, "method": r.Method, "url": u, "bytes": len(r.Body),
	})
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, apperror.New(408, "request canceled or timed out", ctx.Err())
		}
		return Response{}, apperror.New(502, "upstream unreachable", err)
	}
	defer resp.Body.Close()
	limit := r.Limit
	if limit <= 0 {
		limit = 32 << 20
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := wire.ReadBody(ctx, resp.Body, c.idle)
		c.logAppend(ctx, "provider.media.error", map[string]any{
			"call": r.Log, "status": resp.StatusCode, "body": string(raw),
		})
		return Response{}, StatusError(resp.StatusCode, resp.Header.Get("Retry-After"), raw)
	}
	raw, err := wire.ReadBodyLimit(ctx, resp.Body, c.idle, limit)
	if err != nil {
		return Response{}, err
	}
	c.logAppend(ctx, "provider.media.response", map[string]any{
		"call": r.Log, "status": resp.StatusCode, "type": resp.Header.Get("Content-Type"), "bytes": len(raw),
	})
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

// JSON marshals v as a request body.
func JSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, apperror.New(500, "could not encode the request", err)
	}
	return raw, nil
}

// Form builds a multipart/form-data body.
type Form struct {
	buf bytes.Buffer
	w   *multipart.Writer
	err error
}

func NewForm() *Form {
	f := &Form{}
	f.w = multipart.NewWriter(&f.buf)
	return f
}

// Field adds a text field; an empty value is left out.
func (f *Form) Field(name, value string) {
	if f.err != nil || value == "" {
		return
	}
	f.err = f.w.WriteField(name, value)
}

// File adds a file part with its own content type.
func (f *Form) File(name, filename, mediaType string, data []byte) {
	if f.err != nil {
		return
	}
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="` + escapeQuotes(name) + `"; filename="` + escapeQuotes(filename) + `"`}
	h["Content-Type"] = []string{mediaType}
	part, err := f.w.CreatePart(h)
	if err != nil {
		f.err = err
		return
	}
	_, f.err = part.Write(data)
}

// Close finishes the body and answers it with its content type.
func (f *Form) Close() ([]byte, string, error) {
	if f.err == nil {
		f.err = f.w.Close()
	}
	if f.err != nil {
		return nil, "", apperror.New(500, "could not encode the request", f.err)
	}
	return f.buf.Bytes(), f.w.FormDataContentType(), nil
}

func escapeQuotes(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// StatusError maps a rejected request through the shared convention,
// reading the provider's message from the OpenAI-shaped or Google-shaped
// error envelope ({"error":{"message","code"|"status"}}) or a bare
// {"error":"…"}.
func StatusError(status int, retryAfter string, body []byte) error {
	msg, code := envelope(body)
	return wire.WithRetryAfter(wire.StatusError(status, msg, code, body), wire.RetryAfter(retryAfter))
}

// EnvelopeError reports an error envelope inside a 2xx body — some
// proxies answer failures as HTTP 200. nil when the body carries none.
func EnvelopeError(body []byte) error {
	msg, code := envelope(body)
	if msg == "" && code == "" {
		return nil
	}
	status := 502
	if n, err := strconv.Atoi(code); err == nil && n >= 400 && n <= 599 {
		status = n
	}
	return wire.StatusError(status, msg, code, body)
}

func envelope(body []byte) (msg, code string) {
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil || len(env.Error) == 0 || string(env.Error) == "null" {
		return "", ""
	}
	var s string
	if json.Unmarshal(env.Error, &s) == nil {
		return s, ""
	}
	var e struct {
		Message string          `json:"message"`
		Code    json.RawMessage `json:"code"`
		Status  string          `json:"status"`
		Type    string          `json:"type"`
	}
	if json.Unmarshal(env.Error, &e) != nil {
		return "", ""
	}
	code = strings.Trim(string(e.Code), `"`)
	if code == "" || code == "null" {
		code = e.Status
	}
	if code == "" {
		code = e.Type
	}
	return e.Message, code
}

func (c *Client) logAppend(ctx context.Context, kind string, v any) {
	if c.log != nil {
		_ = c.log.Append(ctx, kind, v) // diagnostics must never break a call
	}
}

// Download fetches a deliverable the provider answered with as a URL
// (a stored image, a finished video). Credentials ride only to the
// provider's own host — a pre-signed storage URL elsewhere needs none,
// and must not receive the key.
func (c *Client) Download(ctx context.Context, prov *aggregate.Provider, style enum.Style, rawURL string, limit int64) (Response, error) {
	if given, err := url.Parse(rawURL); err != nil || (given.Scheme != "" && given.Scheme != "https" && given.Scheme != "http") {
		return Response{}, apperror.New(502, "provider answered an unusable media URL", err)
	}
	target, err := url.Parse(URL(prov, rawURL))
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") {
		return Response{}, apperror.New(502, "provider answered an unusable media URL", err)
	}
	base, err := url.Parse(prov.Endpoint())
	if err == nil && strings.EqualFold(base.Hostname(), target.Hostname()) {
		return c.Do(ctx, prov, Request{Method: http.MethodGet, Path: target.String(), Style: style, Limit: limit, Log: "download"})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Response{}, apperror.New(502, "provider answered an unusable media URL", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, apperror.New(408, "request canceled or timed out", ctx.Err())
		}
		return Response{}, apperror.New(502, "could not download the generated media", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Response{}, apperror.New(502, fmt.Sprintf("could not download the generated media (HTTP %d)", resp.StatusCode), nil)
	}
	raw, err := wire.ReadBodyLimit(ctx, resp.Body, c.idle, limit)
	if err != nil {
		return Response{}, err
	}
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

// MediaType settles a payload's type: the provider's declaration when it
// names a media type, else a sniff of the bytes. SVG sniffs as XML, so
// markup that opens an <svg> element is named for what it is.
func MediaType(declared string, data []byte) string {
	if t, _, err := mime.ParseMediaType(declared); err == nil && strings.Contains(t, "/") && t != "application/octet-stream" {
		return declared
	}
	sniffed := http.DetectContentType(data)
	if strings.HasPrefix(sniffed, "text/xml") || strings.HasPrefix(sniffed, "text/plain") {
		head := data[:min(len(data), 1024)]
		if bytes.Contains(bytes.ToLower(head), []byte("<svg")) {
			return "image/svg+xml"
		}
	}
	return sniffed
}

// GeminiURL addresses a model method on the generateContent surface.
// Prefixed ids ("google/gemini-x", "models/x") normalize to the bare id,
// escaped as one path segment so a delimiter inside the id cannot
// rewrite the URL and address a different model.
func GeminiURL(prov *aggregate.Provider, modelID, method string) string {
	id := strings.TrimPrefix(modelID, "models/")
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	return strings.TrimRight(prov.Endpoint(), "/") + "/models/" + url.PathEscape(id) + ":" + method
}

// GeminiPart is one generateContent content part — text or inline media.
type GeminiPart struct {
	Text       string      `json:"text,omitempty"`
	InlineData *GeminiBlob `json:"inlineData,omitempty"`
}

type GeminiBlob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

// GeminiResponse is the slice of a generateContent answer the media
// families read: the first candidate's parts, and why nothing came back.
type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []GeminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

// DecodeGemini reads a generateContent answer, or the reason it is empty.
func DecodeGemini(body []byte) (parts []GeminiPart, text string, err error) {
	var r GeminiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, "", apperror.New(502, "malformed upstream body", err)
	}
	if r.PromptFeedback.BlockReason != "" {
		return nil, "", apperror.New(400, "the provider refused the prompt ("+r.PromptFeedback.BlockReason+")", nil)
	}
	if len(r.Candidates) == 0 {
		return nil, "", apperror.New(502, "upstream returned no candidates", nil)
	}
	var sb strings.Builder
	for _, p := range r.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	parts = r.Candidates[0].Content.Parts
	if len(parts) == 0 && r.Candidates[0].FinishReason != "" && r.Candidates[0].FinishReason != "STOP" {
		return nil, "", apperror.New(502, "the model stopped without an answer ("+r.Candidates[0].FinishReason+")", nil)
	}
	return parts, strings.TrimSpace(sb.String()), nil
}

// WAV wraps raw 16-bit little-endian PCM in a RIFF header so players and
// chat apps can open it — PCM surfaces (Gemini speech, /audio/speech
// with pcm) answer headerless samples.
func WAV(pcm []byte, rate, channels int) []byte {
	if rate <= 0 {
		rate = 24000
	}
	if channels <= 0 {
		channels = 1
	}
	const bits = 16
	out := make([]byte, 44, 44+len(pcm))
	copy(out[0:], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+len(pcm)))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1) // PCM
	binary.LittleEndian.PutUint16(out[22:], uint16(channels))
	binary.LittleEndian.PutUint32(out[24:], uint32(rate))
	binary.LittleEndian.PutUint32(out[28:], uint32(rate*channels*bits/8))
	binary.LittleEndian.PutUint16(out[32:], uint16(channels*bits/8))
	binary.LittleEndian.PutUint16(out[34:], bits)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(len(pcm)))
	return append(out, pcm...)
}

// PCMRate reads the sample rate a PCM media type declares
// ("audio/L16;codec=pcm;rate=24000"); 0 when it names none.
func PCMRate(mediaType string) int {
	_, params, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(params["rate"])
	return n
}

// IsPCM reports a headerless PCM media type.
func IsPCM(mediaType string) bool {
	t, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return false
	}
	t = strings.ToLower(t)
	return t == "audio/pcm" || t == "audio/l16" || t == "audio/raw" || t == "audio/x-raw"
}
