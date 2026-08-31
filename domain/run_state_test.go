package domain

import (
	"testing"
	"time"
)

func TestWorkflowRunStartRun(t *testing.T) {
	t.Run("empty status becomes queued", func(t *testing.T) {
		run := &WorkflowRun{}
		now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		run.StartRun(now)
		if run.Status != StatusQueued {
			t.Fatalf("Status = %v, want %v", run.Status, StatusQueued)
		}
		if !run.CreatedAt.Equal(now) {
			t.Fatalf("CreatedAt = %v, want %v", run.CreatedAt, now)
		}
	})
	t.Run("already queued stays queued but stamps CreatedAt", func(t *testing.T) {
		run := &WorkflowRun{Status: StatusQueued}
		now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		run.StartRun(now)
		if run.Status != StatusQueued {
			t.Fatalf("Status = %v, want %v", run.Status, StatusQueued)
		}
		if !run.CreatedAt.Equal(now) {
			t.Fatalf("CreatedAt = %v, want %v", run.CreatedAt, now)
		}
	})
	t.Run("terminal run is not restarted", func(t *testing.T) {
		run := &WorkflowRun{Status: StatusSuccess}
		run.StartRun(time.Now())
		if run.Status != StatusSuccess {
			t.Fatalf("Status = %v, want %v (terminal must not restart)", run.Status, StatusSuccess)
		}
	})
}

func TestWorkflowRunBeginRunning(t *testing.T) {
	t.Run("queued becomes running with StartedAt", func(t *testing.T) {
		run := &WorkflowRun{Status: StatusQueued}
		now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		run.BeginRunning(now)
		if run.Status != StatusRunning {
			t.Fatalf("Status = %v, want %v", run.Status, StatusRunning)
		}
		if run.StartedAt == nil || !run.StartedAt.Equal(now) {
			t.Fatalf("StartedAt = %v, want %v", run.StartedAt, now)
		}
	})
	t.Run("waiting run is not disturbed", func(t *testing.T) {
		wakeAt := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
		run := &WorkflowRun{Status: StatusWaiting, WakeAt: &wakeAt}
		run.BeginRunning(time.Now())
		if run.Status != StatusWaiting {
			t.Fatalf("Status = %v, want %v (waiting must not be disturbed)", run.Status, StatusWaiting)
		}
		if run.WakeAt == nil || !run.WakeAt.Equal(wakeAt) {
			t.Fatalf("WakeAt changed = %v, want %v", run.WakeAt, wakeAt)
		}
	})
	t.Run("running run keeps first StartedAt", func(t *testing.T) {
		first := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		run := &WorkflowRun{Status: StatusRunning, StartedAt: &first}
		run.BeginRunning(time.Now())
		if run.StartedAt == nil || !run.StartedAt.Equal(first) {
			t.Fatalf("StartedAt = %v, want %v (first must be kept)", run.StartedAt, first)
		}
	})
}

func TestWorkflowRunParkWait(t *testing.T) {
	run := &WorkflowRun{Status: StatusRunning}
	wakeAt := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	run.ParkWait(wakeAt)
	if run.Status != StatusWaiting {
		t.Fatalf("Status = %v, want %v", run.Status, StatusWaiting)
	}
	if run.WakeAt == nil || !run.WakeAt.Equal(wakeAt) {
		t.Fatalf("WakeAt = %v, want %v", run.WakeAt, wakeAt)
	}
}

func TestWorkflowRunParkBlocked(t *testing.T) {
	run := &WorkflowRun{Status: StatusRunning}
	reason := "capability missing"
	run.ParkBlocked(reason)
	if run.Status != StatusBlocked {
		t.Fatalf("Status = %v, want %v", run.Status, StatusBlocked)
	}
	if run.BlockedReason != reason {
		t.Fatalf("BlockedReason = %q, want %q", run.BlockedReason, reason)
	}
}

func TestWorkflowRunFailDAG(t *testing.T) {
	run := &WorkflowRun{Status: StatusRunning}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	run.FailDAG(now)
	if run.Status != StatusFailed {
		t.Fatalf("Status = %v, want %v", run.Status, StatusFailed)
	}
	if run.FinishedAt == nil || !run.FinishedAt.Equal(now) {
		t.Fatalf("FinishedAt = %v, want %v", run.FinishedAt, now)
	}
}

func TestWorkflowRunFinalize(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	t.Run("all success becomes success", func(t *testing.T) {
		run := &WorkflowRun{
			Status: StatusRunning,
			Jobs:   []JobRun{{Status: StatusSuccess}, {Status: StatusSkipped}},
		}
		run.Finalize(now, run.Summary())
		if run.Status != StatusSuccess {
			t.Fatalf("Status = %v, want %v", run.Status, StatusSuccess)
		}
		if run.FinishedAt == nil {
			t.Fatal("FinishedAt must be set on success")
		}
	})
	t.Run("any failed becomes failed", func(t *testing.T) {
		run := &WorkflowRun{
			Status: StatusRunning,
			Jobs:   []JobRun{{Status: StatusSuccess}, {Status: StatusFailed}},
		}
		run.Finalize(now, run.Summary())
		if run.Status != StatusFailed {
			t.Fatalf("Status = %v, want %v", run.Status, StatusFailed)
		}
	})
	t.Run("blocked with incomplete becomes failed", func(t *testing.T) {
		run := &WorkflowRun{
			Status: StatusRunning,
			Jobs:   []JobRun{{Status: StatusSuccess}, {Status: StatusBlocked}},
		}
		run.Finalize(now, run.Summary())
		if run.Status != StatusFailed {
			t.Fatalf("Status = %v, want %v", run.Status, StatusFailed)
		}
	})
	t.Run("waiting run is not finalized", func(t *testing.T) {
		wakeAt := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
		run := &WorkflowRun{Status: StatusWaiting, WakeAt: &wakeAt}
		run.Finalize(now, run.Summary())
		if run.Status != StatusWaiting {
			t.Fatalf("Status = %v, want %v (waiting must not finalize)", run.Status, StatusWaiting)
		}
		if run.FinishedAt != nil {
			t.Fatal("FinishedAt must not be set while waiting")
		}
	})
	t.Run("blocked run is not finalized", func(t *testing.T) {
		run := &WorkflowRun{Status: StatusBlocked, BlockedReason: "cap missing"}
		run.Finalize(now, run.Summary())
		if run.Status != StatusBlocked {
			t.Fatalf("Status = %v, want %v (blocked must not finalize)", run.Status, StatusBlocked)
		}
	})
	t.Run("in-flight jobs prevent finalize", func(t *testing.T) {
		run := &WorkflowRun{
			Status: StatusRunning,
			Jobs:   []JobRun{{Status: StatusSuccess}, {Status: StatusRunning}},
		}
		run.Finalize(now, run.Summary())
		if run.Status != StatusRunning {
			t.Fatalf("Status = %v, want %v (in-flight must not finalize)", run.Status, StatusRunning)
		}
		if run.FinishedAt != nil {
			t.Fatal("FinishedAt must not be set while jobs in-flight")
		}
	})
}

func TestWorkflowRunCancel(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	t.Run("cancels run and active jobs", func(t *testing.T) {
		run := &WorkflowRun{
			Status: StatusRunning,
			Jobs: []JobRun{
				{Status: StatusRunning},
				{Status: StatusQueued},
				{Status: StatusSuccess},
			},
		}
		run.Cancel(now)
		if run.Status != StatusCancelled {
			t.Fatalf("Status = %v, want %v", run.Status, StatusCancelled)
		}
		if run.FinishedAt == nil || !run.FinishedAt.Equal(now) {
			t.Fatalf("FinishedAt = %v, want %v", run.FinishedAt, now)
		}
		if run.Jobs[0].Status != StatusCancelled {
			t.Fatalf("job[0] = %v, want %v", run.Jobs[0].Status, StatusCancelled)
		}
		if run.Jobs[1].Status != StatusCancelled {
			t.Fatalf("job[1] = %v, want %v", run.Jobs[1].Status, StatusCancelled)
		}
		if run.Jobs[2].Status != StatusSuccess {
			t.Fatalf("job[2] = %v, want %v (terminal must not be touched)", run.Jobs[2].Status, StatusSuccess)
		}
	})
	t.Run("already terminal run is not re-cancelled", func(t *testing.T) {
		run := &WorkflowRun{Status: StatusSuccess}
		run.Cancel(now)
		if run.Status != StatusSuccess {
			t.Fatalf("Status = %v, want %v (terminal must not be re-cancelled)", run.Status, StatusSuccess)
		}
	})
}
