package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	if got := parseRetryAfter("3", now); got != 3*time.Second {
		t.Fatalf("seconds Retry-After = %s, want 3s", got)
	}
	if got := parseRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); got != 5*time.Second {
		t.Fatalf("date Retry-After = %s, want 5s", got)
	}
	if got := parseRetryAfter("invalid", now); got != 0 {
		t.Fatalf("invalid Retry-After = %s, want 0", got)
	}
}

// TestIncompleteSSEErrorDistinct pins the contract for the "stream closed
// cleanly but the protocol terminator ([DONE] / message_stop /
// response.completed) never arrived" path. It must:
//   - still wrap io.ErrUnexpectedEOF so errors.Is keeps working,
//   - remain a temporary retryable UpstreamError,
//   - surface a message that names the failure mode (not bare "unexpected EOF")
//     so operators can tell it apart from a mid-frame network cut.
func TestIncompleteSSEErrorDistinct(t *testing.T) {
	err := incompleteSSEError()
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("incompleteSSEError must wrap io.ErrUnexpectedEOF, got %v", err)
	}
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("incompleteSSEError must be an *UpstreamError, got %T", err)
	}
	if !upstream.Temporary {
		t.Fatalf("incompleteSSEError must be marked Temporary")
	}
	if upstream.Kind != application.KindSSETransport {
		t.Fatalf("incompleteSSEError Kind = %q, want %q", upstream.Kind, application.KindSSETransport)
	}
	msg := err.Error()
	if !strings.Contains(msg, "incomplete") && !strings.Contains(msg, "terminator") {
		t.Fatalf("incompleteSSEError message must name the failure mode, got %q", msg)
	}
}

// TestRetryableSSEReadErrorDistinct pins the "connection cut mid-frame" path.
// It must preserve the underlying error chain, stay retryable, and produce a
// message distinct from incompleteSSEError so logs can differentiate the two.
func TestRetryableSSEReadErrorDistinct(t *testing.T) {
	err := retryableSSEReadError(io.ErrUnexpectedEOF)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("retryableSSEReadError must preserve io.ErrUnexpectedEOF chain, got %v", err)
	}
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("retryableSSEReadError must be an *UpstreamError, got %T", err)
	}
	if !upstream.Temporary {
		t.Fatalf("retryableSSEReadError must be marked Temporary")
	}
	if upstream.Kind != application.KindSSETransport {
		t.Fatalf("retryableSSEReadError Kind = %q, want %q", upstream.Kind, application.KindSSETransport)
	}
	if err.Error() == incompleteSSEError().Error() {
		t.Fatalf("retryableSSEReadError and incompleteSSEError must produce distinguishable messages (both %q)", err.Error())
	}
}

// TestRetryableSSEReadErrorPassesThroughNonRetryable ensures a non-network
// error (e.g. a JSON decode failure from decodeData, or an error returned by
// the frame callback) is NOT relabeled as a retryable UpstreamError — only
// io.ErrUnexpectedEOF and net.Error get the temporary wrapper.
func TestRetryableSSEReadErrorPassesThroughNonRetryable(t *testing.T) {
	src := errors.New("invalid SSE frame: bad json")
	err := retryableSSEReadError(src)
	if err != src {
		t.Fatalf("retryableSSEReadError must pass non-network errors through unwrapped, got %v", err)
	}
	var upstream *application.UpstreamError
	if errors.As(err, &upstream) {
		t.Fatalf("retryableSSEReadError must not wrap non-network errors as UpstreamError, got %v", upstream)
	}
}

// TestRetryableSSEReadErrorWrapsNetError verifies a net.Error (e.g. connection
// reset) is wrapped as a temporary retryable UpstreamError.
func TestRetryableSSEReadErrorWrapsNetError(t *testing.T) {
	src := &netError{msg: "read tcp 1.2.3.4:443: connection reset by peer", timeout: false}
	err := retryableSSEReadError(src)
	if !errors.Is(err, src) {
		t.Fatalf("retryableSSEReadError must preserve the net.Error chain, got %v", err)
	}
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) || !upstream.Temporary {
		t.Fatalf("retryableSSEReadError must wrap net.Error as temporary UpstreamError, got %v", err)
	}
}

// netError is a minimal net.Error implementation for testing.
type netError struct {
	msg     string
	timeout bool
}

func (e *netError) Error() string   { return e.msg }
func (e *netError) Timeout() bool   { return e.timeout }
func (e *netError) Temporary() bool { return true }

// TestReadSSEIdleTimeout verifies that readSSE with a non-zero idleTimeout
// returns errIdleTimeout when the stream stalls (sends one chunk then never
// sends another). This is the "hung provider" detection path — distinct from
// a clean close without terminator (incompleteSSEError) or a mid-frame cut
// (retryableSSEReadError).
func TestReadSSEIdleTimeout(t *testing.T) {
	// Simulate a provider that sends one SSE frame then stalls forever.
	r, w := io.Pipe()
	go func() {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		// Never close, never send more — simulates a hung stream.
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got []sseEvent
	err := readSSE(ctx, r, 50*time.Millisecond, func(ev sseEvent) error {
		got = append(got, ev)
		return nil
	})
	if !errors.Is(err, errIdleTimeout) {
		t.Fatalf("readSSE with idle timeout must return errIdleTimeout on stall, got %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("readSSE must deliver the first chunk before timing out")
	}
}

// TestReadSSEIdleTimeoutDisabled verifies that idleTimeout=0 falls back to the
// original blocking behavior (no idle watchdog). The test closes the pipe
// reader to unblock the scanner, confirming no idle timer fired.
func TestReadSSEIdleTimeoutDisabled(t *testing.T) {
	r, w := io.Pipe()
	go func() {
		fmt.Fprintf(w, "data: {\"choices\":[{}]}\n\n")
		time.Sleep(50 * time.Millisecond)
		w.Close()
	}()

	err := readSSE(context.Background(), r, 0, func(ev sseEvent) error { return nil })
	if err != nil {
		t.Fatalf("readSSE with idleTimeout=0 must not time out when stream closes, got %v", err)
	}
}

// TestReadSSEIdleTimeoutCompletes verifies that a stream that sends data and
// closes cleanly within the idle window does NOT trigger a timeout.
func TestReadSSEIdleTimeoutCompletes(t *testing.T) {
	r := strings.NewReader("data: {\"choices\":[{}]}\n\ndata: [DONE]\n\n")
	err := readSSE(context.Background(), r, 5*time.Second, func(ev sseEvent) error { return nil })
	if err != nil {
		t.Fatalf("readSSE must not time out on a completing stream, got %v", err)
	}
}

// TestIdleTimeoutErrorKind verifies that errIdleTimeout is wrapped as
// KindIdleTimeout by the adapter-level helper.
func TestIdleTimeoutErrorKind(t *testing.T) {
	err := idleTimeoutUpstreamError()
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("idleTimeoutUpstreamError must be *UpstreamError, got %T", err)
	}
	if upstream.Kind != application.KindIdleTimeout {
		t.Fatalf("Kind = %q, want %q", upstream.Kind, application.KindIdleTimeout)
	}
	if !upstream.Temporary {
		t.Fatalf("idle timeout must be Temporary (retryable)")
	}
}

// TestIsOpenRouterURL verifies OpenRouter host detection. Attribution headers
// must only be sent to openrouter.ai hosts — a custom proxy may reject or
// forward unknown router-specific headers to an unrelated upstream.
func TestIsOpenRouterURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://openrouter.ai/api/v1", true},
		{"https://api.openrouter.ai/api/v1", true},
		{"http://openrouter.ai", true},
		{"https://api.openai.com/v1", false},
		{"https://my-proxy.example.com/v1", false},
		{"https://deepseek.com/v1", false},
		{"not-a-url", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := isOpenRouterURL(tt.url); got != tt.want {
				t.Fatalf("isOpenRouterURL(%q) = %t, want %t", tt.url, got, tt.want)
			}
		})
	}
}

// TestOpenRouterAttributionHeaders verifies the attribution headers are
// present and carry the expected NusaShell identification values.
func TestOpenRouterAttributionHeaders(t *testing.T) {
	h := openRouterAttributionHeaders()
	if h["http-referer"] == "" {
		t.Fatal("missing http-referer header")
	}
	if h["x-openrouter-title"] == "" {
		t.Fatal("missing x-openrouter-title header")
	}
	if h["x-openrouter-categories"] == "" {
		t.Fatal("missing x-openrouter-categories header")
	}
}

// TestIsStreamUnsupported verifies detection of provider responses that reject
// streaming with a 4xx body containing "stream" + an unsupported phrase. 401/403
// auth errors must NOT be classified as stream-unsupported (they won't work
// non-streaming either).
func TestIsStreamUnsupported(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{name: "400 stream not supported", status: 400, body: "streaming is not supported for this model", want: true},
		{name: "400 stream unsupported", status: 400, body: "stream is unsupported", want: true},
		{name: "400 stream disabled", status: 400, body: "streaming is disabled", want: true},
		{name: "400 must be false", status: 400, body: "stream must be false", want: true},
		{name: "400 non-stream", status: 400, body: "non-stream mode required", want: true},
		{name: "401 auth error", status: 401, body: "stream not supported", want: false},
		{name: "403 forbidden", status: 403, body: "stream not available", want: false},
		{name: "400 unrelated", status: 400, body: "invalid model", want: false},
		{name: "500 server error", status: 500, body: "stream not supported", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStreamUnsupported(tt.status, tt.body); got != tt.want {
				t.Fatalf("isStreamUnsupported(%d, %q) = %t, want %t", tt.status, tt.body, got, tt.want)
			}
		})
	}
}

// TestIsResponsesUnsupported verifies detection of 404/405 and body phrases
// that indicate the Responses API is not available, triggering a fallback to
// chat completions.
func TestIsResponsesUnsupported(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{name: "404 not found", status: 404, body: "not found", want: true},
		{name: "405 method not allowed", status: 405, body: "method not allowed", want: true},
		{name: "400 not supported", status: 400, body: "responses API not supported", want: true},
		{name: "400 does not support", status: 400, body: "does not support responses", want: true},
		{name: "500 server error", status: 500, body: "internal error", want: false},
		{name: "429 rate limit", status: 429, body: "rate limited", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &application.UpstreamError{
				Kind:       application.KindHTTPStatus,
				StatusCode: tt.status,
				Err:        errors.New(tt.body),
			}
			if got := isResponsesUnsupported(err); got != tt.want {
				t.Fatalf("isResponsesUnsupported(%d, %q) = %t, want %t", tt.status, tt.body, got, tt.want)
			}
		})
	}
}

// TestLooksLikeSseText verifies detection of SSE-formatted text in a
// non-SSE response body (some providers return SSE with a JSON content-type).
func TestLooksLikeSseText(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"data: {\"choices\":[]}\n\n", true},
		{"event: ping\ndata: {}\n\n", true},
		{"  data: hello\n", true},
		{"{\"choices\":[]}", false},
		{"", false},
		{"plain text response", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := looksLikeSseText(tt.input); got != tt.want {
				t.Fatalf("looksLikeSseText(%q) = %t, want %t", tt.input, got, tt.want)
			}
		})
	}
}

// TestShouldRetryWithoutImages verifies that a 4xx error with image
// attachments triggers a retry without images, but 5xx errors, non-image
// messages, and already-aborted contexts do not.
func TestShouldRetryWithoutImages(t *testing.T) {
	ctxAborted, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name     string
		err      error
		hasImage bool
		ctx      context.Context
		want     bool
	}{
		{
			name:     "4xx with image",
			err:      &application.UpstreamError{Kind: application.KindHTTPStatus, StatusCode: 400, Err: errors.New("bad request")},
			hasImage: true,
			ctx:      context.Background(),
			want:     true,
		},
		{
			name:     "4xx without image",
			err:      &application.UpstreamError{Kind: application.KindHTTPStatus, StatusCode: 400, Err: errors.New("bad request")},
			hasImage: false,
			ctx:      context.Background(),
			want:     false,
		},
		{
			name:     "5xx with image",
			err:      &application.UpstreamError{Kind: application.KindHTTPStatus, StatusCode: 503, Err: errors.New("server error")},
			hasImage: true,
			ctx:      context.Background(),
			want:     false,
		},
		{
			name:     "4xx with image but aborted",
			err:      &application.UpstreamError{Kind: application.KindHTTPStatus, StatusCode: 400, Err: errors.New("bad request")},
			hasImage: true,
			ctx:      ctxAborted,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := []application.ChatMessage{
				{Role: "user", Content: "hello"},
			}
			if tt.hasImage {
				msgs[0].Attachments = []domain.Attachment{{Type: "image", DataURL: "data:image/png;base64,abc"}}
			}
			if got := shouldRetryWithoutImages(tt.err, msgs, tt.ctx); got != tt.want {
				t.Fatalf("shouldRetryWithoutImages = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestRepairToolCallArguments verifies that malformed JSON tool call arguments
// (trailing commas, markdown fences) are repaired to valid JSON.
func TestRepairToolCallArguments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "valid json", input: `{"key":"value"}`, want: `{"key":"value"}`},
		{name: "trailing comma object", input: `{"key":"value",}`, want: `{"key":"value"}`},
		{name: "trailing comma array", input: `{"items":[1,2,3,]}`, want: `{"items":[1,2,3]}`},
		{name: "markdown fence", input: "```json\n{\"key\":\"value\"}\n```", want: `{"key":"value"}`},
		{name: "markdown fence no lang", input: "```\n{\"key\":\"value\"}\n```", want: `{"key":"value"}`},
		{name: "empty string", input: "", want: "{}"},
		{name: "whitespace only", input: "  ", want: "{}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repairToolCallArguments(tt.input)
			if got != tt.want {
				t.Fatalf("repairToolCallArguments(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
