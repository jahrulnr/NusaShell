package jsonstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nusashell/domain"
)

// Regression: the conversations directory may contain legacy sidecars from
// the retired desktop app (conv_<id>.meta.json with an object "model",
// conv_<id>.runtime.json) plus other stores' files. Loading must ignore
// them — and skip unparsable conversation files — instead of failing
// Store construction.
func TestLoadIgnoresLegacySidecarsAndUnparsableFiles(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ok := &domain.Conversation{ID: "conv_ok", Title: "ok"}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(convDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.MarshalIndent(ok, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write("conv_ok.json", string(b))
	// Legacy desktop-era meta: "model" is an object, not a string.
	write("conv_legacy.meta.json", `{"id":"conv_legacy","title":"legacy","model":{"modelKey":"m","effort":"auto","explicit":true}}`)
	write("conv_legacy.runtime.json", `{"pending":true}`)
	write("conv_broken.json", `{not json`)
	write("todos.json", `[{"id":"t1"}]`)

	s, err := New(dir)
	if err != nil {
		t.Fatalf("New() must survive legacy/unparsable files, got: %v", err)
	}
	convs := s.List()
	if len(convs) != 1 || convs[0].ID != "conv_ok" {
		t.Fatalf("want only conv_ok loaded, got %d conversations", len(convs))
	}
}

// TestSafeSegmentRejectsUnsafeIDs guards the path-segment check shared by the
// conversation and ACP run stores. An id that could escape its directory must
// be rejected instead of silently writing outside the store.
func TestSafeSegmentRejectsUnsafeIDs(t *testing.T) {
	for _, id := range []string{"", "../etc/passwd", "..", "a/b", `..\win`, "conv_\x00x"} {
		if err := safeSegment(id); err == nil {
			t.Errorf("safeSegment(%q) = nil, want an error", id)
		}
	}
	if err := safeSegment("conv_abc123"); err != nil {
		t.Errorf("safeSegment(conv_abc123) = %v, want nil", err)
	}
}

// TestConversationDeleteRemovesSidecars pins the cascade contract: deleting
// a conversation must also remove its .chunks/ archive, .acp/ subagent
// snapshot directory, and the plan directory (conversations/<id>/), while
// leaving sibling conversations and their sidecars untouched.
func TestConversationDeleteRemovesSidecars(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two conversations; the cascade must only touch conv_drop.
	keepID := "conv_keep"
	dropID := "conv_drop"
	conv := &domain.Conversation{ID: keepID, Title: "keep"}
	b, _ := json.MarshalIndent(conv, "", "  ")
	if err := os.WriteFile(filepath.Join(convDir, keepID+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	conv = &domain.Conversation{ID: dropID, Title: "drop"}
	b, _ = json.MarshalIndent(conv, "", "  ")
	if err := os.WriteFile(filepath.Join(convDir, dropID+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	// Sidecars for the drop target: archived chunk, acp snapshot, plan dir.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(convDir, dropID+".chunks"), 0o755))
	must(os.WriteFile(filepath.Join(convDir, dropID+".chunks", "chunk-0.json"), []byte("[]"), 0o644))
	must(os.MkdirAll(filepath.Join(convDir, dropID+".acp"), 0o755))
	must(os.WriteFile(filepath.Join(convDir, dropID+".acp", "acprun_abc.json"), []byte("{}"), 0o644))
	must(os.MkdirAll(filepath.Join(convDir, dropID), 0o755))
	must(os.WriteFile(filepath.Join(convDir, dropID, "plan.md"), []byte("brief"), 0o644))

	// Parallel sidecars for the keep target so we can prove the cascade is
	// scoped to a single conversation.
	must(os.MkdirAll(filepath.Join(convDir, keepID+".chunks"), 0o755))
	must(os.WriteFile(filepath.Join(convDir, keepID+".chunks", "chunk-0.json"), []byte("[]"), 0o644))
	must(os.MkdirAll(filepath.Join(convDir, keepID+".acp"), 0o755))
	must(os.WriteFile(filepath.Join(convDir, keepID+".acp", "acprun_def.json"), []byte("{}"), 0o644))
	must(os.MkdirAll(filepath.Join(convDir, keepID), 0o755))
	must(os.WriteFile(filepath.Join(convDir, keepID, "plan.md"), []byte("keep-brief"), 0o644))

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := store.Delete(dropID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Drop sidecars must be gone.
	for _, p := range []string{
		filepath.Join(convDir, dropID+".json"),
		filepath.Join(convDir, dropID+".chunks", "chunk-0.json"),
		filepath.Join(convDir, dropID+".chunks"),
		filepath.Join(convDir, dropID+".acp", "acprun_abc.json"),
		filepath.Join(convDir, dropID+".acp"),
		filepath.Join(convDir, dropID, "plan.md"),
		filepath.Join(convDir, dropID),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, got err=%v", p, err)
		}
	}

	// Keep sidecars must survive.
	for _, p := range []string{
		filepath.Join(convDir, keepID+".json"),
		filepath.Join(convDir, keepID+".chunks", "chunk-0.json"),
		filepath.Join(convDir, keepID+".acp", "acprun_def.json"),
		filepath.Join(convDir, keepID, "plan.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to survive sibling delete, got err=%v", p, err)
		}
	}

	if _, err := store.Get(dropID); err == nil {
		t.Errorf("expected drop conversation to be gone from in-memory map")
	}
}

// TestConversationDeleteRejectsUnsafeIDs guards the cascade from path
// traversal: a hostile id like "../escape" must be refused before any
// RemoveAll, so a malicious RPC cannot wipe directories outside the store.
func TestConversationDeleteRejectsUnsafeIDs(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing target outside the store: the cascade must not touch it.
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := &Store{dir: dir, conversations: map[string]*domain.Conversation{}}
	for _, id := range []string{"", "../escape", "a/b", `..\win`, "conv_\x00x"} {
		if err := store.Delete(id); err == nil {
			t.Errorf("Delete(%q) = nil, want an error", id)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("sentinel outside store was deleted: %v", err)
	}
}

func TestConversationPathIsAbsoluteAndSafe(t *testing.T) {
	store := &Store{dir: "relative-data"}
	root, err := filepath.Abs(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "conversations", "conv_abc123.json")
	if got := store.ConversationPath("conv_abc123"); got != want {
		t.Fatalf("ConversationPath() = %q, want %q", got, want)
	}
	for _, id := range []string{"", "../escape", "a/b", `..\win`, "conv_\x00x"} {
		if got := store.ConversationPath(id); got != "" {
			t.Errorf("ConversationPath(%q) = %q, want empty", id, got)
		}
	}
}
