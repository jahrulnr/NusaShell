// Package ai implements the application AI provider port for Anthropic and
// OpenAI-compatible chat endpoints, including SSE streaming, tool calling,
// and Anthropic prompt caching.
package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"nusashell/application"
)

// defaultIdleTimeout is the per-chunk stall window for SSE streams. The timer
// resets on every successful read, so it only fires when the provider sends
// no data for this duration — a hung stream, not a slow one. Mirrors the TS
// createIdleTimeout(DEFAULT_TIMEOUT_MS = 60_000).
const defaultIdleTimeout = 60 * time.Second

// defaultMaxResponseBytes is the safety cap on SSE stream size. Prevents OOM
// if a provider sends an unexpectedly large response. Mirrors the TS
// DEFAULT_MAX_RESPONSE_BYTES = 8 * 1024 * 1024.
const defaultMaxResponseBytes = 8 * 1024 * 1024

// sseEvent is one Server-Sent Event frame.
type sseEvent struct {
	Event string
	Data  string
}

// errIdleTimeout is returned by readSSE when the stream stalls for the
// configured idle window with no data. It is wrapped as KindIdleTimeout by
// the adapter.
var errIdleTimeout = errors.New("SSE stream idle timeout: provider stalled with no data")

// errResponseTooLarge is returned when the SSE stream exceeds
// defaultMaxResponseBytes. It is wrapped as a non-retryable UpstreamError.
var errResponseTooLarge = errors.New("SSE stream exceeded the configured size limit")

// limitedReader wraps an io.Reader and returns errResponseTooLarge when the
// total bytes read exceed max. Unlike io.LimitReader, it returns a distinct
// error instead of a silent EOF at the limit.
type limitedReader struct {
	r   io.Reader
	n   int
	max int
}

func (l *limitedReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += n
	if l.n > l.max {
		return n, errResponseTooLarge
	}
	return n, err
}

// idleTimeoutReader wraps an io.ReadCloser with a resettable idle timer.
// Each successful Read resets the timer. If no data arrives for timeout
// duration, the timer fires, closes the underlying reader (unblocking any
// pending Read), and subsequent/timed-out reads return errIdleTimeout.
type idleTimeoutReader struct {
	rc       io.ReadCloser
	timeout  time.Duration
	timer    *time.Timer
	mu       sync.Mutex
	timedOut bool
}

func newIdleTimeoutReader(rc io.ReadCloser, timeout time.Duration) *idleTimeoutReader {
	r := &idleTimeoutReader{rc: rc, timeout: timeout}
	r.timer = time.AfterFunc(timeout, func() {
		r.mu.Lock()
		r.timedOut = true
		_ = rc.Close() // unblock any pending Read on the body
		r.mu.Unlock()
	})
	return r
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	r.mu.Lock()
	timedOut := r.timedOut
	r.mu.Unlock()
	if timedOut {
		return n, errIdleTimeout
	}
	if n > 0 {
		r.timer.Reset(r.timeout)
	}
	return n, err
}

func (r *idleTimeoutReader) Close() error {
	r.timer.Stop()
	return r.rc.Close()
}

// readSSE streams SSE frames from r, calling fn for each frame until EOF.
// If idleTimeout > 0, the stream is wrapped with an idle watchdog that fires
// errIdleTimeout when no data arrives for the configured duration. Pass 0 to
// disable the idle watchdog (original blocking behavior). If maxBytes > 0,
// the read aborts with errResponseTooLarge when the stream exceeds the limit.
func readSSE(ctx context.Context, r io.Reader, idleTimeout time.Duration, fn func(ev sseEvent) error) error {
	var body io.Reader = r
	if idleTimeout > 0 {
		// Wrap with idle timeout. If r is not a ReadCloser, wrap with
		// io.NopCloser so Close is available.
		rc, ok := r.(io.ReadCloser)
		if !ok {
			rc = io.NopCloser(r)
		}
		body = newIdleTimeoutReader(rc, idleTimeout)
	}
	// Wrap with a byte-counting reader to enforce maxResponseBytes.
	body = &limitedReader{r: body, max: defaultMaxResponseBytes}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var ev sseEvent
	flush := func() error {
		if ev.Data != "" || ev.Event != "" {
			if err := fn(ev); err != nil {
				return err
			}
		}
		ev = sseEvent{}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimPrefix(val, " ")
		switch key {
		case "event":
			ev.Event = val
		case "data":
			ev.Data += val
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

// decodeData unmarshals an SSE data payload.
func decodeData[T any](ev sseEvent, out *T) error {
	if ev.Data == "" {
		return fmt.Errorf("empty SSE data frame")
	}
	return json.Unmarshal([]byte(ev.Data), out)
}

// joinEndpoint appends the operation path to the configured base URL
// verbatim. The base URL is the API root the user chose — it already carries
// whatever version segment the endpoint uses (v1, v4, …) — so no version is
// ever injected. A base that already ends with the operation path is used
// as-is, letting users paste a full endpoint.
func joinEndpoint(base, op string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, op) {
		return base
	}
	return base + op
}

// jsonReq builds an HTTP request with a JSON body.
func jsonReq(ctx context.Context, method, url string, headers map[string]string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// openSSE creates an SSE request and validates the HTTP response before a
// provider-specific stream decoder reads its frames. Wire decoding stays in
// each adapter because the event shapes differ by provider protocol.
func openSSE(ctx context.Context, client *http.Client, url string, headers map[string]string, body any) (*http.Response, error) {
	req, err := jsonReq(ctx, http.MethodPost, url, headers, body)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &application.UpstreamError{Kind: application.KindConnect, Temporary: true, Err: err}
	}
	if resp.StatusCode < 400 {
		return resp, nil
	}
	defer resp.Body.Close()
	message, _ := readAllLimit(resp.Body, 4096)
	return nil, &application.UpstreamError{
		Kind:       application.KindHTTPStatus,
		StatusCode: resp.StatusCode,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		Err:        fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(message)),
	}
}

func readAllLimit(r io.Reader, n int64) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, n))
	return string(b), err
}

// doJSON performs a request and decodes the JSON response into out.
func doJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return &application.UpstreamError{Kind: application.KindConnect, Temporary: true, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &application.UpstreamError{
			Kind:       application.KindHTTPStatus,
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
			Err:        fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
		}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}

// isOpenRouterURL reports whether baseUrl points at an OpenRouter API host.
// OpenRouter-specific attribution headers must only be sent to its own hosts;
// a custom OpenAI-compatible proxy may reject or forward unknown
// router-specific headers to an unrelated upstream.
func isOpenRouterURL(baseUrl string) bool {
	u, err := neturl.Parse(baseUrl)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
}

// openRouterAttributionHeaders returns the OpenRouter ranking/attribution
// headers NusaShell sends when talking to an OpenRouter endpoint.
func openRouterAttributionHeaders() map[string]string {
	return map[string]string{
		"http-referer":            "https://github.com/jahrulnr/NusaShell",
		"x-openrouter-title":      "NusaShell",
		"x-openrouter-categories": "personal-agent,programming-app",
	}
}

// streamUnsupportedPhrases are body substrings (case-insensitive) that, when
// combined with a 4xx status and the word "stream", indicate the provider
// rejects streaming for this model/endpoint. The adapter should fall back to
// non-streaming. Mirrors the TS isStreamUnsupported phrase list.
var streamUnsupportedPhrases = []string{
	"not support", "unsupported", "disabled", "not available", "not enabled", "must be false", "non-stream",
}

// isStreamUnsupported reports whether a 4xx response body indicates the
// provider rejects streaming. Auth errors (401/402/403) are excluded — they
// won't work non-streaming either. 5xx errors are excluded — they're transient.
func isStreamUnsupported(status int, body string) bool {
	if status == 401 || status == 402 || status == 403 {
		return false
	}
	if status < 400 || status >= 500 {
		return false
	}
	normalized := strings.ToLower(body)
	if !strings.Contains(normalized, "stream") {
		return false
	}
	for _, phrase := range streamUnsupportedPhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

// isResponsesUnsupported reports whether an UpstreamError indicates the
// Responses API (/responses) is not available, triggering a fallback to chat
// completions (/chat/completions). 404/405 are always unsupported; 4xx/5xx
// bodies with "not found"/"not supported"/"does not support"/"unavailable"
// are also unsupported.
func isResponsesUnsupported(err error) bool {
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) {
		return false
	}
	if upstream.StatusCode == 404 || upstream.StatusCode == 405 {
		return true
	}
	if upstream.Err == nil {
		return false
	}
	normalized := strings.ToLower(upstream.Err.Error())
	if upstream.StatusCode < 400 || upstream.StatusCode >= 600 {
		return false
	}
	for _, phrase := range []string{"not found", "not supported", "does not support", "unavailable"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

// looksLikeSseText reports whether a response body string begins with SSE
// framing (data: or event:), used to detect SSE content returned with a
// non-event-stream content-type.
func looksLikeSseText(value string) bool {
	prefix := strings.TrimLeft(value, " \t\r\n")
	if len(prefix) > 32 {
		prefix = prefix[:32]
	}
	prefix = strings.ToLower(prefix)
	return strings.HasPrefix(prefix, "data:") || strings.HasPrefix(prefix, "event:")
}

// isStreamUnsupportedError checks whether an UpstreamError from openSSE
// indicates the provider rejects streaming, triggering a fallback to
// non-streaming Complete.
func isStreamUnsupportedError(err error) bool {
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) || upstream.Err == nil {
		return false
	}
	return isStreamUnsupported(upstream.StatusCode, upstream.Err.Error())
}

// streamFallbackToComplete calls the adapter's Complete method and simulates
// streaming by emitting the full content/reasoning as a single delta. Used
// when the provider rejects streaming but non-streaming works.
func streamFallbackToComplete(ctx context.Context, adapter application.AIProvider, req application.ChatRequest, onDelta, onReasoning func(string)) (application.ChatResponse, error) {
	resp, err := adapter.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if resp.Content != "" && onDelta != nil {
		onDelta(resp.Content)
	}
	if resp.Reasoning != "" && onReasoning != nil {
		onReasoning(resp.Reasoning)
	}
	return resp, nil
}

// shouldRetryWithoutImages reports whether a 4xx provider error should be
// retried with image attachments stripped. Returns true only when the error
// is a 4xx UpstreamError, the context is not aborted, and at least one user
// message carries an image attachment. Mirrors the TS shouldRetryWithoutImages.
func shouldRetryWithoutImages(err error, messages []application.ChatMessage, ctx context.Context) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) {
		return false
	}
	if upstream.StatusCode < 400 || upstream.StatusCode >= 500 {
		return false
	}
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		for _, att := range msg.Attachments {
			if att.Type == "image" {
				return true
			}
		}
	}
	return false
}

// stripImages returns a copy of messages with image attachments removed.
// Used to retry a 4xx request that may have been rejected due to image
// content (some providers/models reject images with a 400).
func stripImages(messages []application.ChatMessage) []application.ChatMessage {
	out := make([]application.ChatMessage, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].Role != "user" || len(out[i].Attachments) == 0 {
			continue
		}
		filtered := out[i].Attachments[:0:0]
		for _, att := range out[i].Attachments {
			if att.Type != "image" {
				filtered = append(filtered, att)
			}
		}
		out[i].Attachments = filtered
	}
	return out
}

// isIncompleteEmptyStream reports whether the error is incompleteSSEError with
// no accumulated content, indicating an empty response body that may succeed
// as a non-streaming request.
func isIncompleteEmptyStream(err error, result application.ChatResponse) bool {
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) || upstream.Kind != application.KindSSETransport {
		return false
	}
	if !errors.Is(upstream.Err, io.ErrUnexpectedEOF) {
		return false
	}
	return result.Content == "" && result.Reasoning == "" && len(result.ToolCalls) == 0
}

// stripTrailingCommas removes trailing commas before } and ] in JSON-like
// strings, handling quoted strings correctly. Mirrors the TS stripTrailingCommas.
func stripTrailingCommas(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	quoted := false
	escaped := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if quoted {
			out.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = false
			}
			continue
		}
		if ch == '"' {
			quoted = true
			out.WriteByte(ch)
			continue
		}
		if ch == ',' {
			next := i + 1
			for next < len(value) && (value[next] == ' ' || value[next] == '\t' || value[next] == '\n' || value[next] == '\r') {
				next++
			}
			if next < len(value) && (value[next] == '}' || value[next] == ']') {
				continue
			}
		}
		out.WriteByte(ch)
	}
	return out.String()
}

// repairToolCallArguments attempts to repair malformed JSON tool call
// arguments by stripping markdown fences and trailing commas. Returns "{}"
// for empty/whitespace input. If the result is not valid JSON, returns "{}".
// Mirrors the TS repairableJsonCandidates + parseObject pipeline.
func repairToolCallArguments(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "{}"
	}
	// Strip markdown fences: ```json\n...\n``` or ```\n...\n```
	if fence := extractMarkdownFence(trimmed); fence != "" {
		trimmed = strings.TrimSpace(fence)
	}
	// Strip trailing commas
	repaired := stripTrailingCommas(trimmed)
	// Validate: if it parses as a JSON object, return it; otherwise return {}
	var obj map[string]any
	if err := json.Unmarshal([]byte(repaired), &obj); err == nil {
		return repaired
	}
	// Try the original (maybe the commas were fine and something else is wrong)
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		return trimmed
	}
	return "{}"
}

// extractMarkdownFence returns the inner content if value is wrapped in a
// markdown code fence (```json ... ``` or ``` ... ```), empty string otherwise.
func extractMarkdownFence(value string) string {
	// Match ```json\n...\n``` or ```\n...\n```
	if !strings.HasPrefix(value, "```") {
		return ""
	}
	rest := value[3:]
	// Skip optional language tag (json, etc.)
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		lang := strings.TrimSpace(rest[:nl])
		if lang == "" || lang == "json" {
			rest = rest[nl+1:]
		} else {
			return ""
		}
	} else {
		return ""
	}
	rest = strings.TrimRight(rest, "\n\r")
	if strings.HasSuffix(rest, "```") {
		return strings.TrimSuffix(rest, "```")
	}
	return ""
}

func incompleteSSEError() error {
	// The HTTP stream closed cleanly (no scanner error) but the provider
	// never sent the protocol terminator ([DONE] for OpenAI/Responses,
	// message_stop for Anthropic, response.completed for Responses). This
	// is the "clean close, no terminator" path — distinct from a mid-frame
	// TCP cut, which surfaces as a real io.ErrUnexpectedEOF from the body
	// reader and is wrapped by retryableSSEReadError below. Naming the mode
	// lets operators tell the two apart in the retry log.
	return &application.UpstreamError{
		Kind:      application.KindSSETransport,
		Temporary: true,
		Err:       fmt.Errorf("incomplete SSE stream: terminator never received: %w", io.ErrUnexpectedEOF),
	}
}

func retryableSSEReadError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errIdleTimeout) {
		return idleTimeoutUpstreamError()
	}
	if errors.Is(err, errResponseTooLarge) {
		// Non-retryable: the response is genuinely too large, retrying
		// will hit the same limit.
		return &application.UpstreamError{
			Kind:      application.KindSSETransport,
			Temporary: false,
			Err:       errResponseTooLarge,
		}
	}
	var networkErr net.Error
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &networkErr) {
		// Mid-frame read failure: the connection was cut while a frame was
		// in flight. Wrap with a descriptive prefix so this path is
		// distinguishable from incompleteSSEError in logs.
		return &application.UpstreamError{
			Kind:      application.KindSSETransport,
			Temporary: true,
			Err:       fmt.Errorf("SSE read interrupted mid-frame: %w", err),
		}
	}
	return err
}

// idleTimeoutUpstreamError wraps errIdleTimeout as a retryable UpstreamError
// with KindIdleTimeout, so the retry log and event can distinguish a stalled
// stream from a mid-frame cut or a clean close without terminator.
func idleTimeoutUpstreamError() error {
	return &application.UpstreamError{
		Kind:      application.KindIdleTimeout,
		Temporary: true,
		Err:       errIdleTimeout,
	}
}
