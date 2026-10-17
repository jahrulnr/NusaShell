package application

import (
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/infrastructure/sqlitestore"
)

// newPairingTestService builds a PairingService on a real SQLite store with
// an adjustable clock. advance moves the clock forward so expiry can be
// exercised deterministically.
func newPairingTestService(t *testing.T) (*PairingService, func(d time.Duration)) {
	t.Helper()
	store, err := sqlitestore.NewPairingStore(t.TempDir() + "/pairing.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	svc := NewPairingService(store)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	return svc, func(d time.Duration) { now = now.Add(d) }
}

// pairDevice drives the full challenge → approve → exchange flow and returns
// the issued session token hash.
func pairDevice(t *testing.T, svc *PairingService, label string) string {
	t.Helper()
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, label); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	exchangeRes, rpcErr := svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	return hashToken(exchangeRes.Token)
}

// TestSessionExpired covers the persisted-but-expired lookup the auth
// middleware uses to answer PAIRING_SESSION_EXPIRED: a row past expiry is
// expired, a live row is not, and unknown/empty input never reports expired.
func TestSessionExpired(t *testing.T) {
	svc, advance := newPairingTestService(t)

	// Nil store (pairing not configured) never reports expired.
	if (&PairingService{}).SessionExpired("x") {
		t.Fatal("nil store must not report expired")
	}
	// Empty token hash never reports expired.
	if svc.SessionExpired("") {
		t.Fatal("empty hash must not report expired")
	}
	// A hash with no persisted row (never issued or already revoked) is not
	// "expired" — it is simply unknown.
	if svc.SessionExpired(hashToken("never-issued")) {
		t.Fatal("unknown hash must not report expired")
	}

	hash := pairDevice(t, svc, "Phone")
	if svc.SessionExpired(hash) {
		t.Fatal("live session must not report expired")
	}

	// Advancing past the 30-day TTL leaves the row persisted but invalid;
	// expiry uses the same at-or-after-ExpiresAt boundary as IsValid.
	advance(PairingSessionTTL)
	if !svc.SessionExpired(hash) {
		t.Fatal("persisted session past ExpiresAt must report expired")
	}
	if _, ok := svc.ValidateSession(hash); ok {
		t.Fatal("expired session must not validate")
	}
}

// TestSessionExpired_AfterRevoke verifies a revoked session does not report
// expired: revocation deletes the row, so the lookup misses and the client
// gets the generic PAIRING_REQUIRED copy.
func TestSessionExpired_AfterRevoke(t *testing.T) {
	svc, _ := newPairingTestService(t)
	hash := pairDevice(t, svc, "Phone")
	sess, ok := svc.ValidateSession(hash)
	if !ok || sess == nil {
		t.Fatal("session should be valid before revoke")
	}
	if rpcErr := svc.RevokeSession(sess.ID); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if svc.SessionExpired(hash) {
		t.Fatal("revoked session must not report expired — the row is deleted")
	}
}
