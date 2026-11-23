package repository

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Auth stores the owner's password hash and login sessions. Errors carry
// the apperror contract — a missing owner or session surfaces as 404.
type Auth interface {
	// OwnerPasswordHash returns the stored hash; 404 when none is set.
	OwnerPasswordHash(ctx context.Context) (string, error)
	// SetOwnerPasswordHash replaces the hash.
	SetOwnerPasswordHash(ctx context.Context, hash string) error
	// SaveSession inserts or updates a session by token hash.
	SaveSession(ctx context.Context, s valueobject.Session) error
	// Session loads a session by token hash; 404 when absent.
	Session(ctx context.Context, tokenHash string) (valueobject.Session, error)
	// DeleteSession removes one session; absent is not an error.
	DeleteSession(ctx context.Context, tokenHash string) error
	// DeleteSessions removes every session.
	DeleteSessions(ctx context.Context) error
	// DeleteExpiredSessions removes sessions expired at or before unix
	// seconds now.
	DeleteExpiredSessions(ctx context.Context, now int64) error
}
