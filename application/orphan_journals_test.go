package application

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveOrphanJournalSidecars(t *testing.T) {
	dataDir := t.TempDir()
	convDir := filepath.Join(dataDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(convDir, "conv_old.journal")
	if err := os.MkdirAll(filepath.Join(orphan, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(convDir, "conv_old.json")
	if err := os.WriteFile(keep, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := RemoveOrphanJournalSidecars(dataDir); n != 1 {
		t.Fatalf("removed = %d, want 1", n)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan journal still present: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("conversation json removed: %v", err)
	}
	if n := RemoveOrphanJournalSidecars(dataDir); n != 0 {
		t.Fatalf("second pass removed = %d, want 0", n)
	}
}
