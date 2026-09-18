package agent

import (
	"errors"
	"strings"
	"testing"

	"nusashell/domain/turndiff"
)

type recordedPatch struct {
	conversationID string
	runID          string
	patch          string
}

type recordingTurnPatches struct {
	saved []recordedPatch
	err   error
}

func (r *recordingTurnPatches) Save(conversationID, runID, patch string) error {
	if r.err != nil {
		return r.err
	}
	r.saved = append(r.saved, recordedPatch{conversationID: conversationID, runID: runID, patch: patch})
	return nil
}

// TestPersistTurnDiffWritesPatchForFileMutations pins the operation/ contract:
// a turn that committed file_* mutations persists its net unified diff once,
// keyed by conversation and run.
func TestPersistTurnDiffWritesPatchForFileMutations(t *testing.T) {
	patches := &recordingTurnPatches{}
	svc := New(Deps{TurnPatches: patches})
	run := &TurnRun{ID: "run_1", ConversationID: "conv_1", Workspace: t.TempDir()}
	run.InitTurnDiff()
	run.TurnDiff.TrackDelta(turndiff.AddFile("a.txt", "hello\n", nil))

	svc.PersistTurnDiff(run)

	if len(patches.saved) != 1 {
		t.Fatalf("saved patches = %d, want 1", len(patches.saved))
	}
	got := patches.saved[0]
	if got.conversationID != "conv_1" || got.runID != "run_1" {
		t.Fatalf("patch key = %s/%s, want conv_1/run_1", got.conversationID, got.runID)
	}
	if !strings.Contains(got.patch, "diff --git a/a.txt b/a.txt") || !strings.Contains(got.patch, "+hello") {
		t.Fatalf("patch body = %q", got.patch)
	}
}

// TestPersistTurnDiffSkipsTurnsWithoutMutations keeps turns that only read
// (explore, answer, exec) from leaving empty patch files behind.
func TestPersistTurnDiffSkipsTurnsWithoutMutations(t *testing.T) {
	patches := &recordingTurnPatches{}
	svc := New(Deps{TurnPatches: patches})
	run := &TurnRun{ID: "run_2", ConversationID: "conv_1"}
	run.InitTurnDiff()

	svc.PersistTurnDiff(run)

	if len(patches.saved) != 0 {
		t.Fatalf("saved patches = %d, want 0", len(patches.saved))
	}
}

// TestPersistTurnDiffIsBestEffort: a failing patch store must not fail the
// turn that already persisted its transcript; it logs and moves on.
func TestPersistTurnDiffIsBestEffort(t *testing.T) {
	patches := &recordingTurnPatches{err: errors.New("disk full")}
	var logged []string
	svc := New(Deps{
		TurnPatches: patches,
		Log: func(level, source, format string, args ...any) {
			logged = append(logged, level+" "+source)
		},
	})
	run := &TurnRun{ID: "run_3", ConversationID: "conv_1", Workspace: t.TempDir()}
	run.InitTurnDiff()
	run.TurnDiff.TrackDelta(turndiff.AddFile("b.txt", "x\n", nil))

	svc.PersistTurnDiff(run)

	if len(logged) != 1 || !strings.HasPrefix(logged[0], "warn agent") {
		t.Fatalf("expected one warn log, got %v", logged)
	}
}

// TestPersistTurnDiffWithoutStoreIsNoop keeps the agent usable in
// compositions that do not wire a patch store.
func TestPersistTurnDiffWithoutStoreIsNoop(t *testing.T) {
	svc := New(Deps{})
	run := &TurnRun{ID: "run_4", ConversationID: "conv_1", Workspace: t.TempDir()}
	run.InitTurnDiff()
	run.TurnDiff.TrackDelta(turndiff.AddFile("c.txt", "y\n", nil))

	svc.PersistTurnDiff(run) // must not panic
}
