package reporecording

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

// Purge stops a live recorder first — its meta lands finalized — then
// empties the root. The store stays usable afterwards.
func TestPurge(t *testing.T) {
	root := t.TempDir() + "/recordings"
	store := NewStore(root, slog.Default())
	defer store.Shutdown()

	w, err := store.Start("s1", "c1", "default", "local", func(s string) string { return s })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		// A live recording outliving its threads is finalized by the
		// purge, not abandoned mid-write.
		<-w.(*Recorder).Done()
		close(done)
	}()

	if err := store.Purge(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("live recorder was not finalized by purge")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("recording root holds %d entries: %v", len(entries), err)
	}
	if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("root mode = %v: %v", info.Mode().Perm(), err)
	}
	if got := store.List(""); len(got) != 0 {
		t.Fatalf("list after purge = %d", len(got))
	}
	if m := w.Meta(); m.Status != "interrupted" && m.Status != "completed" {
		t.Fatalf("stopped recorder status = %q", m.Status)
	}
}
