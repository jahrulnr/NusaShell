package ai

import (
	"net/http"
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
