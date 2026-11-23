package repository

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Picks keeps the marks the owner made on a thread's live page until a
// message carries them, so a composer draft survives a restart.
type Picks interface {
	// Save assigns the pick its id and stores it.
	Save(ctx context.Context, p valueobject.Pick) (valueobject.Pick, error)
	// Get is a 404 apperror for an id that was never saved.
	Get(ctx context.Context, id string) (valueobject.Pick, error)
	// Purge removes every stored pick — called only once the threads
	// that owned them are gone.
	Purge(ctx context.Context) error
}
