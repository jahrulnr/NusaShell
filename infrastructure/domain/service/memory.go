package service

import "context"

// MemoryIndex renders the index of memories a thread sees — the note the
// harness puts before every model turn. "" means nothing to show.
type MemoryIndex interface {
	Index(ctx context.Context, conversationID string) (string, error)
}
