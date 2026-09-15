package sqlitestore

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"nusashell/domain"

	_ "modernc.org/sqlite"
)

// PairingStore implements application.PairingStore on SQLite. Only hashes
// of pairing codes and session tokens are stored; plaintext values are
// never persisted. Follows the same owner-only file pattern as the
// credential store.
type PairingStore struct {
	db *sql.DB
}

// NewPairingStore opens (or creates) the pairing SQLite database.
func NewPairingStore(path string) (*PairingStore, error) {
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
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS pairing_challenges (
			id           TEXT PRIMARY KEY,
			code_hash    TEXT NOT NULL,
			state        TEXT NOT NULL,
			device_label TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL,
			expires_at   TEXT NOT NULL,
			decided_at   TEXT
		);
		CREATE TABLE IF NOT EXISTS pairing_sessions (
			id           TEXT PRIMARY KEY,
			token_hash   TEXT UNIQUE NOT NULL,
			device_label TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			expires_at   TEXT NOT NULL,
			revoked      INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_pairing_sessions_token_hash ON pairing_sessions(token_hash);
	`); err != nil {
		db.Close()
		return nil, err
	}
	// Revoked sessions are terminal and should not accumulate in the pairing
	// database. Remove rows written by older versions that retained them.
	if _, err := db.Exec(`DELETE FROM pairing_sessions WHERE revoked <> 0`); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return &PairingStore{db: db}, nil
}

func (s *PairingStore) Close() error { return s.db.Close() }

// ---- challenges ----

func (s *PairingStore) SaveChallenge(c *domain.PairingChallenge) error {
	var decidedAt *string
	if c.DecidedAt != nil {
		t := c.DecidedAt.Format(time.RFC3339)
		decidedAt = &t
	}
	_, err := s.db.Exec(`
		INSERT INTO pairing_challenges (id, code_hash, state, device_label, created_at, expires_at, decided_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			code_hash = excluded.code_hash,
			state = excluded.state,
			device_label = excluded.device_label,
			expires_at = excluded.expires_at,
			decided_at = excluded.decided_at`,
		c.ID, c.CodeHash, string(c.State), c.DeviceLabel,
		c.CreatedAt.Format(time.RFC3339),
		c.ExpiresAt.Format(time.RFC3339),
		decidedAt,
	)
	return err
}

func (s *PairingStore) GetChallenge(id string) (*domain.PairingChallenge, error) {
	var (
		ch        domain.PairingChallenge
		state     string
		decidedAt sql.NullString
		createdAt string
		expiresAt string
	)
	err := s.db.QueryRow(
		`SELECT id, code_hash, state, device_label, created_at, expires_at, decided_at FROM pairing_challenges WHERE id = ?`,
		id,
	).Scan(&ch.ID, &ch.CodeHash, &state, &ch.DeviceLabel, &createdAt, &expiresAt, &decidedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ch.State = domain.PairingState(state)
	ch.CreatedAt = parseTime(createdAt)
	ch.ExpiresAt = parseTime(expiresAt)
	if decidedAt.Valid {
		t := parseTime(decidedAt.String)
		ch.DecidedAt = &t
	}
	return &ch, nil
}

// ---- sessions ----

func (s *PairingStore) SaveSession(sess *domain.PairingSession) error {
	revoked := 0
	if sess.Revoked {
		revoked = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO pairing_sessions (id, token_hash, device_label, created_at, last_seen_at, expires_at, revoked)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			token_hash = excluded.token_hash,
			device_label = excluded.device_label,
			last_seen_at = excluded.last_seen_at,
			expires_at = excluded.expires_at,
			revoked = excluded.revoked`,
		sess.ID, sess.TokenHash, sess.DeviceLabel,
		sess.CreatedAt.Format(time.RFC3339),
		sess.LastSeenAt.Format(time.RFC3339),
		sess.ExpiresAt.Format(time.RFC3339),
		revoked,
	)
	return err
}

func (s *PairingStore) GetSessionByTokenHash(hash string) (*domain.PairingSession, error) {
	var (
		sess       domain.PairingSession
		revoked    int
		createdAt  string
		lastSeenAt string
		expiresAt  string
	)
	err := s.db.QueryRow(
		`SELECT id, token_hash, device_label, created_at, last_seen_at, expires_at, revoked FROM pairing_sessions WHERE token_hash = ?`,
		hash,
	).Scan(&sess.ID, &sess.TokenHash, &sess.DeviceLabel, &createdAt, &lastSeenAt, &expiresAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.CreatedAt = parseTime(createdAt)
	sess.LastSeenAt = parseTime(lastSeenAt)
	sess.ExpiresAt = parseTime(expiresAt)
	sess.Revoked = revoked != 0
	return &sess, nil
}

func (s *PairingStore) ListSessions() ([]*domain.PairingSession, error) {
	rows, err := s.db.Query(
		`SELECT id, token_hash, device_label, created_at, last_seen_at, expires_at, revoked FROM pairing_sessions ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []*domain.PairingSession
	for rows.Next() {
		var (
			sess       domain.PairingSession
			revoked    int
			createdAt  string
			lastSeenAt string
			expiresAt  string
		)
		if err := rows.Scan(&sess.ID, &sess.TokenHash, &sess.DeviceLabel, &createdAt, &lastSeenAt, &expiresAt, &revoked); err != nil {
			return nil, err
		}
		sess.CreatedAt = parseTime(createdAt)
		sess.LastSeenAt = parseTime(lastSeenAt)
		sess.ExpiresAt = parseTime(expiresAt)
		sess.Revoked = revoked != 0
		sessions = append(sessions, &sess)
	}
	return sessions, rows.Err()
}

// TouchSession updates only last_seen_at. Deliberately narrow: a
// ValidateSession touch racing RevokeSession cannot recreate a deleted row.
func (s *PairingStore) TouchSession(id string, lastSeenAt time.Time) error {
	_, err := s.db.Exec(`UPDATE pairing_sessions SET last_seen_at = ? WHERE id = ?`,
		lastSeenAt.Format(time.RFC3339), id)
	return err
}

// RevokeSession removes a single session. Deletion is the terminal state for
// a paired device, so revoked sessions do not accumulate in storage or UI.
func (s *PairingStore) RevokeSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM pairing_sessions WHERE id = ?`, id)
	return err
}

// RevokeAllSessions removes every paired session in one DELETE.
func (s *PairingStore) RevokeAllSessions() error {
	_, err := s.db.Exec(`DELETE FROM pairing_sessions`)
	return err
}

// ErrNotFound is returned when a challenge or session is not found.
var ErrNotFound = errors.New("pairing: not found")

// parseTime parses an RFC3339 timestamp, returning the zero value on error.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
