// Package sqlitestore implements the credential persistence port on SQLite
// (pure-Go modernc driver; no cgo). Credentials are the only data that must
// not live in the JSON/JSONL files.
package sqlitestore

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	clock "nusashell/pkg/time"

	_ "modernc.org/sqlite"
)

// CredentialStore stores API keys per provider, keyed by provider id.
type CredentialStore struct {
	db *sql.DB
}

func NewCredentials(path string) (*CredentialStore, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		_ = os.Chmod(dir, 0o700)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Single local writer; WAL keeps reads cheap and writes safe.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS credentials (
			provider_id TEXT PRIMARY KEY,
			api_key     TEXT NOT NULL,
			updated_at  TEXT NOT NULL
		);`); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return &CredentialStore{db: db}, nil
}

func (c *CredentialStore) Get(providerID string) (string, bool, error) {
	var key, updated string
	err := c.db.QueryRow(
		`SELECT api_key, updated_at FROM credentials WHERE provider_id = ?`, providerID,
	).Scan(&key, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return key, true, nil
}

func (c *CredentialStore) Set(providerID, key string) error {
	_, err := c.db.Exec(`
		INSERT INTO credentials (provider_id, api_key, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(provider_id) DO UPDATE SET api_key = excluded.api_key, updated_at = excluded.updated_at`,
		providerID, key, clock.NewTime().RFC3339(),
	)
	return err
}

func (c *CredentialStore) Delete(providerID string) error {
	_, err := c.db.Exec(`DELETE FROM credentials WHERE provider_id = ?`, providerID)
	return err
}

// ListByPrefix returns all provider IDs that start with the given prefix.
// Used by Codex multi-account support to enumerate accounts stored under
// "{providerID}:account:{accountID}" keys.
func (c *CredentialStore) ListByPrefix(prefix string) ([]string, error) {
	rows, err := c.db.Query(
		`SELECT provider_id FROM credentials WHERE provider_id LIKE ? ORDER BY provider_id`,
		prefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (c *CredentialStore) Close() error { return c.db.Close() }
