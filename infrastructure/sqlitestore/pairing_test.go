package sqlitestore

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"nusashell/domain"
)

func TestRevokeSessionDeletesSession(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPairingStore(filepath.Join(dir, "pairing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	now := time.Now()
	sess := domain.NewPairingSession("psess_1", "hash1", "Phone", 30*24*time.Hour, now)
	if err := store.SaveSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSessionByTokenHash("hash1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked session lookup error = %v, want ErrNotFound", err)
	}
	list, err := store.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("sessions = %d, want 0 after revoke", len(list))
	}
}

// TestRevokeAllSessions verifies revoke-all removes every session.
func TestRevokeAllSessions(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPairingStore(filepath.Join(dir, "pairing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	now := time.Now()
	for _, id := range []string{"psess_a", "psess_b"} {
		sess := domain.NewPairingSession(id, "hash"+id, "dev", 30*24*time.Hour, now)
		if err := store.SaveSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RevokeAllSessions(); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("sessions = %d, want 0 after revoke-all", len(list))
	}
}

func TestNewPairingStoreRemovesPreviouslyRevokedSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pairing.db")
	store, err := NewPairingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	active := domain.NewPairingSession("psess_active", "hash-active", "active", 30*24*time.Hour, now)
	revoked := domain.NewPairingSession("psess_revoked", "hash-revoked", "revoked", 30*24*time.Hour, now)
	revoked.Revoke()
	if err := store.SaveSession(active); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(revoked); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewPairingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	list, err := store.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != active.ID {
		t.Fatalf("sessions after startup cleanup = %+v, want only %s", list, active.ID)
	}
	if _, err := store.GetSessionByTokenHash(revoked.TokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy revoked session lookup error = %v, want ErrNotFound", err)
	}
}
