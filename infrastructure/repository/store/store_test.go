package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jahrulnr/nusashell/migration"
)

func TestOpenAppliesMigrationsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db", "nusashell.db")
	ctx := context.Background()
	db, err := Open(ctx, path, migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version < 1 {
		t.Fatalf("user_version = %d, %v", version, err)
	}
	var mode string
	_ = db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	if mode != "wal" {
		t.Fatalf("journal_mode = %q", mode)
	}
	db.Close()
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("db mode = %v, %v", fi.Mode().Perm(), err)
	}
	// Reopening must not re-run applied files.
	db, err = Open(ctx, path, migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
}

func TestOpenRefusesNewerSchemaAndGaps(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "x.db")
	two := fstest.MapFS{
		"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)},
		"0002_b.sql": {Data: []byte(`CREATE TABLE b (id INTEGER);`)},
	}
	db, err := Open(ctx, path, two)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	one := fstest.MapFS{"0001_a.sql": two["0001_a.sql"]}
	if _, err := Open(ctx, path, one); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("older binary err = %v", err)
	}
	gap := fstest.MapFS{"0001_a.sql": two["0001_a.sql"], "0003_c.sql": {Data: []byte(`SELECT 1;`)}}
	if _, err := Open(ctx, filepath.Join(t.TempDir(), "y.db"), gap); err == nil {
		t.Fatal("a numbering gap must be refused")
	}
}
