package ci

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

func TestLocalExecutorRunsEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix echo")
	}
	ex := &LocalExecutor{Root: t.TempDir()}
	dir := t.TempDir()
	run := &domain.WorkflowRun{ID: "run1", WorkflowID: "wf"}
	jr := &domain.JobRun{ID: "jr1", JobID: "j"}
	sr := &domain.StepRun{ID: "s1", StepID: "step_1"}
	ws, err := ex.Prepare(context.Background(), application.PrepareRequest{
		Run: run, Job: domain.Job{ID: "j"}, JobRun: jr, Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	var logs []domain.LogChunk
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := ex.RunStep(ctx, application.RunStepRequest{
		Run: run, Job: domain.Job{ID: "j"}, JobRun: jr,
		Step: domain.Step{Run: "echo hello-ci"}, StepRun: sr,
		Workspace: ws,
		Env:       CIEnv(run, *jr, *sr, ws.Dir),
		OnOutput:  func(c domain.LogChunk) { logs = append(logs, c) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit %d err=%s", res.ExitCode, res.Error)
	}
	found := false
	for _, l := range logs {
		if l.Stream == "stdout" && len(l.Text) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no stdout logs: %+v", logs)
	}
	if _, err := os.Stat(filepath.Join(ex.Root, "run1", "jr1", "workspace")); err != nil {
		t.Fatal(err)
	}
}

func TestLocalExecutorCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sleep")
	}
	ex := &LocalExecutor{Root: t.TempDir()}
	run := &domain.WorkflowRun{ID: "run2"}
	jr := &domain.JobRun{ID: "jr2", JobID: "j"}
	sr := &domain.StepRun{ID: "s2"}
	ws, err := ex.Prepare(context.Background(), application.PrepareRequest{
		Run: run, Job: domain.Job{ID: "j"}, JobRun: jr, Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res, err := ex.RunStep(ctx, application.RunStepRequest{
		Run: run, JobRun: jr, Step: domain.Step{Run: "sleep 10"}, StepRun: sr, Workspace: ws,
	})
	if err == nil && res.ExitCode == 0 {
		t.Fatal("expected cancellation")
	}
}
