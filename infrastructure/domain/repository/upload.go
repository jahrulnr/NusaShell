package repository

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Uploads keeps files the owner attaches to a message before it is sent.
// Each upload is one file under its own id; the attachment it yields
// points at the stored bytes and pins their sha256.
type Uploads interface {
	Save(ctx context.Context, name, mediaType string, data []byte) (id string, a valueobject.Attachment, err error)
	// Get is a 404 apperror for an id that was never saved.
	Get(ctx context.Context, id string) (valueobject.Attachment, error)
	// Read returns the stored bytes and media type; a 404 apperror for an
	// id that was never saved.
	Read(ctx context.Context, id string) (data []byte, mediaType string, err error)
	// Purge removes every stored upload — called only once the rooms that
	// referenced them are gone.
	Purge(ctx context.Context) error
}
