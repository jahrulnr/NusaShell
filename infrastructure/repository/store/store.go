// Package store opens the application database: one SQLite file holding
// the relational state that is not a conversation transcript (accounts,
// sessions, and later channels, projects, automations). It owns the file
// mechanics — privacy, WAL, one connection, and schema migration — and
// nothing about what the tables mean; repositories built on the handle do.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// Open opens (creating when missing) the database at path — a file or
// ":memory:" — and applies every migration in migrations newer than the
// file's user_version. The handle uses one connection: SQLite serializes
// writers anyway, and one connection keeps WAL and pragmas coherent.
func Open(ctx context.Context, path string, migrations fs.FS) (*sql.DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*sql.DB, error) {
		_ = db.Close()
		return nil, err
	}
	for _, pragma := range []string{`PRAGMA busy_timeout = 5000`, `PRAGMA foreign_keys = ON`, `PRAGMA journal_mode = WAL`} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fail(fmt.Errorf("%s: %w", pragma, err))
		}
	}
	if path != ":memory:" {
		// Account and session rows are credentials; the WAL and shm
		// sidecars inherit the database file's mode.
		if err := os.Chmod(path, 0o600); err != nil {
			return fail(err)
		}
	}
	if err := migrate(ctx, db, migrations); err != nil {
		return fail(err)
	}
	return db, nil
}

// migrate applies, in order and each in its own transaction, the files
// whose number exceeds user_version. A database newer than the binary is
// refused rather than run against a schema this code does not know.
func migrate(ctx context.Context, db *sql.DB, migrations fs.FS) error {
	entries, err := fs.ReadDir(migrations, ".")
	if err != nil {
		return err
	}
	type file struct {
		version int
		name    string
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v < 1 {
			return fmt.Errorf("migration %q: name must be NNNN_description.sql", e.Name())
		}
		files = append(files, file{v, e.Name()})
	}
	slices.SortFunc(files, func(a, b file) int { return a.version - b.version })
	for i, f := range files {
		if f.version != i+1 {
			return fmt.Errorf("migration %q: expected number %d", f.name, i+1)
		}
	}
	var current int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return err
	}
	if current > len(files) {
		return fmt.Errorf("database schema version %d is newer than this binary knows (%d)", current, len(files))
	}
	for _, f := range files[current:] {
		body, err := fs.ReadFile(migrations, f.name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", f.name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, f.version)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
