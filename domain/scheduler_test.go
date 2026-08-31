package domain

import (
	"testing"
	"time"
)

func TestSchedulerPolicyConstants(t *testing.T) {
	if JobLease != 30*time.Second {
		t.Errorf("JobLease = %v, want 30s", JobLease)
	}
	if JobHeartbeat != 10*time.Second {
		t.Errorf("JobHeartbeat = %v, want 10s", JobHeartbeat)
	}
	if MaxParallelJobs != 4 {
		t.Errorf("MaxParallelJobs = %d, want 4", MaxParallelJobs)
	}
	if MaxFanout != 32 {
		t.Errorf("MaxFanout = %d, want 32", MaxFanout)
	}
}

func TestWakeWaitingRunTransitionsToQueued(t *testing.T) {
	run := &WorkflowRun{
		Status: StatusWaiting,
		WakeAt: ptrTime(time.Now().Add(-time.Hour)),
		Jobs: []JobRun{
			{
				Status: StatusWaiting,
				Steps: []StepRun{
					{Status: StatusWaiting},
					{Status: StatusSuccess},
				},
			},
			{Status: StatusQueued},
		},
	}
	WakeWaitingRun(run)
	if run.Status != StatusQueued {
		t.Fatalf("run status = %v, want %v", run.Status, StatusQueued)
	}
	if run.WakeAt != nil {
		t.Fatalf("WakeAt = %v, want nil", run.WakeAt)
	}
	if run.Jobs[0].Status != StatusQueued {
		t.Fatalf("job[0] status = %v, want %v", run.Jobs[0].Status, StatusQueued)
	}
	if run.Jobs[0].Steps[0].Status != StatusQueued {
		t.Fatalf("step[0][0] status = %v, want %v", run.Jobs[0].Steps[0].Status, StatusQueued)
	}
	if run.Jobs[0].Steps[1].Status != StatusSuccess {
		t.Fatalf("step[0][1] status = %v, want %v (must not touch terminal)", run.Jobs[0].Steps[1].Status, StatusSuccess)
	}
	if run.Jobs[1].Status != StatusQueued {
		t.Fatalf("job[1] status = %v, want %v (already queued, unchanged)", run.Jobs[1].Status, StatusQueued)
	}
}

func TestWakeWaitingRunNoOpWhenNotWaiting(t *testing.T) {
	run := &WorkflowRun{Status: StatusRunning}
	WakeWaitingRun(run)
	if run.Status != StatusRunning {
		t.Fatalf("status = %v, want %v (must not touch non-waiting)", run.Status, StatusRunning)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
