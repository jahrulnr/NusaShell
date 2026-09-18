package jsonstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestConversationSaveWritesIndexJSONL pins the transcript format: one
// metadata record (no transcript) followed by one record per message, and a
// reload through a fresh store returns the same conversation.
func TestConversationSaveWritesIndexJSONL(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	conv := &domain.Conversation{
		ID:        "conv_jsonl",
		Title:     "round trip",
		Model:     "prov_x:model-y",
		Status:    "idle",
		CreatedAt: time.Now().UTC().Add(-time.Hour),
		UpdatedAt: time.Now().UTC(),
		Messages: []domain.Message{
			{ID: "msg_1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			{ID: "msg_2", Role: domain.RoleAssistant, Content: "hai", Status: domain.StatusDone},
		},
	}
	if err := store.Save(conv); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, "conversations", conv.ID, "index.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("index.jsonl missing: %v", err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatal("index.jsonl must end with a newline")
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1+len(conv.Messages) {
		t.Fatalf("index.jsonl lines = %d, want 1 header + %d messages", len(lines), len(conv.Messages))
	}
	var header map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("header line is not JSON: %v", err)
	}
	if _, ok := header["Messages"]; ok {
		t.Fatalf("header line must not carry the transcript: %s", lines[0])
	}
	if _, ok := header["Title"]; !ok {
		t.Fatalf("header line must carry the metadata: %s", lines[0])
	}
	var msg domain.Message
	if err := json.Unmarshal([]byte(lines[1]), &msg); err != nil {
		t.Fatalf("message line is not JSON: %v", err)
	}
	if msg.ID != "msg_1" || msg.Content != "halo" {
		t.Fatalf("message line mismatch: %+v", msg)
	}
	// The retired flat transcript must not be written next to the folder.
	if _, err := os.Stat(filepath.Join(dir, "conversations", conv.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("flat %s.json must not exist, got err=%v", conv.ID, err)
	}

	reloaded, err := New(dir)
	if err != nil {
		t.Fatalf("reload New: %v", err)
	}
	got, err := reloaded.Get(conv.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.Title != conv.Title || got.Model != conv.Model || len(got.Messages) != 2 {
		t.Fatalf("reloaded conversation mismatch: %+v", got)
	}
	if got.Messages[0].ID != "msg_1" || got.Messages[1].Content != "hai" {
		t.Fatalf("reloaded messages mismatch: %+v", got.Messages)
	}
}

// TestLoadIgnoresFlatSiblingsAndSkipsTornTranscriptLines guards two failure
// modes: unrelated flat files in conversations/ must not be parsed as
// conversations, and one unparsable message line must not hide the rest of
// the transcript.
func TestLoadIgnoresFlatSiblingsAndSkipsTornTranscriptLines(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	mustWrite(t, filepath.Join(convDir, "conv_ok", "index.jsonl"), strings.Join([]string{
		`{"ID":"conv_ok","Title":"ok","Status":"idle"}`,
		`{"ID":"msg_1","Role":"user","Content":"first"}`,
		`{"ID":"msg_torn","Role":"assistant",`, // crash-torn tail line
		`{"ID":"msg_2","Role":"assistant","Content":"second"}`,
	}, "\n")+"\n")
	// Flat files owned by other stores and by the retired desktop app.
	mustWrite(t, filepath.Join(convDir, "todos.json"), `[{"id":"t1"}]`)
	mustWrite(t, filepath.Join(convDir, "conv_ghost.meta.json"), `{"id":"conv_ghost"}`)
	mustWrite(t, filepath.Join(convDir, "acp_runs.jsonl.imported"), `{"id":"acprun_x"}`)

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() must survive unrelated flat files, got: %v", err)
	}
	convs := store.List()
	if len(convs) != 1 || convs[0].ID != "conv_ok" {
		t.Fatalf("want only conv_ok loaded, got %d conversations", len(convs))
	}
	if len(convs[0].Messages) != 2 {
		t.Fatalf("torn line must be skipped without dropping the rest: %+v", convs[0].Messages)
	}
	if convs[0].Messages[1].ID != "msg_2" {
		t.Fatalf("messages out of order after torn line: %+v", convs[0].Messages)
	}
}

// TestConversationFolderIDMismatchIsSkipped keeps a corrupted folder from
// being loaded and then re-saved into a second, divergent folder.
func TestConversationFolderIDMismatchIsSkipped(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	mustWrite(t, filepath.Join(convDir, "conv_folder", "index.jsonl"),
		`{"ID":"conv_other","Title":"mismatch","Status":"idle"}`+"\n")

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("mismatched folder must be skipped, got %+v", got)
	}
}

// TestArchiveChunkWritesUnderConversationChunkDir pins the chunk/ sidecar
// path and the round trip.
func TestArchiveChunkWritesUnderConversationChunkDir(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	msgs := []domain.Message{{ID: "msg_1", Role: domain.RoleUser, Content: "archived"}}
	idx, err := store.ArchiveChunk("conv_chunks", msgs)
	if err != nil {
		t.Fatalf("ArchiveChunk: %v", err)
	}
	if idx != 0 {
		t.Fatalf("first chunk index = %d, want 0", idx)
	}
	path := filepath.Join(dir, "conversations", "conv_chunks", "chunk", "chunk-0.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("chunk not at %s: %v", path, err)
	}
	got, err := store.GetChunk("conv_chunks", 0)
	if err != nil {
		t.Fatalf("GetChunk: %v", err)
	}
	if len(got) != 1 || got[0].Content != "archived" {
		t.Fatalf("chunk round trip mismatch: %+v", got)
	}
	if _, err := store.GetChunk("conv_chunks", 7); err == nil {
		t.Fatal("missing chunk must return an error")
	}
}

// TestSafeSegmentRejectsUnsafeIDs guards the path-segment check shared by the
// conversation, ACP run, and operation stores. An id that could escape its
// directory must be rejected instead of silently writing outside the store.
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

// TestConversationSaveRejectsUnsafeIDs keeps a hostile id from writing a
// transcript outside the conversations directory.
func TestConversationSaveRejectsUnsafeIDs(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, id := range []string{"", "../escape", "a/b", `..\win`, "conv_\x00x"} {
		conv := &domain.Conversation{ID: id, Title: "hostile"}
		if err := store.Save(conv); err == nil {
			t.Errorf("Save(%q) = nil, want an error", id)
		}
	}
}

// TestConversationDeleteRemovesFolder pins the cascade contract: deleting a
// conversation removes its whole folder (transcript, chunk/, acp/,
// operation/, plan.md) while leaving sibling conversations untouched.
func TestConversationDeleteRemovesFolder(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	keepID := "conv_keep"
	dropID := "conv_drop"
	for _, id := range []string{keepID, dropID} {
		mustWrite(t, filepath.Join(convDir, id, conversationIndexName),
			`{"ID":"`+id+`","Title":"`+id+`","Status":"idle"}`+"\n")
		mustWrite(t, filepath.Join(convDir, id, "chunk", "chunk-0.json"), `[]`)
		mustWrite(t, filepath.Join(convDir, id, "acp", "acprun_abc.json"), `{}`)
		mustWrite(t, filepath.Join(convDir, id, "operation", "run_abc.patch"), "diff --git a/a b/a\n")
		mustWrite(t, filepath.Join(convDir, id, "plan.md"), "brief")
	}

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := store.Delete(dropID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(filepath.Join(convDir, dropID)); !os.IsNotExist(err) {
		t.Errorf("expected conversations/%s to be removed, got err=%v", dropID, err)
	}
	for _, sub := range []string{conversationIndexName, filepath.Join("chunk", "chunk-0.json"), filepath.Join("acp", "acprun_abc.json"), filepath.Join("operation", "run_abc.patch"), "plan.md"} {
		if _, err := os.Stat(filepath.Join(convDir, keepID, sub)); err != nil {
			t.Errorf("sibling sidecar %s must survive delete: %v", sub, err)
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
	mustMkdir(t, convDir)
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
	want := filepath.Join(root, "conversations", "conv_abc123", "index.jsonl")
	if got := store.ConversationPath("conv_abc123"); got != want {
		t.Fatalf("ConversationPath() = %q, want %q", got, want)
	}
	for _, id := range []string{"", "../escape", "a/b", `..\win`, "conv_\x00x"} {
		if got := store.ConversationPath(id); got != "" {
			t.Errorf("ConversationPath(%q) = %q, want empty", id, got)
		}
	}
}
