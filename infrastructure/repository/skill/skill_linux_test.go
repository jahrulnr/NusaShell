//go:build linux

package reposkill

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO parked at a SKILL.md path must not stall discovery — os.ReadFile
// would block the hydration path that runs before every provider call.
func TestDiscoveryRejectsFIFOSkillWithoutBlocking(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	dir := filepath.Join(root, "pipe")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "SKILL.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		catalog, err := NewLocal("", "", "").List(t.Context(), workspace)
		if err != nil {
			done <- err
			return
		}
		if len(catalog.Skills) != 0 {
			t.Error("FIFO listed as a skill")
		}
		if len(catalog.Diagnostics) != 1 {
			t.Errorf("diagnostics = %v, want one entry for the FIFO", catalog.Diagnostics)
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("skill discovery blocked on a FIFO SKILL.md")
	}
}
