package service

// MediaDescriptions remembers read_media's descriptions and transcripts on
// disk, one namespace per conversation: they are text lifted from a file, and
// text from one room must never surface in another.
//
// The key is opaque to the store — the reader derives it from the file's
// bytes, the model that read it, and the prompt version, so a changed model or
// prompt is a miss rather than a stale answer. A missing entry is ok=false and
// no error; the reader describes the file again.
type MediaDescriptions interface {
	// Get returns a stored description, and marks the entry as used so the
	// room's trim drops the least-recently-used answers first.
	Get(convID, key string) (text string, ok bool, err error)
	// Put stores one description, atomically and within the room's budget.
	Put(convID, key, text string) error
	// Purge forgets one room's descriptions — a deleted room leaves no text
	// behind.
	Purge(convID string) error
	// PurgeAll forgets every room's descriptions, alongside the other stores a
	// delete-all empties.
	PurgeAll() error
}
