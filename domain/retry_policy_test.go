package domain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"
)

func TestRetryPolicyConstants(t *testing.T) {
	if MaxProviderAttempts != 5 {
		t.Errorf("MaxProviderAttempts = %d, want 5", MaxProviderAttempts)
	}
	if RetryBaseDelay != 250*time.Millisecond {
		t.Errorf("RetryBaseDelay = %v, want 250ms", RetryBaseDelay)
	}
	if RetryMaxDelay != 4*time.Second {
		t.Errorf("RetryMaxDelay = %v, want 4s", RetryMaxDelay)
	}
	if RetryAfterCutoff != 5*time.Minute {
		t.Errorf("RetryAfterCutoff = %v, want 5m", RetryAfterCutoff)
	}
}

func TestPermanentFailurePhrases(t *testing.T) {
	if len(PermanentFailurePhrases) == 0 {
		t.Fatal("PermanentFailurePhrases must not be empty")
	}
	want := "insufficient balance"
	found := false
	for _, p := range PermanentFailurePhrases {
		if p == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("PermanentFailurePhrases missing %q", want)
	}
}

func TestContextOverflowPhrases(t *testing.T) {
	if len(ContextOverflowPhrases) == 0 {
		t.Fatal("ContextOverflowPhrases must not be empty")
	}
	want := "maximum context length"
	found := false
	for _, p := range ContextOverflowPhrases {
		if p == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ContextOverflowPhrases missing %q", want)
	}
}

func TestIsPermanentProviderFailure(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"402 always permanent", 402, "", true},
		{"503 insufficient balance", 503, "Insufficient Balance for this request", true},
		{"503 internal server error", 503, "internal server error", false},
		{"429 with top up", 429, "please top up your account", true},
		{"500 generic", 500, "internal server error", false},
		{"200 ok body", 200, "insufficient balance", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPermanentProviderFailure(tc.status, tc.body); got != tc.want {
				t.Fatalf("IsPermanentProviderFailure(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

func TestProviderErrorCanAutoRetry(t *testing.T) {
	tests := []struct {
		name string
		err  *ProviderError
		want bool
	}{
		{
			name: "temporary transport failure",
			err:  &ProviderError{Kind: KindSSETransport, Temporary: true, Err: errors.New("unexpected EOF")},
			want: true,
		},
		{
			name: "request timeout",
			err:  &ProviderError{StatusCode: 408},
			want: true,
		},
		{
			name: "short rate limit window",
			err:  &ProviderError{StatusCode: 429, RetryAfter: 30 * time.Second},
			want: true,
		},
		{
			name: "rate limit without retry-after",
			err:  &ProviderError{StatusCode: 429},
			want: false,
		},
		{
			name: "rate limit with long retry-after",
			err:  &ProviderError{StatusCode: 429, RetryAfter: RetryAfterCutoff + time.Second},
			want: false,
		},
		{
			name: "temporary rate limit still needs a retry window",
			err:  &ProviderError{StatusCode: 429, Temporary: true},
			want: false,
		},
		{
			name: "server failure",
			err:  &ProviderError{StatusCode: 503},
			want: true,
		},
		{
			name: "billing failure",
			err:  &ProviderError{StatusCode: 503, Temporary: true, Err: errors.New("insufficient balance")},
			want: false,
		},
		{
			name: "invalid request",
			err:  &ProviderError{StatusCode: 400, Temporary: true},
			want: false,
		},
		{
			name: "structural tpm failure",
			err: &ProviderError{
				StatusCode: 429,
				RetryAfter: 30 * time.Second,
				Err:        errors.New("tokens per min (TPM): Limit 200000, Requested 333331"),
			},
			want: false,
		},
		{
			name: "cancelled request",
			err:  &ProviderError{Temporary: true, Err: context.Canceled},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.CanAutoRetry(); got != tt.want {
				t.Fatalf("CanAutoRetry() = %t, want %t", got, tt.want)
			}
		})
	}
}

type temporaryNetError struct {
	temporary bool
	timeout   bool
}

func (e temporaryNetError) Error() string   { return "temporary network failure" }
func (e temporaryNetError) Temporary() bool { return e.temporary }
func (e temporaryNetError) Timeout() bool   { return e.timeout }

func TestProviderErrorCanAutoRetryRecognizesTransientTransportCauses(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	wrappedReset := &url.Error{Op: "Get", URL: "https://provider.test/stream", Err: reset}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "clean EOF means incomplete stream", err: io.EOF, want: true},
		{name: "unexpected EOF means truncated stream", err: io.ErrUnexpectedEOF, want: true},
		{name: "closed connection", err: net.ErrClosed, want: true},
		{name: "TCP connection reset", err: reset, want: true},
		{name: "wrapped TCP connection reset", err: fmt.Errorf("stream read error: %w", wrappedReset), want: true},
		{name: "connection aborted", err: syscall.ECONNABORTED, want: true},
		{name: "broken pipe", err: syscall.EPIPE, want: true},
		{name: "connection refused", err: syscall.ECONNREFUSED, want: true},
		{name: "network timeout", err: temporaryNetError{timeout: true}, want: true},
		{name: "temporary network error", err: temporaryNetError{temporary: true}, want: true},
		{name: "plain reset text is not enough", err: errors.New("connection reset by peer"), want: false},
		{name: "non-transient syscall", err: syscall.EINVAL, want: false},
		{name: "missing cause", err: nil, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &ProviderError{Kind: KindSSETransport, Err: tc.err}
			if got := err.CanAutoRetry(); got != tc.want {
				t.Fatalf("CanAutoRetry(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

func TestCanAutoRetryRecognizesRawTransientTransportErrors(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, reset, net.ErrClosed} {
		if !CanAutoRetry(err) {
			t.Fatalf("CanAutoRetry(%T: %v) = false, want true", err, err)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("bad request")} {
		if CanAutoRetry(err) {
			t.Fatalf("CanAutoRetry(%T: %v) = true, want false", err, err)
		}
	}
}

func TestCanAutoRetryUnwrapsProviderError(t *testing.T) {
	err := &ProviderError{StatusCode: 503, Err: errors.New("upstream unavailable")}
	if !CanAutoRetry(fmt.Errorf("review provider failed: %w", err)) {
		t.Fatal("CanAutoRetry should inspect wrapped ProviderError")
	}
	if CanAutoRetry(errors.New("plain application error")) {
		t.Fatal("plain errors must not be auto-retried")
	}
	var nilErr *ProviderError
	if nilErr.CanAutoRetry() {
		t.Fatal("nil ProviderError must not be auto-retried")
	}
}
