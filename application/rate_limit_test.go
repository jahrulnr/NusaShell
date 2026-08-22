package application

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMarkProviderRateLimitedAndWait(t *testing.T) {
	a := &App{}
	if w := a.ProviderRateLimitWait("tok"); w != 0 {
		t.Fatalf("fresh provider wait = %v, want 0", w)
	}
	a.MarkProviderRateLimited("tok", time.Now().Add(30*time.Second))
	w := a.ProviderRateLimitWait("tok")
	if w <= 0 || w > 31*time.Second {
		t.Fatalf("wait = %v, want ~30s", w)
	}
	// Expired window clears.
	a.MarkProviderRateLimited("tok", time.Now().Add(-time.Second))
	if w := a.ProviderRateLimitWait("tok"); w != 0 {
		t.Fatalf("expired wait = %v, want 0", w)
	}
}

func TestMarkProviderRateLimitedDefaultsToOneMinute(t *testing.T) {
	a := &App{}
	a.MarkProviderRateLimited("tok", time.Time{})
	w := a.ProviderRateLimitWait("tok")
	if w <= 55*time.Second || w > 61*time.Second {
		t.Fatalf("default wait = %v, want ~1min", w)
	}
}

func TestDecorateRateLimitError(t *testing.T) {
	a := &App{}
	up := &UpstreamError{Kind: KindHTTPStatus, StatusCode: 429, Err: errors.New("rate limit")}
	err := a.decorateRateLimitError("tok", up)
	if err == nil {
		t.Fatal("nil error")
	}
	if !strings.Contains(err.Error(), "rate-limited") || !strings.Contains(err.Error(), "try again") {
		t.Fatalf("friendly message missing: %q", err.Error())
	}
	if w := a.ProviderRateLimitWait("tok"); w <= 0 {
		t.Fatalf("rate-limit window not recorded, wait=%v", w)
	}
}

func TestDecorateRateLimitErrorNon429Untouched(t *testing.T) {
	a := &App{}
	inner := errors.New("boom")
	up := &UpstreamError{Kind: KindHTTPStatus, StatusCode: 500, Err: inner}
	err := a.decorateRateLimitError("tok", up)
	if !errors.Is(err, inner) {
		t.Fatalf("non-429 error must pass through, got %v", err)
	}
	if w := a.ProviderRateLimitWait("tok"); w != 0 {
		t.Fatalf("non-429 must not record window, wait=%v", w)
	}
}
