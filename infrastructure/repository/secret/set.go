package reposecret

import (
	"context"
	"time"
)

// Set seals value and upserts it under key; created_at survives updates.
func (s *Store) Set(ctx context.Context, key, value string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO secrets (key, value, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, s.seal(key, value), now, now)
	return err
}
