package domain

import (
	"testing"
	"time"
)

func TestDefaultRateLimitWindow(t *testing.T) {
	if DefaultRateLimitWindow != time.Minute {
		t.Fatalf("DefaultRateLimitWindow = %v, want 1m", DefaultRateLimitWindow)
	}
}
