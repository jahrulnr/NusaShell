package ci

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// LocalExecutor runs steps on the host shell in the selected workspace.
type LocalExecutor struct {
	Root string
}

func (e *LocalExecutor) Prepare(_ context.Context, req application.PrepareRequest) (application.ExecutionWorkspace, error) {
	jobDir := filepath.Join(e.Root, req.Run.ID, req.JobRun.ID)
	ws := filepath.Join(jobDir, "workspace")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		return application.ExecutionWorkspace{}, err
	}
	if err := os.MkdirAll(filepath.Join(jobDir, "logs"), 0o700); err != nil {
		return application.ExecutionWorkspace{}, err
	}
	// Phase 1: execute in the selected workspace when present.
	dir := req.Workspace
	if dir == "" {
		dir = ws
	}
	return application.ExecutionWorkspace{Dir: dir}, nil
}

func (e *LocalExecutor) Cleanup(_ context.Context, _ application.CleanupRequest) error {
	return nil
}

func (e *LocalExecutor) RunStep(ctx context.Context, req application.RunStepRequest) (application.StepResult, error) {
	if req.Step.Run == "" {
		return application.StepResult{Error: "local executor only runs shell steps"}, fmt.Errorf("no run command")
	}
	shell := req.Step.Shell
	if shell == "" || shell == "auto" {
		if runtime.GOOS == "windows" {
			shell = "powershell"
		} else {
			shell = "sh"
		}
	}
	var args []string
	switch shell {
	case "bash":
		args = []string{"-c", req.Step.Run}
	case "pwsh":
		args = []string{"-NoProfile", "-NonInteractive", "-Command", req.Step.Run}
	case "powershell":
		args = []string{"-NoProfile", "-NonInteractive", "-Command", req.Step.Run}
	default:
		args = []string{"-c", req.Step.Run}
	}
	cmd := exec.Command(shell, args...)
	cmd.Dir = req.Workspace.Dir
	env := os.Environ()
	if len(req.Env) > 0 {
		seen := map[string]int{}
		for i, kv := range env {
			if j := strings.IndexByte(kv, '='); j > 0 {
				seen[kv[:j]] = i
			}
		}
		for k, v := range req.Env {
			entry := k + "=" + v
			if i, ok := seen[k]; ok {
				env[i] = entry
			} else {
				env = append(env, entry)
			}
		}
	}
	cmd.Env = env
	setProcGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &chunkWriter{req: req, stream: "stdout", buf: &stdout}
	cmd.Stderr = &chunkWriter{req: req, stream: "stderr", buf: &stderr}
	if err := cmd.Start(); err != nil {
		return application.StepResult{Error: err.Error()}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		killProcGroup(cmd)
		<-done // wait for the process to exit before returning
		res := application.StepResult{Error: ctx.Err().Error()}
		if cmd.ProcessState != nil {
			res.ExitCode = cmd.ProcessState.ExitCode()
		}
		return res, ctx.Err()
	}
	res := application.StepResult{}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	return res, nil
}

type chunkWriter struct {
	req    application.RunStepRequest
	stream string
	buf    *bytes.Buffer
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if w.req.OnOutput != nil {
		w.req.OnOutput(domain.LogChunk{
			RunID:     w.req.Run.ID,
			JobID:     w.req.JobRun.ID,
			StepID:    w.req.StepRun.ID,
			Timestamp: clock.NewTime().Time(),
			Stream:    w.stream,
			Text:      string(p),
		})
	}
	return len(p), nil
}
