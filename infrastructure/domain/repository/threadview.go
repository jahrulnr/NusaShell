package repository

import (
	"context"
	"encoding/json"
	"time"
)

// ThreadViews keeps the owner's view of each thread across reloads and
// devices: one opaque document per key, written by the WebUI. Errors
// carry the apperror contract — a key with no document surfaces as 404.
type ThreadViews interface {
	// Get returns the stored document and when it was written.
	Get(ctx context.Context, key string) (json.RawMessage, time.Time, error)
	// Put replaces the document and returns its write time.
	Put(ctx context.Context, key string, doc json.RawMessage) (time.Time, error)
	// Delete forgets the document; a missing one is not an error.
	Delete(ctx context.Context, key string) error
	// Purge removes every stored view — called only once the threads
	// they belong to are gone. The fresh-room view under the "" key
	// goes with them.
	Purge(ctx context.Context) error
}
