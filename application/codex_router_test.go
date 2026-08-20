package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCodexAccountRouter_Sticky(t *testing.T) {
	r := NewCodexAccountRouter()
	accounts := []string{"acc-a", "acc-b", "acc-c"}

	// First pick — any available account
	picked := r.PickAccount("conv-1", "prov-1", accounts)
	if picked == "" {
		t.Fatal("expected non-empty account")
	}

	// Second pick for same conversation — must return same account (sticky)
	picked2 := r.PickAccount("conv-1", "prov-1", accounts)
	if picked2 != picked {
		t.Fatalf("sticky: got %s, want %s", picked2, picked)
	}

	// Different conversation — may pick any account
	picked3 := r.PickAccount("conv-2", "prov-1", accounts)
	if picked3 == "" {
		t.Fatal("expected non-empty account for conv-2")
	}
}

func TestCodexAccountRouter_Failover(t *testing.T) {
	r := NewCodexAccountRouter()
	accounts := []string{"acc-a", "acc-b"}

	// Pick for conv-1
	picked := r.PickAccount("conv-1", "prov-1", accounts)
	if picked == "" {
		t.Fatal("expected non-empty account")
	}

	// Mark it rate-limited
	r.MarkRateLimited(picked, 5*time.Minute)

	// Next pick must return a different account (failover)
	picked2 := r.PickAccount("conv-1", "prov-1", accounts)
	if picked2 == "" {
		t.Fatal("expected failover to a different account")
	}
	if picked2 == picked {
		t.Fatalf("failover: got same rate-limited account %s", picked2)
	}
}

func TestCodexAccountRouter_CooldownExpiry(t *testing.T) {
	r := NewCodexAccountRouter()
	accounts := []string{"acc-a", "acc-b"}

	picked := r.PickAccount("conv-1", "prov-1", accounts)
	r.MarkRateLimited(picked, 50*time.Millisecond)

	// Immediately after — should failover
	picked2 := r.PickAccount("conv-1", "prov-1", accounts)
	if picked2 == picked {
		t.Fatal("should have failed over while cooldown active")
	}

	// After cooldown expires — original account should be available again
	time.Sleep(60 * time.Millisecond)
	picked3 := r.PickAccount("conv-2", "prov-1", accounts)
	if picked3 == "" {
		t.Fatal("expected non-empty account after cooldown")
	}
}

func TestCodexAccountRouter_NoAccounts(t *testing.T) {
	r := NewCodexAccountRouter()
	if got := r.PickAccount("conv-1", "prov-1", nil); got != "" {
		t.Fatalf("expected empty, got %s", got)
	}
}

func TestCodexAccountRouter_StickyAccount(t *testing.T) {
	r := NewCodexAccountRouter()
	accounts := []string{"acc-a"}

	picked := r.PickAccount("conv-1", "prov-1", accounts)
	if got := r.StickyAccount("conv-1"); got != picked {
		t.Fatalf("StickyAccount: got %s, want %s", got, picked)
	}
	if got := r.StickyAccount("unknown"); got != "" {
		t.Fatalf("StickyAccount for unknown conv: got %s, want empty", got)
	}
}

func TestIsRateLimitError(t *testing.T) {
	if isRateLimitError(nil) {
		t.Fatal("nil should not be rate limit")
	}
	if isRateLimitError(context.Canceled) {
		t.Fatal("context.Canceled should not be rate limit")
	}
	err := &UpstreamError{StatusCode: 429, Err: errors.New("rate limited")}
	if !isRateLimitError(err) {
		t.Fatal("429 should be rate limit")
	}
	err2 := &UpstreamError{StatusCode: 500, Err: errors.New("server error")}
	if isRateLimitError(err2) {
		t.Fatal("500 should not be rate limit")
	}
}

func TestIsContextOverflowError(t *testing.T) {
	if isContextOverflowError(nil) {
		t.Fatal("nil should not be context overflow")
	}
	// 400 with context_length_exceeded body
	overflowErr := &UpstreamError{StatusCode: 400, Err: errors.New(`{"error":{"message":"This model's maximum context length is 262144 tokens","type":"BadRequestError","code":400}}`)}
	if !isContextOverflowError(overflowErr) {
		t.Fatal("400 with 'maximum context length' should be context overflow")
	}
	// 400 with context_length_exceeded
	overflowErr2 := &UpstreamError{StatusCode: 400, Err: errors.New("context_length_exceeded")}
	if !isContextOverflowError(overflowErr2) {
		t.Fatal("400 with 'context_length_exceeded' should be context overflow")
	}
	// 400 with reduce prompt
	overflowErr3 := &UpstreamError{StatusCode: 400, Err: errors.New("Please reduce the length of the input prompt")}
	if !isContextOverflowError(overflowErr3) {
		t.Fatal("400 with 'reduce the length of the input prompt' should be context overflow")
	}
	// 400 unrelated
	badRequestErr := &UpstreamError{StatusCode: 400, Err: errors.New("invalid model")}
	if isContextOverflowError(badRequestErr) {
		t.Fatal("400 unrelated should not be context overflow")
	}
	// 500 should not match
	serverErr := &UpstreamError{StatusCode: 500, Err: errors.New("maximum context length")}
	if isContextOverflowError(serverErr) {
		t.Fatal("500 should not be context overflow even with matching body")
	}
}

func TestRateLimitCooldown(t *testing.T) {
	// With Retry-After
	err := &UpstreamError{StatusCode: 429, RetryAfter: 2 * time.Minute, Err: errors.New("rate limited")}
	if got := rateLimitCooldown(err); got != 2*time.Minute {
		t.Fatalf("cooldown: got %s, want 2m", got)
	}
	// Without Retry-After — defaults to retryAfterCutoff (5m)
	err2 := &UpstreamError{StatusCode: 429, Err: errors.New("rate limited")}
	if got := rateLimitCooldown(err2); got != retryAfterCutoff {
		t.Fatalf("cooldown default: got %s, want %s", got, retryAfterCutoff)
	}
}

// ---- Circuit breaker tests ----

func TestCodexAccountRouter_CircuitOpen(t *testing.T) {
	r := NewCodexAccountRouter()
	accounts := []string{"acc-a", "acc-b"}

	picked := r.PickAccount("conv-1", "prov-1", accounts)
	if picked == "" {
		t.Fatal("expected non-empty account")
	}

	// Open circuit for the picked account (usage exhausted)
	r.MarkCircuitOpen(picked, time.Now().Add(2*time.Hour))

	// Next pick must failover to the other account
	picked2 := r.PickAccount("conv-1", "prov-1", accounts)
	if picked2 == "" {
		t.Fatal("expected failover to a different account")
	}
	if picked2 == picked {
		t.Fatalf("circuit open: got same blocked account %s", picked2)
	}

	// CircuitOpenUntil should report ~2h from now
	until := r.CircuitOpenUntil(picked)
	if until.IsZero() {
		t.Fatal("CircuitOpenUntil should not be zero for open circuit")
	}
	if !until.After(time.Now().Add(1 * time.Hour)) {
		t.Fatalf("CircuitOpenUntil should be >1h from now, got %s", until)
	}
}

func TestCodexAccountRouter_CircuitReset(t *testing.T) {
	r := NewCodexAccountRouter()
	accounts := []string{"acc-a"}

	r.MarkCircuitOpen("acc-a", time.Now().Add(1*time.Hour))
	if r.CircuitOpenUntil("acc-a").IsZero() {
		t.Fatal("circuit should be open")
	}

	r.ResetCircuit("acc-a")
	if !r.CircuitOpenUntil("acc-a").IsZero() {
		t.Fatal("circuit should be closed after reset")
	}

	// Account should be pickable again
	picked := r.PickAccount("conv-1", "prov-1", accounts)
	if picked != "acc-a" {
		t.Fatalf("after reset: got %s, want acc-a", picked)
	}
}

func TestCodexAccountRouter_CircuitExpiry(t *testing.T) {
	r := NewCodexAccountRouter()
	accounts := []string{"acc-a"}

	// Open circuit for very short duration
	r.MarkCircuitOpen("acc-a", time.Now().Add(50*time.Millisecond))
	if r.PickAccount("conv-1", "prov-1", accounts) != "" {
		t.Fatal("should not pick circuit-open account")
	}

	// After expiry — account available again
	time.Sleep(60 * time.Millisecond)
	picked := r.PickAccount("conv-1", "prov-1", accounts)
	if picked != "acc-a" {
		t.Fatalf("after circuit expiry: got %s, want acc-a", picked)
	}
}

func TestCodexAccountRouter_AllBlocked(t *testing.T) {
	r := NewCodexAccountRouter()
	accounts := []string{"acc-a", "acc-b"}

	// Block all accounts
	r.MarkCircuitOpen("acc-a", time.Now().Add(1*time.Hour))
	r.MarkCircuitOpen("acc-b", time.Now().Add(30*time.Minute))

	res := r.PickAccountDetailed("conv-1", "prov-1", accounts)
	if res.AccountID != "" {
		t.Fatalf("expected empty, got %s", res.AccountID)
	}
	if !res.AllRateLimited {
		t.Fatal("AllRateLimited should be true")
	}
	if res.EarliestReset.IsZero() {
		t.Fatal("EarliestReset should not be zero")
	}
	// acc-b resets first (30min < 1h)
	if !res.EarliestReset.Before(time.Now().Add(31 * time.Minute)) {
		t.Fatalf("EarliestReset should be ~30min, got %s", res.EarliestReset)
	}
}

func TestCodexAccountRouter_CircuitDoesNotShorten(t *testing.T) {
	r := NewCodexAccountRouter()
	// Open circuit for 2 hours
	r.MarkCircuitOpen("acc-a", time.Now().Add(2*time.Hour))
	first := r.CircuitOpenUntil("acc-a")

	// Try to shorten to 1 hour — should be ignored
	r.MarkCircuitOpen("acc-a", time.Now().Add(1*time.Hour))
	second := r.CircuitOpenUntil("acc-a")

	if second.Before(first) {
		t.Fatalf("circuit should not shorten: first=%s second=%s", first, second)
	}
}

func TestCodexAccountRouter_StickyCleanupWhenAccountDeleted(t *testing.T) {
	r := NewCodexAccountRouter()
	accounts := []string{"acc-a", "acc-b"}

	// Pick for conv-1 — sticks to acc-a
	picked := r.PickAccount("conv-1", "prov-1", accounts)
	if picked != "acc-a" {
		t.Fatalf("expected acc-a, got %s", picked)
	}

	// acc-a is deleted (removed from available list)
	accounts = []string{"acc-b"}
	picked2 := r.PickAccount("conv-1", "prov-1", accounts)
	if picked2 != "acc-b" {
		t.Fatalf("expected failover to acc-b, got %s", picked2)
	}

	// Sticky should now point to acc-b, not acc-a
	if got := r.StickyAccount("conv-1"); got != "acc-b" {
		t.Fatalf("sticky should be acc-b after cleanup, got %s", got)
	}
}
