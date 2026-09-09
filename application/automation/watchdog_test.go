package automation

import (
	"context"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

// wedgedAgentStepRunner simulates an agent turn that never returns even after
// its context ends — the failure mode that previously left runs stuck in
// "running" forever because the executor waited on the step unconditionally.
type wedgedAgentStepRunner struct{}

func (wedgedAgentStepRunner) RunAgentStep(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, conversationID string, onUpdate func(string)) (map[string]any, string, error) {
	select {}
}

// TestAgentStepWedgedIsFinalizedWhenContextEnds guards the executor watchdog:
// when the job context ends while an agent step has not returned, the step and
// job must be finalized with a visible reason instead of hanging forever.
func TestAgentStepWedgedIsFinalizedWhenContextEnds(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	svc.Exec.Agent = wedgedAgentStepRunner{}
	workflow := &domain.WorkflowDefinition{
		ID:   "wedged-agent",
		Name: "Wedged agent",
		Jobs: []domain.Job{{ID: "reply", Steps: []domain.Step{{ID: "agent-reply", Agent: &domain.AgentStep{Prompt: "reply"}}}}},
	}
	if result := domain.ValidateSyntax(workflow); result.Verdict() != "VALID" {
		t.Fatalf("workflow must validate: %+v", result.Issues)
	}
	run := NewWorkflowRun(*workflow, "event")
	if err := svc.Exec.Runs.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Exec.runJob(ctx, run.ID, "reply") }()

	// Wait until the job is durably running (the watchdog is installed with
	// the job context), then cancel like automation(op="cancel") would.
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := svc.Exec.Runs.Get(context.Background(), run.ID)
		if err == nil {
			if jr := got.JobRunByID("reply"); jr != nil && jr.Status == domain.StatusRunning {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not reach running state")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runJob error = %v, want nil after persisted finalization", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runJob did not finalize the wedged agent step after context cancel")
	}
	got, err := svc.Exec.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobRun := got.JobRunByID("reply")
	if jobRun == nil {
		t.Fatal("reply job run missing")
	}
	if jobRun.Status != domain.StatusFailed {
		t.Fatalf("reply job status = %s, want failed", jobRun.Status)
	}
	if !strings.Contains(jobRun.Error, "agent step interrupted") {
		t.Fatalf("reply job error = %q, want watchdog interruption diagnostic", jobRun.Error)
	}
}
