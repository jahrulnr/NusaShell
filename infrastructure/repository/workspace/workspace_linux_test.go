//go:build linux

package repoworkspace

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenRegularRejectsFIFOWithoutBlocking(t *testing.T) {
	rootPath := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(rootPath, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := NewLocal().Open(t.Context(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if _, _, err := root.OpenRegular("pipe"); err == nil {
		t.Fatal("FIFO must be rejected as non-regular media")
	}
}

// OpenHost opens absolute paths outside the workspace root — a blocking
// open on a FIFO would park the caller before the regular-file check
// ever ran.
func TestOpenHostRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		f, _, err := NewLocal().OpenHost(t.Context(), path)
		if err == nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO opened as a host file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OpenHost blocked on a FIFO")
	}
}

// WriteFile is the workspace write path — a blocking create-open on a
// FIFO waits for a reader that may never come.
func TestWriteFileRejectsFIFOWithoutBlocking(t *testing.T) {
	rootPath := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(rootPath, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := NewLocal().Open(t.Context(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	done := make(chan error, 1)
	go func() { done <- root.WriteFile("pipe", []byte("x"), 0o600) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as a write target")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WriteFile blocked on a FIFO")
	}
}
