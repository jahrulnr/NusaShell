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
	// Every save also refreshes the meta.json sidecar and leaves no pending
	// log behind after a full (first) write.
	meta, err := os.ReadFile(filepath.Join(dir, "conversations", conv.ID, "meta.json"))
	if err != nil {
		t.Fatalf("meta.json missing: %v", err)
	}
	var metaHeader map[string]json.RawMessage
	if err := json.Unmarshal(meta, &metaHeader); err != nil {
		t.Fatalf("meta.json is not JSON: %v", err)
	}
	if _, ok := metaHeader["Messages"]; ok {
		t.Fatalf("meta.json must not carry the transcript: %s", meta)
	}
	if _, err := os.Stat(filepath.Join(dir, "conversations", conv.ID, "pending.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("pending.jsonl must not exist after a full write, got err=%v", err)
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

// transcriptFileLines returns the non-empty lines of a JSONL file.
func transcriptFileLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func newTestConversation(id string, msgs ...domain.Message) *domain.Conversation {
	return &domain.Conversation{
		ID:        id,
		Title:     id + " title",
		Status:    "idle",
		Type:      domain.ConversationTypeConversation,
		CreatedAt: time.Now().UTC().Add(-time.Hour),
		UpdatedAt: time.Now().UTC(),
		Messages:  msgs,
	}
}

// TestConversationSaveAppendsChangedLinesToPending pins the append-mostly
// write path: a tail append plus an earlier-message patch must land in
// pending.jsonl without touching index.jsonl, while meta.json still carries
// the newest header.
func TestConversationSaveAppendsChangedLinesToPending(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	conv := newTestConversation("conv_wal",
		domain.Message{ID: "msg_1", Role: domain.RoleUser, Content: "one"},
		domain.Message{ID: "msg_2", Role: domain.RoleAssistant, Content: "two"},
	)
	if err := store.Save(conv); err != nil {
		t.Fatalf("Save: %v", err)
	}
	convDir := filepath.Join(dir, "conversations", "conv_wal")
	indexPath := filepath.Join(convDir, "index.jsonl")
	pendingPath := filepath.Join(convDir, "pending.jsonl")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	// Tail append + mutation of an earlier message (e.g. a status patch).
	next, err := store.Get("conv_wal")
	if err != nil {
		t.Fatal(err)
	}
	next.Title = "renamed"
	next.Messages[0].Content = "one (edited)"
	next.Messages = append(next.Messages, domain.Message{ID: "msg_3", Role: domain.RoleUser, Content: "three"})
	if err := store.Save(next); err != nil {
		t.Fatalf("Save tail+patch: %v", err)
	}

	indexAfter, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(indexBefore) != string(indexAfter) {
		t.Fatal("index.jsonl must be untouched by an append-path save")
	}
	pending := transcriptFileLines(t, pendingPath)
	if len(pending) != 2 {
		t.Fatalf("pending.jsonl lines = %d, want 2 (edited msg_1 + new msg_3)", len(pending))
	}
	var p0, p1 domain.Message
	if err := json.Unmarshal([]byte(pending[0]), &p0); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(pending[1]), &p1); err != nil {
		t.Fatal(err)
	}
	if p0.ID != "msg_1" || p0.Content != "one (edited)" || p1.ID != "msg_3" {
		t.Fatalf("pending lines mismatch: %q | %q", pending[0], pending[1])
	}
	// meta.json carries the newest header even though index.jsonl lagged.
	var meta domain.Conversation
	mb, err := os.ReadFile(filepath.Join(convDir, "meta.json"))
	if err != nil {
		t.Fatalf("meta.json missing: %v", err)
	}
	if err := json.Unmarshal(mb, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Title != "renamed" {
		t.Fatalf("meta.json title = %q, want renamed", meta.Title)
	}

	// In-memory view and a reload both see the merged transcript.
	got, err := store.Get("conv_wal")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 3 || got.Messages[0].Content != "one (edited)" || got.Messages[2].ID != "msg_3" {
		t.Fatalf("merged view mismatch: %+v", got.Messages)
	}
	reloaded, err := New(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, err = reloaded.Get("conv_wal")
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.Title != "renamed" {
		t.Fatalf("reloaded title = %q, want meta.json value", got.Title)
	}
	if len(got.Messages) != 3 || got.Messages[0].Content != "one (edited)" || got.Messages[1].ID != "msg_2" || got.Messages[2].ID != "msg_3" {
		t.Fatalf("pending replay mismatch after reload: %+v", got.Messages)
	}

	// A save that changes nothing appends nothing: pending must not grow.
	pendingBefore := transcriptFileLines(t, pendingPath)
	if err := store.Save(got); err != nil {
		t.Fatalf("Save unchanged: %v", err)
	}
	if after := transcriptFileLines(t, pendingPath); len(after) != len(pendingBefore) {
		t.Fatalf("unchanged save grew pending.jsonl: %d -> %d", len(pendingBefore), len(after))
	}
}

// TestConversationSaveEpochResetRewritesIndex covers the paths that cannot be
// expressed as pending upserts — a truncated transcript (compaction), a
// reordered or mid-inserted message, and an ID-less message. Each must fall
// back to a full index.jsonl rewrite and drop the stale pending log.
func TestConversationSaveEpochResetRewritesIndex(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	convDir := filepath.Join(dir, "conversations", "conv_epoch")
	indexPath := filepath.Join(convDir, "index.jsonl")
	pendingPath := filepath.Join(convDir, "pending.jsonl")

	conv := newTestConversation("conv_epoch",
		domain.Message{ID: "msg_1", Role: domain.RoleUser, Content: "a"},
		domain.Message{ID: "msg_2", Role: domain.RoleAssistant, Content: "b"},
	)
	if err := store.Save(conv); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Grow the tail once so a stale pending log exists to be removed.
	next, _ := store.Get("conv_epoch")
	next.Messages = append(next.Messages, domain.Message{ID: "msg_3", Role: domain.RoleUser, Content: "c"})
	if err := store.Save(next); err != nil {
		t.Fatalf("Save append: %v", err)
	}
	if lines := transcriptFileLines(t, pendingPath); len(lines) != 1 {
		t.Fatalf("pending.jsonl lines = %d, want 1", len(lines))
	}

	// Compaction epoch: the transcript is replaced wholesale.
	reset := newTestConversation("conv_epoch",
		domain.Message{ID: "msg_sys", Role: domain.RoleSystem, Content: "summary"},
	)
	reset.Summary = "compacted"
	if err := store.Save(reset); err != nil {
		t.Fatalf("Save reset: %v", err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending.jsonl must be removed on epoch reset, got err=%v", err)
	}
	lines := transcriptFileLines(t, indexPath)
	if len(lines) != 2 { // header + msg_sys
		t.Fatalf("index.jsonl lines = %d after reset, want 2", len(lines))
	}
	got, err := store.Get("conv_epoch")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ID != "msg_sys" || got.Summary != "compacted" {
		t.Fatalf("reset transcript mismatch: %+v", got)
	}

	// Mid-insert is an epoch change: [sys, X, sys2] replaces [sys, sys2].
	next, _ = store.Get("conv_epoch")
	next.Messages = append(next.Messages, domain.Message{ID: "msg_sys2", Role: domain.RoleAssistant, Content: "more"})
	if err := store.Save(next); err != nil {
		t.Fatalf("Save append2: %v", err)
	}
	if lines := transcriptFileLines(t, pendingPath); len(lines) != 1 {
		t.Fatalf("pending.jsonl lines = %d after tail append, want 1", len(lines))
	}
	mid, _ := store.Get("conv_epoch")
	mid.Messages = []domain.Message{mid.Messages[0], {ID: "msg_mid", Role: domain.RoleUser, Content: "inserted"}, mid.Messages[1]}
	if err := store.Save(mid); err != nil {
		t.Fatalf("Save mid-insert: %v", err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending.jsonl must be removed on mid-insert, got err=%v", err)
	}
	got, _ = store.Get("conv_epoch")
	if len(got.Messages) != 3 || got.Messages[1].ID != "msg_mid" {
		t.Fatalf("mid-insert rewrite mismatch: %+v", got.Messages)
	}
	reloaded, err := New(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, _ = reloaded.Get("conv_epoch")
	if len(got.Messages) != 3 || got.Messages[1].ID != "msg_mid" {
		t.Fatalf("mid-insert reload mismatch: %+v", got.Messages)
	}

	// An ID-less message can never ride the upsert log: saves keep doing a
	// full rewrite and no pending.jsonl is produced.
	noID, _ := store.Get("conv_epoch")
	noID.Messages = append(noID.Messages, domain.Message{Role: domain.RoleUser, Content: "no id"})
	if err := store.Save(noID); err != nil {
		t.Fatalf("Save id-less: %v", err)
	}
	noID2, _ := store.Get("conv_epoch")
	noID2.Messages = append(noID2.Messages, domain.Message{ID: "msg_tail", Role: domain.RoleUser, Content: "tail"})
	if err := store.Save(noID2); err != nil {
		t.Fatalf("Save after id-less: %v", err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("id-less transcript must never produce pending.jsonl, got err=%v", err)
	}
	if got, _ := store.Get("conv_epoch"); len(got.Messages) != 5 || got.Messages[4].ID != "msg_tail" {
		t.Fatalf("id-less transcript mismatch: %+v", got.Messages)
	}
}

// TestConversationPendingReplayedOnLoad writes index.jsonl + pending.jsonl +
// meta.json fixtures by hand and checks the merged read path: known IDs
// replace content at their first-seen position, new IDs append, a torn tail
// line is skipped, and meta.json wins over the stale index header.
func TestConversationPendingReplayedOnLoad(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	// Fixture lines are produced by the same encoder Save uses so byte-level
	// write-state diffs stay meaningful.
	staleHeader, err := json.Marshal(conversationHeader{Conversation: domain.Conversation{ID: "conv_p", Title: "stale title", Status: "idle"}})
	if err != nil {
		t.Fatal(err)
	}
	msgLine := func(m domain.Message) string {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	mustWrite(t, filepath.Join(convDir, "conv_p", "index.jsonl"), strings.Join([]string{
		string(staleHeader),
		msgLine(domain.Message{ID: "msg_1", Role: domain.RoleUser, Content: "original"}),
		msgLine(domain.Message{ID: "msg_2", Role: domain.RoleAssistant, Content: "reply"}),
	}, "\n")+"\n")
	mustWrite(t, filepath.Join(convDir, "conv_p", "meta.json"),
		`{"ID":"conv_p","Title":"fresh title","Status":"idle"}`)
	mustWrite(t, filepath.Join(convDir, "conv_p", "pending.jsonl"), strings.Join([]string{
		msgLine(domain.Message{ID: "msg_1", Role: domain.RoleUser, Content: "edited"}),
		msgLine(domain.Message{ID: "msg_3", Role: domain.RoleUser, Content: "appended"}),
		`{"ID":"msg_9","Role":"assistant",`, // torn tail from a crash
	}, "\n")+"\n")

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := store.Get("conv_p")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "fresh title" {
		t.Fatalf("meta.json must win over the index header, got title %q", got.Title)
	}
	want := []struct{ id, content string }{
		{"msg_1", "edited"}, {"msg_2", "reply"}, {"msg_3", "appended"},
	}
	if len(got.Messages) != len(want) {
		t.Fatalf("merged messages = %d, want %d: %+v", len(got.Messages), len(want), got.Messages)
	}
	for i, w := range want {
		if got.Messages[i].ID != w.id || got.Messages[i].Content != w.content {
			t.Fatalf("message %d = %q/%q, want %q/%q", i, got.Messages[i].ID, got.Messages[i].Content, w.id, w.content)
		}
	}
	// The tracked state reflects the merged view: an unchanged save must not
	// append to pending again.
	before, err := os.ReadFile(filepath.Join(convDir, "conv_p", "pending.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(got); err != nil {
		t.Fatalf("Save unchanged: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(convDir, "conv_p", "pending.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("saving the merged conversation unchanged must not grow pending.jsonl")
	}
}

// TestConversationPendingFlushAtThreshold drops the flush bound and appends
// until it trips: index.jsonl must be rewritten canonically and pending.jsonl
// removed, with reload still seeing every message.
func TestConversationPendingFlushAtThreshold(t *testing.T) {
	old := pendingFlushMaxLines
	pendingFlushMaxLines = 3
	defer func() { pendingFlushMaxLines = old }()

	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	conv := newTestConversation("conv_flush",
		domain.Message{ID: "msg_1", Role: domain.RoleUser, Content: "a"},
	)
	if err := store.Save(conv); err != nil {
		t.Fatalf("Save: %v", err)
	}
	pendingPath := filepath.Join(dir, "conversations", "conv_flush", "pending.jsonl")
	indexPath := filepath.Join(dir, "conversations", "conv_flush", "index.jsonl")

	for i, id := range []string{"msg_2", "msg_3", "msg_4"} {
		cur, err := store.Get("conv_flush")
		if err != nil {
			t.Fatal(err)
		}
		cur.Messages = append(cur.Messages, domain.Message{ID: id, Role: domain.RoleAssistant, Content: id})
		if err := store.Save(cur); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
		if i < 2 {
			if _, err := os.Stat(pendingPath); err != nil {
				t.Fatalf("pending.jsonl must exist before the flush (save %d): %v", i, err)
			}
		}
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending.jsonl must be folded into index.jsonl at the flush bound, got err=%v", err)
	}
	lines := transcriptFileLines(t, indexPath)
	if len(lines) != 1+4 {
		t.Fatalf("index.jsonl lines = %d after flush, want header + 4 messages", len(lines))
	}
	reloaded, err := New(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, err := reloaded.Get("conv_flush")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 4 || got.Messages[3].ID != "msg_4" {
		t.Fatalf("post-flush transcript mismatch: %+v", got.Messages)
	}
	// Post-flush saves return to the append path.
	cur, _ := reloaded.Get("conv_flush")
	cur.Messages = append(cur.Messages, domain.Message{ID: "msg_5", Role: domain.RoleUser, Content: "e"})
	if err := reloaded.Save(cur); err != nil {
		t.Fatalf("Save after flush: %v", err)
	}
	if lines := transcriptFileLines(t, pendingPath); len(lines) != 1 {
		t.Fatalf("pending.jsonl lines = %d after post-flush append, want 1", len(lines))
	}
}

// TestConversationRecoverySaveUsesPending covers the crash-recovery save at
// the end of load(): the interrupted-turn patches ride the pending upsert log
// instead of forcing a full index rewrite on the first boot.
func TestConversationRecoverySaveUsesPending(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	header, err := json.Marshal(conversationHeader{Conversation: domain.Conversation{ID: "conv_crash", Title: "crash", Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	msgLine := func(m domain.Message) string {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	indexPath := filepath.Join(convDir, "conv_crash", "index.jsonl")
	mustWrite(t, indexPath, strings.Join([]string{
		string(header),
		msgLine(domain.Message{ID: "msg_1", Role: domain.RoleUser, Content: "hi"}),
		msgLine(domain.Message{ID: "msg_2", Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{ID: "tc1", Name: "exec", Status: domain.ToolRunning}}}),
	}, "\n")+"\n")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := store.Get("conv_crash")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "idle" || got.Messages[1].Status != domain.StatusInterrupted || got.Messages[1].ToolCalls[0].Status != domain.ToolInterrupted {
		t.Fatalf("abandoned turn not recovered: status=%q msg=%+v", got.Status, got.Messages[1])
	}
	// Recovery persisted through the pending log: index.jsonl untouched,
	// pending.jsonl holds the patched assistant message, meta.json the
	// healed header.
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(indexBefore) != string(indexAfter) {
		t.Fatal("recovery save must not rewrite index.jsonl")
	}
	pending := transcriptFileLines(t, filepath.Join(convDir, "conv_crash", "pending.jsonl"))
	if len(pending) != 1 {
		t.Fatalf("pending.jsonl lines = %d, want 1 (patched msg_2)", len(pending))
	}
	var patched domain.Message
	if err := json.Unmarshal([]byte(pending[0]), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.ID != "msg_2" || patched.Status != domain.StatusInterrupted {
		t.Fatalf("pending line mismatch: %s", pending[0])
	}
	// And a reload sees the recovered state.
	reloaded, err := New(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, _ = reloaded.Get("conv_crash")
	if got.Status != "idle" || got.Messages[1].Status != domain.StatusInterrupted {
		t.Fatalf("recovered state lost on reload: %+v", got)
	}
}

// TestSaveSnapshotParity proves SaveSnapshot persists exactly like Save while
// taking ownership of the caller's object: the stored pointer is the object
// handed over, not another clone.
func TestSaveSnapshotParity(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	viaSnapshot := newTestConversation("conv_snap",
		domain.Message{ID: "msg_1", Role: domain.RoleUser, Content: "owned"},
	)
	if err := store.SaveSnapshot(viaSnapshot); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if store.conversations["conv_snap"] != viaSnapshot {
		t.Fatal("SaveSnapshot must store the caller's object without cloning")
	}
	got, err := store.Get("conv_snap")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "owned" {
		t.Fatalf("SaveSnapshot round trip mismatch: %+v", got.Messages)
	}
	// Readers still get private snapshots: mutating the Get result must not
	// leak back into the store.
	got.Messages[0].Content = "mutated"
	again, _ := store.Get("conv_snap")
	if again.Messages[0].Content != "owned" {
		t.Fatal("Get must return a private copy")
	}

	viaSave := newTestConversation("conv_clone",
		domain.Message{ID: "msg_1", Role: domain.RoleUser, Content: "cloned"},
	)
	if err := store.Save(viaSave); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if store.conversations["conv_clone"] == viaSave {
		t.Fatal("Save must store a private clone, not the caller's object")
	}
	viaSave.Title = "caller-side mutation"
	again, _ = store.Get("conv_clone")
	if again.Title == "caller-side mutation" {
		t.Fatal("Save clone isolation violated")
	}

	// SaveSnapshot drives the same WAL write path: a tail append lands in
	// pending.jsonl without rewriting index.jsonl.
	owned, _ := store.Get("conv_snap")
	owned.Messages = append(owned.Messages, domain.Message{ID: "msg_2", Role: domain.RoleAssistant, Content: "two"})
	if err := store.SaveSnapshot(owned); err != nil {
		t.Fatalf("SaveSnapshot append: %v", err)
	}
	pending := transcriptFileLines(t, filepath.Join(dir, "conversations", "conv_snap", "pending.jsonl"))
	if len(pending) != 1 {
		t.Fatalf("pending.jsonl lines = %d, want 1", len(pending))
	}
}
