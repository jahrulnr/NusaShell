package domain

import (
	"testing"
	"time"
)

func TestJobRunSkip(t *testing.T) {
	jr := &JobRun{Status: StatusQueued}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	jr.Skip(now)
	if jr.Status != StatusSkipped {
		t.Fatalf("Status = %v, want %v", jr.Status, StatusSkipped)
	}
	if jr.FinishedAt == nil || !jr.FinishedAt.Equal(now) {
		t.Fatalf("FinishedAt = %v, want %v", jr.FinishedAt, now)
	}
}

func TestJobRunBeginRunning(t *testing.T) {
	jr := &JobRun{Status: StatusQueued}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	jr.BeginRunning(now)
	if jr.Status != StatusRunning {
		t.Fatalf("Status = %v, want %v", jr.Status, StatusRunning)
	}
	if jr.StartedAt == nil || !jr.StartedAt.Equal(now) {
		t.Fatalf("StartedAt = %v, want %v", jr.StartedAt, now)
	}
}

func TestJobRunSucceed(t *testing.T) {
	jr := &JobRun{Status: StatusRunning}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	outputs := map[string]any{"key": "value"}
	jr.Succeed(outputs, now)
	if jr.Status != StatusSuccess {
		t.Fatalf("Status = %v, want %v", jr.Status, StatusSuccess)
	}
	if jr.FinishedAt == nil || !jr.FinishedAt.Equal(now) {
		t.Fatalf("FinishedAt = %v, want %v", jr.FinishedAt, now)
	}
	if jr.Outputs["key"] != "value" {
		t.Fatalf("Outputs = %v, want key=value", jr.Outputs)
	}
}

func TestJobRunParkWait(t *testing.T) {
	jr := &JobRun{Status: StatusRunning}
	jr.ParkWait()
	if jr.Status != StatusWaiting {
		t.Fatalf("Status = %v, want %v", jr.Status, StatusWaiting)
	}
}

func TestJobRunFail(t *testing.T) {
	jr := &JobRun{Status: StatusRunning}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	jr.Fail("build error", now)
	if jr.Status != StatusFailed {
		t.Fatalf("Status = %v, want %v", jr.Status, StatusFailed)
	}
	if jr.FailureReason != "build error" {
		t.Fatalf("FailureReason = %q, want %q", jr.FailureReason, "build error")
	}
	if jr.FinishedAt == nil || !jr.FinishedAt.Equal(now) {
		t.Fatalf("FinishedAt = %v, want %v", jr.FinishedAt, now)
	}
}

func TestJobRunParkBlocked(t *testing.T) {
	jr := &JobRun{Status: StatusQueued}
	jr.ParkBlocked("upstream failed: job_a")
	if jr.Status != StatusBlocked {
		t.Fatalf("Status = %v, want %v", jr.Status, StatusBlocked)
	}
	if jr.BlockedReason != "upstream failed: job_a" {
		t.Fatalf("BlockedReason = %q, want %q", jr.BlockedReason, "upstream failed: job_a")
	}
}

func TestJobRunEnsureStep(t *testing.T) {
	jr := &JobRun{Status: StatusQueued}
	step1 := Step{ID: "step_1", Name: "first"}
	step2 := Step{ID: "step_2", Name: "second"}

	sr1 := jr.EnsureStep(step1)
	if sr1 == nil {
		t.Fatal("EnsureStep returned nil for first step")
	}
	if sr1.StepID != "step_1" || sr1.Status != StatusQueued {
		t.Fatalf("first step = %+v, want StepID=step_1 Status=queued", sr1)
	}
	if len(jr.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(jr.Steps))
	}

	// Second call for same step returns the existing one (idempotent).
	sr1Again := jr.EnsureStep(step1)
	if sr1Again != sr1 {
		t.Fatal("EnsureStep must return the same pointer for the same step")
	}
	if len(jr.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1 (idempotent)", len(jr.Steps))
	}

	sr2 := jr.EnsureStep(step2)
	if sr2 == nil || sr2.StepID != "step_2" {
		t.Fatalf("second step = %+v, want StepID=step_2", sr2)
	}
	if len(jr.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(jr.Steps))
	}
}

func TestStepRunBeginRunning(t *testing.T) {
	sr := &StepRun{Status: StatusQueued}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	sr.BeginRunning(now)
	if sr.Status != StatusRunning {
		t.Fatalf("Status = %v, want %v", sr.Status, StatusRunning)
	}
	if sr.StartedAt == nil || !sr.StartedAt.Equal(now) {
		t.Fatalf("StartedAt = %v, want %v", sr.StartedAt, now)
	}
}

func TestStepRunFail(t *testing.T) {
	sr := &StepRun{Status: StatusRunning}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	sr.Fail(1, "exit code 1", now)
	if sr.Status != StatusFailed {
		t.Fatalf("Status = %v, want %v", sr.Status, StatusFailed)
	}
	if sr.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", sr.ExitCode)
	}
	if sr.Error != "exit code 1" {
		t.Fatalf("Error = %q, want %q", sr.Error, "exit code 1")
	}
	if sr.FinishedAt == nil || !sr.FinishedAt.Equal(now) {
		t.Fatalf("FinishedAt = %v, want %v", sr.FinishedAt, now)
	}
}

func TestStepRunSucceed(t *testing.T) {
	sr := &StepRun{Status: StatusRunning}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	sr.Succeed(0, now)
	if sr.Status != StatusSuccess {
		t.Fatalf("Status = %v, want %v", sr.Status, StatusSuccess)
	}
	if sr.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", sr.ExitCode)
	}
	if sr.FinishedAt == nil || !sr.FinishedAt.Equal(now) {
		t.Fatalf("FinishedAt = %v, want %v", sr.FinishedAt, now)
	}
}

func TestStepRunParkWait(t *testing.T) {
	sr := &StepRun{Status: StatusRunning}
	wakeAt := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	sr.ParkWait(wakeAt)
	if sr.Status != StatusWaiting {
		t.Fatalf("Status = %v, want %v", sr.Status, StatusWaiting)
	}
	if sr.WakeAt == nil || !sr.WakeAt.Equal(wakeAt) {
		t.Fatalf("WakeAt = %v, want %v", sr.WakeAt, wakeAt)
	}
}
