package jsonstore

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentAtomicWriteSamePath stresses atomicWrite with many concurrent
// writers to the same destination path. A shared fixed temp file name would
// cause renames to collide ("no such file or directory"); a unique temp file
// per write must succeed for every concurrent writer.
func TestConcurrentAtomicWriteSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conv.json")

	const writers = 64
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := atomicWrite(path, []byte(fmt.Sprintf("payload-%d", n))); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent atomicWrite failed: %v", err)
	}
	if b, err := os.ReadFile(path); err != nil {
		t.Fatalf("read final file: %v", err)
	} else {
		t.Logf("final content length %d", len(b))
	}
}
