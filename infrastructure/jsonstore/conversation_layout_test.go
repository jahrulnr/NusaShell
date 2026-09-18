package jsonstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

// TestMigrateLegacyConversationLayout pins the one-shot conversion of the
// retired flat layout into the per-conversation folder layout: the transcript
// becomes index.jsonl, the .chunks/.acp sidecar directories move under the
// conversation folder, the plan mirror is untouched, and files that are not
// conversations stay where they are.
func TestMigrateLegacyConversationLayout(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")

	legacy := &domain.Conversation{
		ID:    "conv_legacy",
		Title: "legacy room",
		Model: "prov_x:model-y",
		Messages: []domain.Message{
			{ID: "msg_1", Role: domain.RoleUser, Content: "hello", Status: domain.StatusDone},
			{ID: "msg_2", Role: domain.RoleAssistant, Content: "hi", Status: domain.StatusDone},
		},
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(convDir, "conv_legacy.json"), string(b))
	mustWrite(t, filepath.Join(convDir, "conv_legacy", "plan.md"), "brief")
	mustWrite(t, filepath.Join(convDir, "conv_legacy.chunks", "chunk-0.json"), `[{"ID":"msg_old"}]`)
	mustWrite(t, filepath.Join(convDir, "conv_legacy.acp", "acprun_x.json"), `{"id":"acprun_x"}`)
	// Noise: other stores and a broken legacy transcript.
	mustWrite(t, filepath.Join(convDir, "todos.json"), `{"conv_legacy":{"Items":[]}}`)
	mustWrite(t, filepath.Join(convDir, "conv_broken.json"), `{not json`)

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := store.Get("conv_legacy")
	if err != nil {
		t.Fatalf("migrated conversation missing: %v", err)
	}
	if got.Title != "legacy room" || len(got.Messages) != 2 || got.Messages[1].Content != "hi" {
		t.Fatalf("migrated conversation mismatch: %+v", got)
	}

	// New layout in place, old layout gone.
	if _, err := os.Stat(filepath.Join(convDir, "conv_legacy", "index.jsonl")); err != nil {
		t.Fatalf("index.jsonl missing after migration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_legacy.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy transcript file must be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_legacy.chunks")); !os.IsNotExist(err) {
		t.Fatalf("legacy .chunks dir must be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_legacy.acp")); !os.IsNotExist(err) {
		t.Fatalf("legacy .acp dir must be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_legacy", "chunk", "chunk-0.json")); err != nil {
		t.Fatalf("chunk-0.json not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_legacy", "acp", "acprun_x.json")); err != nil {
		t.Fatalf("acprun_x.json not moved: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(convDir, "conv_legacy", "plan.md")); err != nil || string(b) != "brief" {
		t.Fatalf("plan.md must survive migration: %q err=%v", b, err)
	}
	// Archived chunks stay loadable through the store after the move.
	if msgs, err := store.GetChunk("conv_legacy", 0); err != nil || len(msgs) != 1 || msgs[0].ID != "msg_old" {
		t.Fatalf("chunk not readable after migration: %+v err=%v", msgs, err)
	}
	// Non-conversation files are untouched; a broken transcript is left for
	// a human instead of being deleted, and it does not become a conversation.
	if _, err := os.Stat(filepath.Join(convDir, "todos.json")); err != nil {
		t.Fatalf("todos.json must survive migration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_broken.json")); err != nil {
		t.Fatalf("broken legacy transcript must be left in place: %v", err)
	}
	if got := store.List(); len(got) != 1 {
		t.Fatalf("want exactly one migrated conversation, got %d", len(got))
	}

	// A second boot is a no-op.
	again, err := New(dir)
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	if got := again.List(); len(got) != 1 || got[0].ID != "conv_legacy" {
		t.Fatalf("migration is not idempotent: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_broken.json")); err != nil {
		t.Fatalf("second boot must not touch the broken transcript: %v", err)
	}
}

// TestMigrateLegacyLayoutKeepsConflictsInPlace guards the non-destructive
// rule: when both layouts hold data, the folder record wins for loading and
// nothing is deleted or overwritten.
func TestMigrateLegacyLayoutKeepsConflictsInPlace(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")

	mustWrite(t, filepath.Join(convDir, "conv_mix.json"), `{"ID":"conv_mix","Title":"legacy copy"}`)
	mustWrite(t, filepath.Join(convDir, "conv_mix", "index.jsonl"), `{"ID":"conv_mix","Title":"folder copy"}`+"\n")
	mustWrite(t, filepath.Join(convDir, "conv_mix.chunks", "chunk-0.json"), `[{"ID":"msg_legacy_0"}]`)
	mustWrite(t, filepath.Join(convDir, "conv_mix.chunks", "chunk-1.json"), `[{"ID":"msg_legacy_1"}]`)
	mustWrite(t, filepath.Join(convDir, "conv_mix", "chunk", "chunk-0.json"), `[{"ID":"msg_folder_0"}]`)

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := store.Get("conv_mix")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "folder copy" {
		t.Fatalf("folder record must win, got %q", got.Title)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_mix.json")); err != nil {
		t.Fatalf("conflicting legacy transcript must be left in place: %v", err)
	}
	// Non-conflicting files still move; the conflicting one stays on both sides.
	if _, err := os.Stat(filepath.Join(convDir, "conv_mix", "chunk", "chunk-1.json")); err != nil {
		t.Fatalf("non-conflicting chunk must move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_mix.chunks", "chunk-0.json")); err != nil {
		t.Fatalf("conflicting chunk must be left in place: %v", err)
	}
	if msgs, err := store.GetChunk("conv_mix", 0); err != nil || msgs[0].ID != "msg_folder_0" {
		t.Fatalf("folder chunk must win: %+v err=%v", msgs, err)
	}
}

// TestMigrateLegacyLayoutSkipsIDMismatch keeps a legacy file whose record id
// disagrees with its name from being written into a sibling conversation's
// folder.
func TestMigrateLegacyLayoutSkipsIDMismatch(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	mustWrite(t, filepath.Join(convDir, "conv_name.json"), `{"ID":"conv_other","Title":"mismatch"}`)

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("mismatched legacy record must not load, got %+v", got)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_name.json")); err != nil {
		t.Fatalf("mismatched legacy file must be left in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "conv_other")); !os.IsNotExist(err) {
		t.Fatalf("no folder may be created for a mismatched record, got err=%v", err)
	}
}

// TestEncodeConversationJSONLOmitsTranscriptFromHeader pins the codec
// contract directly, including an explicit message count.
func TestEncodeConversationJSONLOmitsTranscriptFromHeader(t *testing.T) {
	conv := &domain.Conversation{
		ID:    "conv_codec",
		Title: "codec",
		Messages: []domain.Message{
			{ID: "msg_1", Role: domain.RoleUser, Content: "a"},
			{ID: "msg_2", Role: domain.RoleAssistant, Content: "b"},
		},
	}
	out, err := encodeConversationJSONL(conv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := strings.Count(string(out), "\n"); got != 3 {
		t.Fatalf("encoded lines = %d, want 3:\n%s", got, out)
	}
	decoded, skipped, err := decodeConversationJSONL(out)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if decoded.ID != conv.ID || len(decoded.Messages) != 2 || decoded.Messages[1].Content != "b" {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
}
