// Package atomicfile provides the single canonical atomic file replace for
// NusaShell: a unique temp file in the destination directory, fsync, chmod,
// rename, and cleanup on every failure path. Concurrent writes to the same
// path are serialized per path so writers cannot collide on a temp name.
package atomicfile

import (
	"os"
	"path/filepath"
	"sync"
)

type writer struct {
	mu sync.Mutex
}

var (
	writersMu sync.Mutex
	writers   = make(map[string]*writer)
)

// Write atomically replaces path with data, preserving the destination's
// directory entry: unique temp file in the same directory, fsync, chmod,
// rename, cleanup on failure. Concurrent writes to the same path are
// serialized per path. Parent directories are created when needed.
func Write(path string, data []byte, perm os.FileMode) error {
	return write(path, data, perm, os.Rename)
}

// WriteWithRename is Write with a caller-supplied rename step, for call
// sites that wrap os.Rename — e.g. the file tools' bounded retry around
// transient Windows sharing violations.
func WriteWithRename(path string, data []byte, perm os.FileMode, rename func(from, to string) error) error {
	return write(path, data, perm, rename)
}

func write(path string, data []byte, perm os.FileMode, rename func(from, to string) error) error {
	writersMu.Lock()
	w, ok := writers[path]
	if !ok {
		w = &writer{}
		writers[path] = w
	}
	writersMu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nusashell-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() {
		_ = os.Remove(name)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		cleanup()
		return err
	}
	if err := rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
