// Package repothreadview implements drepo.ThreadViews on the application
// database (see infrastructure/repository/store).
package repothreadview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Repository stores thread views in the thread_views table.
type Repository struct {
	db  *sql.DB
	now func() time.Time
}

var _ drepo.ThreadViews = (*Repository)(nil)

// New wraps an opened application database.
func New(db *sql.DB) *Repository { return &Repository{db: db, now: time.Now} }

func (r *Repository) Get(ctx context.Context, key string) (json.RawMessage, time.Time, error) {
	var doc string
	var at int64
	err := r.db.QueryRowContext(ctx, `SELECT document, updated_at FROM thread_views WHERE key = ?`, key).Scan(&doc, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, apperror.NewNotFoundError("view of "+key, err)
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	return json.RawMessage(doc), time.UnixMilli(at), nil
}

func (r *Repository) Put(ctx context.Context, key string, doc json.RawMessage) (time.Time, error) {
	at := r.now()
	_, err := r.db.ExecContext(ctx, `INSERT INTO thread_views (key, document, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET document = excluded.document, updated_at = excluded.updated_at`,
		key, string(doc), at.UnixMilli())
	return at, err
}

func (r *Repository) Delete(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM thread_views WHERE key = ?`, key)
	return err
}

// Purge drops every stored view, including the fresh-room document under
// the "" key.
func (r *Repository) Purge(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM thread_views`)
	return err
}
