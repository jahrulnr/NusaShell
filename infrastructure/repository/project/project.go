// Package repoproject implements the Project repository port on the
// application database (see infrastructure/repository/store).
package repoproject

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Repository is the SQLite Project store.
type Repository struct{ db *sql.DB }

// New wraps an opened application database.
func New(db *sql.DB) *Repository { return &Repository{db: db} }

var _ drepo.Project = (*Repository)(nil)

// Timestamps are stored in Unix nanoseconds: Update compares them for the
// stale-read check, so they must round-trip exactly.
func (r *Repository) Create(ctx context.Context, p *aggregate.Project) error {
	folders, err := json.Marshal(p.Folders())
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO projects (id, name, folders, pinned, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID(), p.Name(), string(folders), p.Pinned(), p.CreatedAt().UnixNano(), p.UpdatedAt().UnixNano())
	if err != nil {
		if _, getErr := r.Get(ctx, p.ID()); getErr == nil {
			return apperror.New(409, "project already exists", err)
		}
	}
	return err
}

func (r *Repository) Update(ctx context.Context, p *aggregate.Project, readAt time.Time) error {
	folders, err := json.Marshal(p.Folders())
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE projects SET name = ?, folders = ?, pinned = ?, updated_at = ? WHERE id = ? AND updated_at = ?`,
		p.Name(), string(folders), p.Pinned(), p.UpdatedAt().UnixNano(), p.ID(), readAt.UnixNano())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := r.Get(ctx, p.ID()); err != nil {
			return err
		}
		return apperror.New(409, "the project was changed elsewhere — reload it and try again", aggregate.ErrProjectChanged)
	}
	return nil
}

func (r *Repository) Get(ctx context.Context, id string) (*aggregate.Project, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, name, folders, pinned, created_at, updated_at FROM projects WHERE id = ?`, id)
	p, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NewNotFoundError("project "+id, err)
	}
	return p, err
}

func (r *Repository) List(ctx context.Context) ([]aggregate.Project, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, folders, pinned, created_at, updated_at FROM projects ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []aggregate.Project
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperror.NewNotFoundError("project "+id, sql.ErrNoRows)
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scan(s scanner) (*aggregate.Project, error) {
	var id, name, foldersJSON string
	var pinned bool
	var created, updated int64
	if err := s.Scan(&id, &name, &foldersJSON, &pinned, &created, &updated); err != nil {
		return nil, err
	}
	var folders []string
	if err := json.Unmarshal([]byte(foldersJSON), &folders); err != nil {
		return nil, fmt.Errorf("project %q corrupt: %w", id, err)
	}
	p, err := aggregate.NewProject(id, name, folders, time.Unix(0, created))
	if err != nil {
		return nil, fmt.Errorf("project %q corrupt: %w", id, err)
	}
	p.SetPinned(pinned)
	p.Restore(time.Unix(0, created), time.Unix(0, updated))
	return p, nil
}
