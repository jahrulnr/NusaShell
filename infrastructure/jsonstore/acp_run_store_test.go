package jsonstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"nusashell/domain"
)

func TestAcpRunStoreSaveLoadList(t *testing.T) {
	dir := t.TempDir()
	store := NewAcpRunStore(dir)

	r1 := domain.AcpRunRecord{
		ID:             "acprun_abc",
		AgentID:        "acp_devin",
		AgentName:      "Devin",
		ConversationID: "conv_1",
		Workspace:      "/tmp/proj",
		Prompt:         "Fix the bug",
		Status:         domain.AcpRunCompleted,
		Transcript: []domain.AcpTranscriptChunk{
			{Kind: "text", Text: "I fixed the bug.", At: time.Now().UTC()},
		},
		StartedAt: time.Now().Add(-1 * time.Minute).UTC(),
		EndedAt:   time.Now().UTC(),
	}
	r2 := domain.AcpRunRecord{
		ID:             "acprun_xyz",
		AgentID:        "acp_gemini",
		AgentName:      "Gemini",
		ConversationID: "conv_1",
		Prompt:         "Write tests",
		Status:         domain.AcpRunFailed,
		Error:          "timeout",
		StartedAt:      time.Now().Add(-2 * time.Minute).UTC(),
		EndedAt:        time.Now().Add(-30 * time.Second).UTC(),
	}
	r3 := domain.AcpRunRecord{
		ID:             "acprun_other",
		AgentID:        "acp_devin",
		AgentName:      "Devin",
		ConversationID: "conv_2",
		Prompt:         "Other conv",
		Status:         domain.AcpRunCompleted,
		StartedAt:      time.Now().UTC(),
		EndedAt:        time.Now().UTC(),
	}

	if err := store.Save(r1); err != nil {
		t.Fatalf("Save r1: %v", err)
	}
	if err := store.Save(r2); err != nil {
		t.Fatalf("Save r2: %v", err)
	}
	if err := store.Save(r3); err != nil {
		t.Fatalf("Save r3: %v", err)
	}

	// Load by ID
	got, ok := store.Load("acprun_abc")
	if !ok {
		t.Fatal("Load acprun_abc: not found")
	}
	if got.AgentName != "Devin" || got.Status != domain.AcpRunCompleted {
		t.Fatalf("Load acprun_abc: got %+v", got)
	}
	if len(got.Transcript) != 1 || got.Transcript[0].Text != "I fixed the bug." {
		t.Fatalf("Load acprun_abc: transcript mismatch: %+v", got.Transcript)
	}

	// List by conversation
	conv1Runs := store.List("conv_1")
	if len(conv1Runs) != 2 {
		t.Fatalf("List conv_1: expected 2, got %d", len(conv1Runs))
	}
	conv2Runs := store.List("conv_2")
	if len(conv2Runs) != 1 {
		t.Fatalf("List conv_2: expected 1, got %d", len(conv2Runs))
	}

	// List all
	allRuns := store.List("")
	if len(allRuns) != 3 {
		t.Fatalf("List all: expected 3, got %d", len(allRuns))
	}
}

func TestAcpRunStorePerConversationLayout(t *testing.T) {
	dir := t.TempDir()
	store := NewAcpRunStore(dir)

	r := domain.AcpRunRecord{
		ID:             "acprun_layout",
		ConversationID: "conv_abc123",
		Status:         domain.AcpRunCompleted,
		StartedAt:      time.Now().UTC(),
		EndedAt:        time.Now().UTC(),
	}
	if err := store.Save(r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	wantPath := filepath.Join(dir, "conversations", "conv_abc123.acp", "acprun_layout.json")
	b, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("run file not at per-conversation path %s: %v", wantPath, err)
	}
	var decoded domain.AcpRunRecord
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("stored file is not a single JSON record: %v", err)
	}
	if decoded.ID != r.ID || decoded.ConversationID != r.ConversationID {
		t.Fatalf("decoded mismatch: %+v", decoded)
	}
}

func TestAcpRunStorePath(t *testing.T) {
	dir := t.TempDir()
	store := NewAcpRunStore(dir)

	got := store.Path("conv_abc123", "acprun_path")
	want := filepath.Join(dir, "conversations", "conv_abc123.acp", "acprun_path.json")
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}

	// Unsafe IDs resolve to "" instead of an escaped path.
	if p := store.Path("../escape", "run"); p != "" {
		t.Fatalf("Path with traversal conversation = %q, want empty", p)
	}
	if p := store.Path("conv_ok", "run/../../x"); p != "" {
		t.Fatalf("Path with traversal run id = %q, want empty", p)
	}
}

func TestAcpRunStoreNoSharedGlobalFile(t *testing.T) {
	dir := t.TempDir()
	store := NewAcpRunStore(dir)

	if err := store.Save(domain.AcpRunRecord{
		ID: "acprun_a", ConversationID: "conv_1", Status: domain.AcpRunCompleted,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	global := filepath.Join(dir, "conversations", "acp_runs.jsonl")
	if _, err := os.Stat(global); !os.IsNotExist(err) {
		t.Fatalf("global shared file must not be created anymore: %s", global)
	}
}

func TestAcpRunStoreSaveUpdatesExisting(t *testing.T) {
	dir := t.TempDir()
	store := NewAcpRunStore(dir)

	r := domain.AcpRunRecord{
		ID:             "acprun_update",
		ConversationID: "conv_1",
		Status:         domain.AcpRunRunning,
		StartedAt:      time.Now().UTC(),
	}
	if err := store.Save(r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Update same run with completed status
	r.Status = domain.AcpRunCompleted
	r.EndedAt = time.Now().UTC()
	r.Transcript = []domain.AcpTranscriptChunk{
		{Kind: "text", Text: "Done!", At: time.Now().UTC()},
	}
	if err := store.Save(r); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	// Should not duplicate
	all := store.List("")
	if len(all) != 1 {
		t.Fatalf("expected 1 record after update, got %d", len(all))
	}
	if all[0].Status != domain.AcpRunCompleted {
		t.Fatalf("status not updated: %s", all[0].Status)
	}
}

func TestAcpRunStoreReloadFromDisk(t *testing.T) {
	dir := t.TempDir()
	store1 := NewAcpRunStore(dir)
	r := domain.AcpRunRecord{
		ID:             "acprun_persist",
		ConversationID: "conv_1",
		Status:         domain.AcpRunCompleted,
		StartedAt:      time.Now().UTC(),
	}
	if err := store1.Save(r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// New store instance reading same files
	store2 := NewAcpRunStore(dir)
	got, ok := store2.Load("acprun_persist")
	if !ok {
		t.Fatal("reload: not found")
	}
	if got.ConversationID != "conv_1" {
		t.Fatalf("reload: mismatch: %+v", got)
	}
}

// Multi-spawn parity: several runs completing concurrently (the async
// onAcpRunDone path fires one goroutine per finished subagent) must all
// land without lost updates or corruption.
func TestAcpRunStoreConcurrentSaves(t *testing.T) {
	dir := t.TempDir()
	store := NewAcpRunStore(dir)

	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("acprun_par_%02d", i)
			err := store.Save(domain.AcpRunRecord{
				ID:             id,
				ConversationID: "conv_multi",
				Prompt:         "parallel spawn",
				Status:         domain.AcpRunCompleted,
				StartedAt:      time.Now().UTC(),
				EndedAt:        time.Now().UTC(),
			})
			if err != nil {
				t.Errorf("concurrent Save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	all := store.List("conv_multi")
	if len(all) != n {
		t.Fatalf("lost updates under concurrency: expected %d records, got %d", n, len(all))
	}
}

// List must return runs ordered by StartedAt ascending regardless of
// filesystem directory order.
func TestAcpRunStoreListSortedByStartedAt(t *testing.T) {
	dir := t.TempDir()
	store := NewAcpRunStore(dir)

	base := time.Now().UTC()
	for _, rec := range []domain.AcpRunRecord{
		{ID: "acprun_mid", ConversationID: "conv_s", StartedAt: base.Add(2 * time.Minute)},
		{ID: "acprun_old", ConversationID: "conv_s", StartedAt: base.Add(-5 * time.Minute)},
		{ID: "acprun_new", ConversationID: "conv_s", StartedAt: base.Add(9 * time.Minute)},
	} {
		rec.Status = domain.AcpRunCompleted
		if err := store.Save(rec); err != nil {
			t.Fatalf("Save %s: %v", rec.ID, err)
		}
	}

	got := store.List("conv_s")
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].StartedAt.After(got[i].StartedAt) {
			t.Fatalf("List not sorted by StartedAt: %v > %v", got[i-1].StartedAt, got[i].StartedAt)
		}
	}
}

// A pre-existing global acp_runs.jsonl from earlier versions is migrated
// lazily into the per-conversation layout; the original file is renamed
// (not deleted) so migration stays reversible.
func TestAcpRunStoreMigratesLegacyJSONL(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "conversations", "acp_runs.jsonl")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"id":"acprun_l1","conversation_id":"conv_old","status":"completed","started_at":"2026-08-20T10:00:00Z","transcript":[]}`,
		`{"id":"acprun_l2","conversation_id":"conv_new","status":"failed","error":"boom","started_at":"2026-08-21T10:00:00Z","transcript":[]}`,
		`not json at all`, // malformed line must be skipped
	}
	body := lines[0] + "\n" + lines[1] + "\n" + lines[2] + "\n"
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewAcpRunStore(dir)
	got, ok := store.Load("acprun_l1")
	if !ok || got.ConversationID != "conv_old" {
		t.Fatalf("migrated l1 missing/wrong: ok=%v %+v", ok, got)
	}
	got2, ok := store.Load("acprun_l2")
	if !ok || got2.Status != domain.AcpRunFailed {
		t.Fatalf("migrated l2 missing/wrong: ok=%v %+v", ok, got2)
	}

	if _, err := os.Stat(filepath.Join(dir, "conversations", "conv_old.acp", "acprun_l1.json")); err != nil {
		t.Fatalf("legacy record not written to per-conversation layout: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy file should be renamed away after migration")
	}
	if _, err := os.Stat(legacy + ".imported"); err != nil {
		t.Fatalf("legacy backup .imported missing: %v", err)
	}
}

func TestAcpRunRecordFilenameSanitization(t *testing.T) {
	dir := t.TempDir()
	store := NewAcpRunStore(dir)
	r := domain.AcpRunRecord{
		ID:             "../../evil/run",
		ConversationID: "conv_x",
		Status:         domain.AcpRunCompleted,
		StartedAt:      time.Now().UTC(),
	}
	if err := store.Save(r); err == nil {
		t.Fatal("Save must reject path-hostile run IDs")
	}
}
