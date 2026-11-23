package repository

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
)

// BrowserLogin stores saved browser logins. A login is unique by profile,
// origin, and username; saving one that exists refreshes its password.
type BrowserLogin interface {
	// Save stores l with password and returns the stored login.
	Save(ctx context.Context, l *aggregate.BrowserLogin, password string) (*aggregate.BrowserLogin, error)
	// List returns a profile's logins ("" for every profile), newest first.
	List(ctx context.Context, profile string) ([]aggregate.BrowserLogin, error)
	Get(ctx context.Context, id string) (*aggregate.BrowserLogin, error)
	// Password returns a login's password.
	Password(ctx context.Context, id string) (string, error)
	Delete(ctx context.Context, id string) error
}
