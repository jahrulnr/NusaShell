package reposecret

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"

	_ "modernc.org/sqlite"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSetGetDelete(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if err := s.Set(ctx, "provider/x/api_key", "sk-live"); err != nil {
		t.Fatal(err)
	}
	v, err := s.Get(ctx, "provider/x/api_key")
	if err != nil {
		t.Fatal(err)
	}
	if v != "sk-live" {
		t.Fatalf("Get = %q", v)
	}

	// Upsert overwrites.
	if err := s.Set(ctx, "provider/x/api_key", "sk-rotated"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get(ctx, "provider/x/api_key"); v != "sk-rotated" {
		t.Fatalf("overwrite = %q", v)
	}

	if err := s.Delete(ctx, "provider/x/api_key"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "provider/x/api_key"); apperror.As(err).StatusCode() != apperror.StatusNotFound {
		t.Fatal("deleted key still resolves")
	}
}

func TestNotFound(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	if _, err := s.Get(ctx, "nope"); apperror.As(err).StatusCode() != apperror.StatusNotFound {
		t.Fatalf("Get missing: %v", err)
	}
	if err := s.Delete(ctx, "nope"); apperror.As(err).StatusCode() != apperror.StatusNotFound {
		t.Fatalf("Delete missing: %v", err)
	}
}

// SQLite creates the rollback journal beside the db and gives it the db
// file's own mode — a crash mid-commit leaves <db>-journal behind with
// plaintext secret values. The store clamps the db 0600 before any value
// is written, so the journal inherits 0600 and stays private even when
// the db sits in a caller-owned loose parent that New must not touch.
func TestJournalSidecarCoveredByDirPerms(t *testing.T) {
	dir := t.TempDir()
	// The caller owns this directory — a shared 0755 parent must survive
	// the open exactly as the caller left it.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "secrets.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Drive a real write transaction on a second connection and hold it
	// open so the rollback journal exists on disk.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO secrets (key, value, created_at, updated_at) VALUES ('probe','SECRET-VALUE',1,1)`); err != nil {
		t.Fatal(err)
	}
	journal := dbPath + "-journal"
	jfi, statErr := os.Stat(journal)
	if statErr != nil {
		_ = tx.Rollback()
		t.Skipf("no -journal file materialized on this driver/filesystem: %v", statErr)
	}
	_ = tx.Rollback()

	if jfi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("journal mode = %o — a sidecar carrying secret values must be owner-only", jfi.Mode().Perm())
	}
	dfi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dfi.Mode().Perm() != 0o755 {
		t.Fatalf("pre-existing parent mode changed to %o", dfi.Mode().Perm())
	}
}

// A pre-existing loose parent is opened as-is: Mutating a caller-owned
// directory breaks a shared parent (a mount root, /tmp, a CI workspace)
// for every other process. Privacy does not depend on the parent — the
// db and every journal it spawns are clamped 0600.
func TestPreExistingParentModeUntouched(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := New(filepath.Join(dir, "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("pre-existing parent mode changed to %o", fi.Mode().Perm())
	}
}

// Values are sealed at rest under secrets.key and bound to their name: the
// file never holds the plaintext, a value moved to another name or read
// with another key does not open, and the key survives a reopen.
func TestValuesAreSealed(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "secrets.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := s.Set(ctx, "a", "sk-very-secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "b", "other"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	raw, _ := os.ReadFile(dbPath)
	if strings.Contains(string(raw), "sk-very-secret") {
		t.Fatal("plaintext in the database file")
	}
	if fi, err := os.Stat(filepath.Join(dir, "secrets.key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi, err)
	}

	s, err = New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(ctx, "a"); err != nil || v != "sk-very-secret" {
		t.Fatalf("reopen Get = %q, %v", v, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE secrets SET value = (SELECT value FROM secrets WHERE key = 'a') WHERE key = 'b'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "b"); err == nil {
		t.Fatal("a value moved to another name opened")
	}
	s.Close()

	if err := os.Remove(filepath.Join(dir, "secrets.key")); err != nil {
		t.Fatal(err)
	}
	s, err = New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Get(ctx, "a"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("a value under another key: %v", err)
	}
}
