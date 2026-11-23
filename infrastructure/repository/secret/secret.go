// Package reposecret implements the SecretStore port on sqlite. Values are
// sealed with AES-256-GCM under a key kept in secrets.key beside the
// database (0600), with the entry's name as associated data, so a copied
// database or backup alone reveals nothing and a value cannot be moved
// to another name.
package reposecret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Store is a sqlite-backed SecretStore.
type Store struct {
	db   *sql.DB
	aead cipher.AEAD
}

// New opens (and migrates) the store at path — a file or ":memory:".
// A missing parent is created private; an existing one belongs to the
// caller and is never chmodded — it may be a shared mount, /tmp, or a CI
// workspace whose access other processes rely on. Sidecar privacy does
// not need the parent: SQLite creates the rollback journal with the
// database file's own mode, and the db is clamped 0600 before any value
// is written, so every journal that can carry secrets inherits 0600 too.
func New(path string) (*Store, error) {
	var key []byte
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		var err error
		if key, err = loadKey(filepath.Join(filepath.Dir(path), "secrets.key")); err != nil {
			return nil, err
		}
	} else {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer keeps the file coherent
	// Busy timeout so a short-lived writer (provider add --key-stdin)
	// retries while another process's store holds the write lock, instead of
	// failing instantly with SQLITE_BUSY. The first statement also
	// materializes the db file.
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, err
	}
	if path != ":memory:" {
		// Values are sealed, but keys and metadata are not — clamp the db, so a
		// pre-existing 0644 file is fixed too. This runs before the
		// migration below: the journal inherits the db's mode at
		// creation, so even the schema transaction's sidecar is 0600.
		if err := os.Chmod(path, 0o600); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS secrets (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, aead: aead}, nil
}

// loadKey reads the sealing key, creating it (0600, never overwritten) on
// first use.
func loadKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			return loadKey(path) // another process made it first
		}
		if err != nil {
			return nil, err
		}
		_, err = f.WriteString(base64.RawStdEncoding.EncodeToString(key))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return key, err
	}
	if err != nil {
		return nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("invalid secret key at %s", path)
	}
	return key, nil
}

// seal encrypts value for name: base64(nonce || ciphertext).
func (s *Store) seal(name, value string) string {
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return base64.RawStdEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(value), []byte(name)))
}

// open reverses seal; a value sealed under another key or name fails.
func (s *Store) open(name, sealed string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	n := s.aead.NonceSize()
	if err != nil || len(raw) < n {
		return "", errSealed
	}
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(name))
	if err != nil {
		return "", errSealed
	}
	return string(plain), nil
}

var errSealed = errors.New("the stored value is not sealed with this server's secrets.key")

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }
