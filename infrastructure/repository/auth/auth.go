// Package repoauth implements the Auth repository port on the application
// database (see infrastructure/repository/store).
package repoauth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Repository is the SQLite Auth store.
type Repository struct{ db *sql.DB }

// New wraps an opened application database.
func New(db *sql.DB) *Repository { return &Repository{db: db} }

var _ drepo.Auth = (*Repository)(nil)

func (r *Repository) OwnerPasswordHash(ctx context.Context) (string, error) {
	var hash string
	err := r.db.QueryRowContext(ctx, `SELECT password_hash FROM owner WHERE id = 1`).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apperror.NewNotFoundError("owner password", err)
	}
	return hash, err
}

func (r *Repository) SetOwnerPasswordHash(ctx context.Context, hash string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO owner (id, password_hash, updated_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		hash, time.Now().Unix())
	return err
}

func (r *Repository) SaveSession(ctx context.Context, s valueobject.Session) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, csrf_token, created_at, last_seen_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(token_hash) DO UPDATE SET last_seen_at = excluded.last_seen_at, expires_at = excluded.expires_at`,
		s.TokenHash(), s.CSRF(), s.CreatedAt().Unix(), s.LastSeen().Unix(), s.ExpiresAt().Unix())
	return err
}

func (r *Repository) Session(ctx context.Context, tokenHash string) (valueobject.Session, error) {
	var csrf string
	var created, seen, expires int64
	err := r.db.QueryRowContext(ctx, `SELECT csrf_token, created_at, last_seen_at, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&csrf, &created, &seen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return valueobject.Session{}, apperror.NewNotFoundError("session", err)
	}
	if err != nil {
		return valueobject.Session{}, err
	}
	return valueobject.NewSession(tokenHash, csrf, time.Unix(created, 0), time.Unix(seen, 0), time.Unix(expires, 0))
}

func (r *Repository) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (r *Repository) DeleteSessions(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

func (r *Repository) DeleteExpiredSessions(ctx context.Context, now int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now)
	return err
}
