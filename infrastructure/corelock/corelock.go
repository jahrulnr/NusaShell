// Package corelock provides the process-level ownership lock and diagnostic
// metadata for one NusaShell core per data directory.
package corelock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var ErrAlreadyHeld = errors.New("another NusaShell core already owns the data directory")

// Lock is an OS-held exclusive lock. The lock file may remain on disk after a
// crash; ownership is the kernel lock, not file existence.
type Lock struct {
	file *os.File
}

// Acquire takes an exclusive non-blocking lock at path. The parent directory
// is created with a private mode before the lock is attempted.
func Acquire(path string) (*Lock, error) {
	if path == "" {
		return nil, fmt.Errorf("core lock path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create core lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open core lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("chmod core lock: %w", err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrAlreadyHeld) {
			return nil, err
		}
		return nil, fmt.Errorf("acquire core lock: %w", err)
	}
	return &Lock{file: file}, nil
}

// Release unlocks and closes the lock. The lock file itself is intentionally
// retained so stale metadata/file presence can never block a future owner.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// Metadata is the diagnostic identity of the running core. It is not an
// ownership mechanism and may be stale after an ungraceful exit.
type Metadata struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	Version   string    `json:"version"`
	Owner     string    `json:"owner"`
	StartedAt time.Time `json:"started_at"`
}

// WriteMetadata atomically writes metadata with private permissions.
func WriteMetadata(path string, metadata Metadata) error {
	if path == "" {
		return fmt.Errorf("core metadata path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create core metadata directory: %w", err)
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode core metadata: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "core-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create core metadata temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod core metadata: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write core metadata: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close core metadata: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install core metadata: %w", err)
	}
	return nil
}

// RemoveMetadata removes diagnostic metadata if it still belongs to metadata.
// The caller must already own the OS lock; a missing file is harmless.
func RemoveMetadata(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
