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
	"time"

	"nusashell/application"
	"nusashell/domain"
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
	shell, args := selectShell(req.Step.Shell, req.Step.Run)
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Dir = req.Workspace.Dir
	cmd.Env = flattenEnv(req.Env)
	setProcGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &chunkWriter{req: req, stream: "stdout", buf: &stdout}
	cmd.Stderr = &chunkWriter{req: req, stream: "stderr", buf: &stderr}
	err := cmd.Run()
	res := application.StepResult{}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if ctx.Err() != nil {
			killProcGroup(cmd)
			res.Error = ctx.Err().Error()
			return res, ctx.Err()
		}
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
			Timestamp: time.Now().UTC(),
			Stream:    w.stream,
			Text:      string(p),
		})
	}
	return len(p), nil
}

func selectShell(explicit, command string) (string, []string) {
	shell := explicit
	if shell == "" || shell == "auto" {
		if runtime.GOOS == "windows" {
			shell = "powershell"
		} else {
			shell = "sh"
		}
	}
	switch shell {
	case "bash":
		return "bash", []string{"-c", command}
	case "pwsh":
		return "pwsh", []string{"-NoProfile", "-NonInteractive", "-Command", command}
	case "powershell":
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", command}
	default:
		return "sh", []string{"-c", command}
	}
}

func flattenEnv(env map[string]string) []string {
	base := os.Environ()
	if len(env) == 0 {
		return base
	}
	seen := map[string]int{}
	for i, kv := range base {
		if j := strings.IndexByte(kv, '='); j > 0 {
			seen[kv[:j]] = i
		}
	}
	for k, v := range env {
		entry := k + "=" + v
		if i, ok := seen[k]; ok {
			base[i] = entry
		} else {
			base = append(base, entry)
		}
	}
	return base
}

func CIEnv(run *domain.WorkflowRun, job domain.JobRun, step domain.StepRun, workspace string) map[string]string {
	return map[string]string{
		"NUSASHELL":             "true",
		"NUSASHELL_CI":          "true",
		"NUSASHELL_PIPELINE_ID": run.WorkflowID,
		"NUSASHELL_RUN_ID":      run.ID,
		"NUSASHELL_JOB_ID":      job.JobID,
		"NUSASHELL_STEP_ID":     step.StepID,
		"NUSASHELL_WORKSPACE":   workspace,
		"NUSASHELL_OS":          runtime.GOOS,
		"NUSASHELL_ARCH":        runtime.GOARCH,
	}
}
