package repository

import (
	"context"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
)

// Project stores projects. Errors carry the apperror contract — a missing
// id surfaces as 404.
type Project interface {
	// Create inserts a new project; 409 when the id exists.
	Create(ctx context.Context, p *aggregate.Project) error
	// Update replaces a stored project. readAt is the updatedAt the caller's
	// edit was based on; when the stored one differs the write is refused
	// with aggregate.ErrProjectChanged (409).
	Update(ctx context.Context, p *aggregate.Project, readAt time.Time) error
	// Get loads one project.
	Get(ctx context.Context, id string) (*aggregate.Project, error)
	// List returns every project.
	List(ctx context.Context) ([]aggregate.Project, error)
	// Delete removes a project; 404 when absent.
	Delete(ctx context.Context, id string) error
}
