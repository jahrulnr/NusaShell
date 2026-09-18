package jsonstore

import (
	"os"
	"path/filepath"
	"testing"

	"nusashell/domain"
)

const samplePatch = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n"

// TestOperationStoreSaveAndPath pins the per-turn patch location
// (conversations/<id>/operation/<runID>.patch), atomic replacement of the
// same turn, and the empty-diff no-op.
func TestOperationStoreSaveAndPath(t *testing.T) {
	dir := t.TempDir()
	store := NewOperationStore(dir)

	want := filepath.Join(dir, "conversations", "conv_op", "operation", "run_1.patch")
	if got := store.Path("conv_op", "run_1"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if err := store.Save("conv_op", "run_1", samplePatch); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("patch missing at %s: %v", want, err)
	}
	if string(b) != samplePatch {
		t.Fatalf("patch content = %q, want %q", b, samplePatch)
	}

	// Re-saving the same run replaces the file instead of appending.
	if err := store.Save("conv_op", "run_1", "second\n"); err != nil {
		t.Fatalf("Save replace: %v", err)
	}
	b, _ = os.ReadFile(want)
	if string(b) != "second\n" {
		t.Fatalf("patch not replaced: %q", b)
	}

	// A turn without mutations has no operation to record.
	if err := store.Save("conv_op", "run_empty", "  \n"); err != nil {
		t.Fatalf("Save empty: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "conversations", "conv_op", "operation", "run_empty.patch")); !os.IsNotExist(err) {
		t.Fatalf("empty patch must not create a file, got err=%v", err)
	}
}

// TestOperationStoreRejectsUnsafeIDs keeps hostile conversation or run IDs
// from escaping the store root.
func TestOperationStoreRejectsUnsafeIDs(t *testing.T) {
	dir := t.TempDir()
	store := NewOperationStore(dir)

	if err := store.Save("../escape", "run_1", samplePatch); err == nil {
		t.Fatal("Save must reject a traversal conversation id")
	}
	if err := store.Save("conv_op", "../escape", samplePatch); err == nil {
		t.Fatal("Save must reject a traversal run id")
	}
	for _, tc := range [][2]string{{"../escape", "run_1"}, {"conv_op", "run/../x"}, {"", "run_1"}, {"conv_op", ""}} {
		if got := store.Path(tc[0], tc[1]); got != "" {
			t.Errorf("Path(%q, %q) = %q, want empty", tc[0], tc[1], got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written outside conversations/, got err=%v", err)
	}
}

// TestOperationStoreCoexistsWithConversationFolder proves a patch lands
// inside the conversation folder the store already owns (index.jsonl), without
// disturbing it.
func TestOperationStoreCoexistsWithConversationFolder(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := store.Save(sampleConversation("conv_both")); err != nil {
		t.Fatalf("Save conversation: %v", err)
	}
	ops := NewOperationStore(dir)
	if err := ops.Save("conv_both", "run_9", samplePatch); err != nil {
		t.Fatalf("Save patch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "conversations", "conv_both", "index.jsonl")); err != nil {
		t.Fatalf("transcript must survive patch write: %v", err)
	}
	reloaded, err := New(dir)
	if err != nil {
		t.Fatalf("reload New: %v", err)
	}
	if got, err := reloaded.Get("conv_both"); err != nil || got.ID != "conv_both" {
		t.Fatalf("conversation must still load: %+v err=%v", got, err)
	}
}

func sampleConversation(id string) *domain.Conversation {
	return &domain.Conversation{ID: id, Title: id, Status: "idle"}
}
