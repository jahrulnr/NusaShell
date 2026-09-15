package domain

import (
	"testing"
	"time"
)

func TestDefaultSettingsDisableRemoteAccess(t *testing.T) {
	settings := DefaultSettings()
	if settings.RemoteAccessEnabled {
		t.Fatal("RemoteAccessEnabled = true, want false")
	}
	if len(settings.RemoteAccessAddresses) != 0 {
		t.Fatalf("RemoteAccessAddresses = %v, want empty", settings.RemoteAccessAddresses)
	}
}

func TestNormalizeRemoteAccessAddressesTrimsAndDeduplicates(t *testing.T) {
	got := NormalizeRemoteAccessAddresses([]string{
		" https://shell.example ",
		"",
		"https://shell.example",
		"http://192.168.1.5:10994",
	})
	want := []string{"https://shell.example", "http://192.168.1.5:10994"}
	if len(got) != len(want) {
		t.Fatalf("normalized addresses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized addresses = %v, want %v", got, want)
		}
	}
}

func TestValidateRemoteAccessAddress(t *testing.T) {
	tests := []struct {
		name string
		url  string
		ok   bool
	}{
		{name: "https hostname", url: "https://shell.example:10994", ok: true},
		{name: "http tunnel", url: "http://127.0.0.1:9000/base", ok: true},
		{name: "missing scheme", url: "shell.example:10994", ok: false},
		{name: "missing host", url: "https://", ok: false},
		{name: "credentials", url: "https://user:pass@shell.example", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRemoteAccessAddress(tt.url)
			if (err == nil) != tt.ok {
				t.Fatalf("ValidateRemoteAccessAddress(%q) error = %v, want ok=%v", tt.url, err, tt.ok)
			}
		})
	}
}

func TestNewPairingChallenge(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := NewPairingChallenge("pair_abc", "hashed-code", 5*time.Minute, now)
	if ch.ID != "pair_abc" {
		t.Fatalf("ID = %q, want pair_abc", ch.ID)
	}
	if ch.CodeHash != "hashed-code" {
		t.Fatalf("CodeHash = %q", ch.CodeHash)
	}
	if ch.State != PairingStatePending {
		t.Fatalf("State = %q, want %q", ch.State, PairingStatePending)
	}
	if !ch.ExpiresAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("ExpiresAt = %v, want %v", ch.ExpiresAt, now.Add(5*time.Minute))
	}
}

func TestPairingChallengeIsExpired(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := NewPairingChallenge("pair_abc", "h", 5*time.Minute, now)
	if ch.IsExpired(now) {
		t.Fatal("challenge should not be expired at creation time")
	}
	// At exactly ExpiresAt the challenge is expired (at-or-after semantics).
	if !ch.IsExpired(ch.ExpiresAt) {
		t.Fatal("challenge should be expired at exactly ExpiresAt")
	}
	// One nanosecond before expiry is still valid.
	if ch.IsExpired(ch.ExpiresAt.Add(-1 * time.Nanosecond)) {
		t.Fatal("challenge should not be expired one nanosecond before ExpiresAt")
	}
	if !ch.IsExpired(now.Add(6 * time.Minute)) {
		t.Fatal("challenge should be expired after expiry")
	}
}

func TestPairingChallengeApproveTransitions(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := NewPairingChallenge("pair_abc", "h", 5*time.Minute, now)
	if err := ch.Approve(now); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if ch.State != PairingStateApproved {
		t.Fatalf("State = %q, want %q", ch.State, PairingStateApproved)
	}
	// Already approved → cannot approve again
	if err := ch.Approve(now); err != ErrPairingChallengeNotPending {
		t.Fatalf("second approve err = %v, want %v", err, ErrPairingChallengeNotPending)
	}
}

func TestPairingChallengeRejectTransitions(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := NewPairingChallenge("pair_abc", "h", 5*time.Minute, now)
	if err := ch.Reject(now); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if ch.State != PairingStateRejected {
		t.Fatalf("State = %q, want %q", ch.State, PairingStateRejected)
	}
	if err := ch.Reject(now); err != ErrPairingChallengeNotPending {
		t.Fatalf("second reject err = %v, want %v", err, ErrPairingChallengeNotPending)
	}
}

func TestPairingChallengeMarkUsedTransitions(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := NewPairingChallenge("pair_abc", "h", 5*time.Minute, now)
	// MarkUsed requires approved state first.
	if err := ch.MarkUsed(now); err != ErrPairingChallengeNotPending {
		t.Fatalf("mark used before approve err = %v, want %v", err, ErrPairingChallengeNotPending)
	}
	if err := ch.Approve(now); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := ch.MarkUsed(now); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	if ch.State != PairingStateUsed {
		t.Fatalf("State = %q, want %q", ch.State, PairingStateUsed)
	}
	if err := ch.MarkUsed(now); err != ErrPairingChallengeNotPending {
		t.Fatalf("second mark used err = %v, want %v", err, ErrPairingChallengeNotPending)
	}
}

func TestPairingChallengeExpiredCannotTransition(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := NewPairingChallenge("pair_abc", "h", 5*time.Minute, now)
	later := now.Add(6 * time.Minute)
	if err := ch.Approve(later); err != ErrPairingChallengeExpired {
		t.Fatalf("approve expired err = %v, want %v", err, ErrPairingChallengeExpired)
	}
	if err := ch.Reject(later); err != ErrPairingChallengeExpired {
		t.Fatalf("reject expired err = %v, want %v", err, ErrPairingChallengeExpired)
	}
	// MarkUsed checks expiry first, so an expired pending challenge returns
	// the expired error before the state check.
	if err := ch.MarkUsed(later); err != ErrPairingChallengeExpired {
		t.Fatalf("mark used expired err = %v, want %v", err, ErrPairingChallengeExpired)
	}
}

func TestPairingChallengeExpiredAtBoundaryCannotTransition(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := NewPairingChallenge("pair_abc", "h", 5*time.Minute, now)
	// At exactly ExpiresAt the challenge is expired (at-or-after semantics),
	// so no transition is allowed.
	if err := ch.Approve(ch.ExpiresAt); err != ErrPairingChallengeExpired {
		t.Fatalf("approve at boundary err = %v, want %v", err, ErrPairingChallengeExpired)
	}
	if err := ch.Reject(ch.ExpiresAt); err != ErrPairingChallengeExpired {
		t.Fatalf("reject at boundary err = %v, want %v", err, ErrPairingChallengeExpired)
	}
}

func TestPairingSessionIsValid(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := NewPairingSession("sess_abc", "token-hash", "My Phone", 24*time.Hour, now)
	if !s.IsValid(now) {
		t.Fatal("session should be valid at creation")
	}
	if s.IsValid(now.Add(25 * time.Hour)) {
		t.Fatal("session should be invalid after expiry")
	}
	s.Revoked = true
	if s.IsValid(now) {
		t.Fatal("revoked session should be invalid")
	}
}

func TestPairingSessionTouch(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := NewPairingSession("sess_abc", "token-hash", "My Phone", 24*time.Hour, now)
	later := now.Add(1 * time.Hour)
	s.Touch(later)
	if !s.LastSeenAt.Equal(later) {
		t.Fatalf("LastSeenAt = %v, want %v", s.LastSeenAt, later)
	}
}

func TestPairingSessionRevoke(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := NewPairingSession("sess_abc", "token-hash", "My Phone", 24*time.Hour, now)
	s.Revoke()
	if !s.Revoked {
		t.Fatal("session should be revoked")
	}
	if s.IsValid(now) {
		t.Fatal("revoked session should not be valid")
	}
}
