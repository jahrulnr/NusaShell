package domain

import (
	"testing"
	"time"
)

func TestAcpRunBeginRunning(t *testing.T) {
	run := &AcpRun{Status: AcpRunStarting}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	run.BeginRunning(now)
	if run.Status != AcpRunRunning {
		t.Fatalf("Status = %v, want %v", run.Status, AcpRunRunning)
	}
	if !run.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", run.UpdatedAt, now)
	}
}

func TestAcpRunFinish(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	t.Run("completed clears pending permission and stamps EndedAt", func(t *testing.T) {
		run := &AcpRun{
			Status:            AcpRunRunning,
			PendingPermission: &AcpPermissionRequest{ID: "perm1"},
		}
		run.Finish(AcpRunCompleted, "", "stop_done", now)
		if run.Status != AcpRunCompleted {
			t.Fatalf("Status = %v, want %v", run.Status, AcpRunCompleted)
		}
		if run.StopReason != "stop_done" {
			t.Fatalf("StopReason = %q, want %q", run.StopReason, "stop_done")
		}
		if run.PendingPermission != nil {
			t.Fatal("PendingPermission must be cleared on finish")
		}
		if !run.EndedAt.Equal(now) {
			t.Fatalf("EndedAt = %v, want %v", run.EndedAt, now)
		}
		if !run.UpdatedAt.Equal(now) {
			t.Fatalf("UpdatedAt = %v, want %v", run.UpdatedAt, now)
		}
	})
	t.Run("failed records error", func(t *testing.T) {
		run := &AcpRun{Status: AcpRunRunning}
		run.Finish(AcpRunFailed, "connection lost", "stop_error", now)
		if run.Status != AcpRunFailed {
			t.Fatalf("Status = %v, want %v", run.Status, AcpRunFailed)
		}
		if run.Error != "connection lost" {
			t.Fatalf("Error = %q, want %q", run.Error, "connection lost")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		run := &AcpRun{Status: AcpRunRunning}
		run.Finish(AcpRunCancelled, "", "cancelled", now)
		if run.Status != AcpRunCancelled {
			t.Fatalf("Status = %v, want %v", run.Status, AcpRunCancelled)
		}
	})
}

func TestAcpRunResolvePermission(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	t.Run("waiting permission returns to running", func(t *testing.T) {
		run := &AcpRun{
			Status:            AcpRunWaitingPermission,
			PendingPermission: &AcpPermissionRequest{ID: "perm1"},
		}
		run.ResolvePermission(now)
		if run.Status != AcpRunRunning {
			t.Fatalf("Status = %v, want %v", run.Status, AcpRunRunning)
		}
		if run.PendingPermission != nil {
			t.Fatal("PendingPermission must be cleared")
		}
		if !run.UpdatedAt.Equal(now) {
			t.Fatalf("UpdatedAt = %v, want %v", run.UpdatedAt, now)
		}
	})
	t.Run("non-waiting run keeps its status", func(t *testing.T) {
		run := &AcpRun{
			Status:            AcpRunRunning,
			PendingPermission: &AcpPermissionRequest{ID: "perm1"},
		}
		run.ResolvePermission(now)
		if run.Status != AcpRunRunning {
			t.Fatalf("Status = %v, want %v (unchanged)", run.Status, AcpRunRunning)
		}
		if run.PendingPermission != nil {
			t.Fatal("PendingPermission must be cleared")
		}
	})
}
