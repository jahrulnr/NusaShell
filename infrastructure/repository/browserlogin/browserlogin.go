// Package repobrowserlogin stores saved browser logins: the record in the
// application database, the password in the secret store.
package repobrowserlogin

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Repository is the login store.
type Repository struct {
	db      *sql.DB
	secrets drepo.SecretStore
}

// New wraps the application database and the secret store.
func New(db *sql.DB, secrets drepo.SecretStore) *Repository {
	return &Repository{db: db, secrets: secrets}
}

var _ drepo.BrowserLogin = (*Repository)(nil)

const cols = `id, profile, origin, username, created_at, updated_at`

func key(id string) string { return "browser-login/" + id }

func (r *Repository) Save(ctx context.Context, l *aggregate.BrowserLogin, password string) (*aggregate.BrowserLogin, error) {
	if password == "" {
		return nil, apperror.New(400, "a saved login needs a password", nil)
	}
	// One login per profile, origin, and username: an existing one keeps
	// its id and gets the new password.
	row := r.db.QueryRowContext(ctx, `SELECT `+cols+` FROM browser_logins WHERE profile = ? AND origin = ? AND username = ?`,
		l.Profile(), l.Origin(), l.Username())
	if old, err := scan(row); err == nil {
		old.Touch(l.UpdatedAt())
		if _, err := r.db.ExecContext(ctx, `UPDATE browser_logins SET updated_at = ? WHERE id = ?`, old.UpdatedAt().UnixNano(), old.ID()); err != nil {
			return nil, err
		}
		return old, r.secrets.Set(ctx, key(old.ID()), password)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO browser_logins (`+cols+`) VALUES (?, ?, ?, ?, ?, ?)`,
		l.ID(), l.Profile(), l.Origin(), l.Username(), l.CreatedAt().UnixNano(), l.UpdatedAt().UnixNano()); err != nil {
		return nil, err
	}
	return l, r.secrets.Set(ctx, key(l.ID()), password)
}

func (r *Repository) List(ctx context.Context, profile string) ([]aggregate.BrowserLogin, error) {
	q, args := `SELECT `+cols+` FROM browser_logins ORDER BY updated_at DESC`, []any{}
	if profile != "" {
		q, args = `SELECT `+cols+` FROM browser_logins WHERE profile = ? ORDER BY updated_at DESC`, []any{profile}
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []aggregate.BrowserLogin
	for rows.Next() {
		l, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

func (r *Repository) Get(ctx context.Context, id string) (*aggregate.BrowserLogin, error) {
	l, err := scan(r.db.QueryRowContext(ctx, `SELECT `+cols+` FROM browser_logins WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NewNotFoundError("saved login "+id, err)
	}
	return l, err
}

func (r *Repository) Password(ctx context.Context, id string) (string, error) {
	return r.secrets.Get(ctx, key(id))
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM browser_logins WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperror.NewNotFoundError("saved login "+id, sql.ErrNoRows)
	}
	if err := r.secrets.Delete(ctx, key(id)); err != nil && apperror.As(err).StatusCode() != 404 {
		return err
	}
	return nil
}

func scan(row interface{ Scan(...any) error }) (*aggregate.BrowserLogin, error) {
	var id, profile, origin, username string
	var created, updated int64
	if err := row.Scan(&id, &profile, &origin, &username, &created, &updated); err != nil {
		return nil, err
	}
	l, err := aggregate.NewBrowserLogin(id, profile, origin, username, time.Unix(0, created))
	if err != nil {
		return nil, err
	}
	l.Restore(time.Unix(0, created), time.Unix(0, updated))
	return l, nil
}
