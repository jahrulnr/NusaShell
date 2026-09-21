package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".nusashell-") && strings.HasSuffix(e.Name(), ".tmp") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := Write(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "hello" {
		t.Fatalf("content = %q, err = %v", b, err)
	}
	if left := tempFiles(t, dir); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestWriteOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "new" {
		t.Fatalf("content = %q, err = %v", b, err)
	}
}

func TestWriteCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "out.txt")
	if err := Write(path, []byte("nested"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "nested" {
		t.Fatalf("content = %q, err = %v", b, err)
	}
}

func TestWriteCleansUpOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	boom := errors.New("rename boom")
	err := WriteWithRename(path, []byte("x"), 0o644, func(from, to string) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write error = %v, want %v", err, boom)
	}
	if left := tempFiles(t, dir); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("destination should not exist after failed rename: %v", err)
	}
}

func TestWriteFailsWhenParentIsFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(filepath.Join(blocker, "out.txt"), []byte("x"), 0o644); err == nil {
		t.Fatal("expected error when parent path is a regular file")
	}
}

func TestWriteConcurrentSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.txt")

	const writersN = 64
	var wg sync.WaitGroup
	errCh := make(chan error, writersN)
	for i := 0; i < writersN; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := Write(path, []byte(fmt.Sprintf("payload-%d", n)), 0o644); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent Write failed: %v", err)
	}
	if left := tempFiles(t, dir); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestWritePreservesPermBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits")
	}
	dir := t.TempDir()
	for _, perm := range []os.FileMode{0o600, 0o644} {
		path := filepath.Join(dir, fmt.Sprintf("out-%o.txt", perm))
		if err := Write(path, []byte("x"), perm); err != nil {
			t.Fatalf("Write(%o): %v", perm, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != perm {
			t.Fatalf("mode = %o, want %o", info.Mode().Perm(), perm)
		}
	}
}
