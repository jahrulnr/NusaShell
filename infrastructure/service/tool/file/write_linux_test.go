//go:build linux

package toolfile

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A blocking create-open on a FIFO waits for a writer that may never
// come — write_file must reject the special file instead of parking
// the call (and the turn) forever.
func TestFileWriteRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := mustWriteTool(t, root)
	done := make(chan error, 1)
	go func() {
		_, err := runWrite(t, tool, `{"path":"pipe","content":"x"}`)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as write target")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write_file blocked on a FIFO")
	}
}
