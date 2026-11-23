// Package repobrowserhistory keeps the pages each browser profile visited
// in the application database.
package repobrowserhistory

import (
	"context"
	"database/sql"
	"strings"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// Repository is the history store.
type Repository struct{ db *sql.DB }

// New wraps the application database.
func New(db *sql.DB) *Repository { return &Repository{db: db} }

var _ drepo.BrowserHistory = (*Repository)(nil)

func (r *Repository) Record(ctx context.Context, profile, url, title string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO browser_history (profile, url, title, visits, visited_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT (profile, url) DO UPDATE SET
		  title = CASE WHEN excluded.title = '' THEN browser_history.title ELSE excluded.title END,
		  visits = browser_history.visits + 1,
		  visited_at = excluded.visited_at`, profile, url, title, at.UnixMilli())
	return err
}

func (r *Repository) Search(ctx context.Context, profile, query string, limit int) ([]drepo.BrowserVisit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var rows *sql.Rows
	var err error
	if q == "" {
		rows, err = r.db.QueryContext(ctx, `SELECT url, title, visits, visited_at FROM browser_history
			WHERE profile = ? ORDER BY visited_at DESC LIMIT ?`, profile, limit)
	} else {
		// LIKE's wildcards in the query match themselves.
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		// A query matches a url after its scheme and "www." too, so
		// "git" starts "https://github.com/".
		rows, err = r.db.QueryContext(ctx, `SELECT url, title, visits, visited_at FROM browser_history
			WHERE profile = ?1 AND (lower(url) LIKE ?2 ESCAPE '\' OR lower(title) LIKE ?2 ESCAPE '\')
			ORDER BY (lower(url) LIKE ?3 ESCAPE '\' OR lower(url) LIKE 'http://' || ?3 ESCAPE '\' OR lower(url) LIKE 'https://' || ?3 ESCAPE '\'
			  OR lower(url) LIKE 'https://www.' || ?3 ESCAPE '\' OR lower(title) LIKE ?3 ESCAPE '\') DESC,
			  visits DESC, visited_at DESC LIMIT ?4`, profile, "%"+esc+"%", esc+"%", limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []drepo.BrowserVisit
	for rows.Next() {
		var v drepo.BrowserVisit
		var at int64
		if err := rows.Scan(&v.URL, &v.Title, &v.Visits, &at); err != nil {
			return nil, err
		}
		v.VisitedAt = time.UnixMilli(at)
		out = append(out, v)
	}
	return out, rows.Err()
}
