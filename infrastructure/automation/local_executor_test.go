package automation

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

func echoHello() string {
	return "echo hello-automation"
}

func sleepTen() string {
	if runtime.GOOS == "windows" {
		return "Start-Sleep -Seconds 10"
	}
	return "sleep 10"
}

func TestLocalExecutorRunsEcho(t *testing.T) {
	ex := &LocalExecutor{Root: t.TempDir()}
	dir := t.TempDir()
	run := &domain.WorkflowRun{TaskState: domain.TaskState[domain.RunStatus]{ID: "run1"}, WorkflowID: "wf"}
	jr := &domain.JobRun{TaskState: domain.TaskState[domain.RunStatus]{ID: "jr1"}, JobID: "j"}
	sr := &domain.StepRun{TaskState: domain.TaskState[domain.RunStatus]{ID: "s1"}, StepID: "step_1"}
	ws, err := ex.Prepare(context.Background(), application.PrepareRequest{
		Run: run, Job: domain.Job{ID: "j"}, JobRun: jr, Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	var logs []domain.LogChunk
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := ex.RunStep(ctx, application.RunStepRequest{
		Run: run, Job: domain.Job{ID: "j"}, JobRun: jr,
		Step: domain.Step{Run: echoHello()}, StepRun: sr,
		Workspace: ws,
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
	ex := &LocalExecutor{Root: t.TempDir()}
	run := &domain.WorkflowRun{TaskState: domain.TaskState[domain.RunStatus]{ID: "run2"}}
	jr := &domain.JobRun{TaskState: domain.TaskState[domain.RunStatus]{ID: "jr2"}, JobID: "j"}
	sr := &domain.StepRun{TaskState: domain.TaskState[domain.RunStatus]{ID: "s2"}}
	ws, err := ex.Prepare(context.Background(), application.PrepareRequest{
		Run: run, Job: domain.Job{ID: "j"}, JobRun: jr, Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, err := ex.RunStep(ctx, application.RunStepRequest{
		Run: run, JobRun: jr, Step: domain.Step{Run: sleepTen()}, StepRun: sr, Workspace: ws,
	})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancel took %s; process group was not interrupted", elapsed)
	}
	if err == nil && res.ExitCode == 0 {
		t.Fatal("expected cancellation")
	}
}
