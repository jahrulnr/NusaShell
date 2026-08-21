package jsonstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

// Regression: SaveMemory wrote memory/legacy.jsonl while DeleteMemory and
// ReplaceMemory rewrote memories.jsonl. After a restart the store reloaded
// legacy.jsonl only, so deletions resurrected and replacements reverted.
func TestMemoryDeleteAndReplaceSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveMemory(&domain.MemoryEntry{ID: "mem-1", Target: domain.MemoryTargetMemory, Content: "alpha beta"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceMemory(domain.MemoryTargetMemory, "beta", "alpha gamma"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteMemory("mem-1"); err != nil {
		t.Fatal(err)
	}

	// Simulate a process restart.
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.ListMemories()
	if len(got) != 0 {
		t.Fatalf("expected 0 memories after delete+restart, got %d: %+v", len(got), got)
	}
}

func TestMemoryLegacyFileStillLoads(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(dir); err != nil {
		t.Fatal(err)
	}
	legacy := "{\"id\":\"old-1\",\"target\":\"memory\",\"content\":\"from old store\"}\n"
	if err := writeFile(filepath.Join(dir, "memory", "legacy.jsonl"), []byte(legacy)); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.ListMemories()
	if len(got) != 1 || !strings.Contains(got[0].Content, "from old store") {
		t.Fatalf("legacy entry not loaded: %+v", got)
	}
}
func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o644)
}

// Regression: the conversations directory is shared with other stores
// (todos.json, artifacts.json, acp_runs.jsonl). load() used to swallow
// every *.json file as a conversation, producing ghost entries with an
// empty ID whose only visible symptom was RPC failures like
// "conversation id is required" when the UI auto-opened the first room.
func TestLoadIgnoresForeignFilesInConversationsDir(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(&domain.Conversation{ID: "conv_real1", Title: "real"}); err != nil {
		t.Fatal(err)
	}
	foreign := map[string]string{
		"todos.json":     `{"goal":"x","items":[]}`,
		"artifacts.json": `{"artifacts":[]}`,
		"notes.txt":      `not json at all`,
	}
	for name, body := range foreign {
		if err := writeFile(filepath.Join(dir, "conversations", name), []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.List()
	if len(got) != 1 || got[0].ID != "conv_real1" {
		t.Fatalf("expected exactly conv_real1, got %+v", got)
	}
}
