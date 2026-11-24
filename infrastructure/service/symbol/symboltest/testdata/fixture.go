package store

import "io"

// Store keeps key/value pairs on disk.
type Store struct {
	path string
}

// Closer releases a resource.
type Closer interface {
	io.Closer
}

// OpenStore creates a Store rooted at path.
func OpenStore(path string) *Store {
	return &Store{path: path}
}

// Close flushes and closes the store.
func (s *Store) Close() error {
	return nil
}

type cache struct{}

func helper() {}
