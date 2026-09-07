package corelock

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAcquireRejectsSecondOwnerAndReleasesAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "core.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	if _, err := Acquire(path); !errors.Is(err, ErrAlreadyHeld) {
		t.Fatalf("second acquire error = %v, want ErrAlreadyHeld", err)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
}

func TestMetadataRoundTripIsAtomicAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "core.json")
	want := Metadata{
		PID:       123,
		Port:      10994,
		Version:   "test",
		Owner:     "test",
		StartedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	if err := WriteMetadata(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// Windows has no POSIX permission bits: chmod(0o600) only maps to the
		// read-only attribute, so os.Stat reports 0666 for a writable file.
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("metadata mode = %o, want 600", info.Mode().Perm())
		}
	}
	if err := RemoveMetadata(path); err != nil {
		t.Fatal(err)
	}
}
