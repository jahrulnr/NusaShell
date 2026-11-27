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
	// Get is not found, with a stable error identity, for an id that was never saved.
	Get(ctx context.Context, id string) (valueobject.Attachment, error)
	// Read returns the stored bytes and media type; not found, with a stable
	// error identity, for an id that was never saved.
	Read(ctx context.Context, id string) (data []byte, mediaType string, err error)
	// Delete removes one stored upload — a copy a refused send made and never
	// handed over. It is idempotent: an id that no longer exists is already the
	// requested state and answers nil, which is what a cleanup path needs (a
	// retried refusal must not fail on its own earlier cleanup). An id that is
	// not an upload id at all is not found — that caller asked for the wrong thing.
	// Purging instead would take every room's files with it.
	Delete(ctx context.Context, id string) error
	// Purge removes every stored upload — called only once the rooms that
	// referenced them are gone.
	Purge(ctx context.Context) error
}
