package domain

import "testing"
import "time"

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
