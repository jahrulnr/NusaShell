package aiutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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
//   - remain a temporary retryable domain.ProviderError,
//   - surface a message that names the failure mode (not bare "unexpected EOF")
//     so operators can tell it apart from a mid-frame network cut.
func TestReadSSEIdleTimeout(t *testing.T) {
	// Simulate a provider that sends one SSE frame then stalls forever.
	r, w := io.Pipe()
	go func() {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		// Never close, never send more — simulates a hung stream.
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got []Event
	err := ReadSSE(ctx, r, 50*time.Millisecond, func(ev Event) error {
		got = append(got, ev)
		return nil
	})
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("ReadSSE with idle timeout must return ErrIdleTimeout on stall, got %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("ReadSSE must deliver the first chunk before timing out")
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

	err := ReadSSE(context.Background(), r, 0, func(ev Event) error { return nil })
	if err != nil {
		t.Fatalf("ReadSSE with idleTimeout=0 must not time out when stream closes, got %v", err)
	}
}

// TestReadSSEIdleTimeoutCompletes verifies that a stream that sends data and
// closes cleanly within the idle window does NOT trigger a timeout.
func TestReadSSEIdleTimeoutCompletes(t *testing.T) {
	r := strings.NewReader("data: {\"choices\":[{}]}\n\ndata: [DONE]\n\n")
	err := ReadSSE(context.Background(), r, 5*time.Second, func(ev Event) error { return nil })
	if err != nil {
		t.Fatalf("ReadSSE must not time out on a completing stream, got %v", err)
	}
}

// TestIdleTimeoutErrorKind verifies that ErrIdleTimeout is wrapped as
// domain.KindIdleTimeout by the adapter-level helper.
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
			if got := IsOpenRouterURL(tt.url); got != tt.want {
				t.Fatalf("IsOpenRouterURL(%q) = %t, want %t", tt.url, got, tt.want)
			}
		})
	}
}

// TestOpenRouterAttributionHeaders verifies the attribution headers are
// present and carry the expected NusaShell identification values.
func TestOpenRouterAttributionHeaders(t *testing.T) {
	h := OpenRouterAttributionHeaders()
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
			got := RepairToolCallArguments(tt.input)
			if got != tt.want {
				t.Fatalf("RepairToolCallArguments(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
