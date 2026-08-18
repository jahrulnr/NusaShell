package jsonstore

import (
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

	// New store instance reading same file
	store2 := NewAcpRunStore(dir)
	got, ok := store2.Load("acprun_persist")
	if !ok {
		t.Fatal("reload: not found")
	}
	if got.ConversationID != "conv_1" {
		t.Fatalf("reload: mismatch: %+v", got)
	}
}
