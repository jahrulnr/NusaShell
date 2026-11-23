package reposecret

import (
	"context"
	"database/sql"
	"errors"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Get returns the unsealed value — 404 for an unknown key.
func (s *Store) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM secrets WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apperror.NewNotFoundError("secret "+key, err)
	}
	if err != nil {
		return "", err
	}
	// A value that does not open (written before sealing, or under a
	// lost secrets.key) is as good as absent: callers treat it like a
	// missing key, so the owner can set it again.
	plain, err := s.open(key, v)
	if err != nil {
		return "", apperror.NewNotFoundError("secret "+key+" (it cannot be opened with this secrets.key; set it again)", err)
	}
	return plain, nil
}
