package tools

// Built-in exec (the volcano island, hosted on the mainland). Runs a shell
// command in a child process with:
//   - platform process-group kill (POSIX pgid / Windows taskkill-free Kill)
//   - idle-watchdog: silence is a real failure; long work is not an error
//   - bounded output capture (head + tail)
// Cross-platform bits live in exec_unix.go / exec_windows.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nusashell/application"
)

const (
	execDefaultIdleTimeout = 180 * time.Second
	execMaxWatchTimeout    = time.Hour
	execHeadBytes          = 8 << 10
	execTailBytes          = 16 << 10
)

func execToolInfos() []application.ToolInfo {
	return []application.ToolInfo{
		{Name: "exec", Description: "Run a shell command as a child process and return combined stdout/stderr. Default shell: POSIX sh on Unix/macOS; on Windows auto-resolves Git Bash then PowerShell (cmd only via shell=\"cmd\"). Optional shell kind: bash, powershell, pwsh, cmd, wsl. No absolute wall-clock limit: a running command that keeps producing output keeps running. Silence longer than idle_timeout_ms (default 180000) cancels the run as failed. Optional timeout_ms adds an explicit hard cap. Long-lived processes are killed together with their children. On Windows, select shells via the shell parameter rather than invoking cmd.exe or powershell.exe inside a bash command line — MSYS path conversion mangles drive-letter paths such as Z:/x.", InputSchema: obj("object", props("command", str("Shell command to run"), "cwd", str("Optional working directory (absolute path); with shell=wsl WSL maps it under /mnt"), "idle_timeout_ms", intSchema("Cancel when no output for this long (default 180000, max 3600000)"), "timeout_ms", intSchema("Optional explicit wall-clock cap in milliseconds"), "shell", strEnum("Shell kind override (default auto: Git Bash when installed, else PowerShell on Windows; sh elsewhere)", "auto", "bash", "powershell", "pwsh", "cmd", "wsl")), "command")},
	}
}

// executeExecTool handles the exec built-in. Returns handled=false for other
// names.
func executeExecTool(ctx context.Context, name string, argsJSON []byte) (bool, string, error) {
	if name != "exec" {
		return false, "", nil
	}
	var args struct {
		Command       string `json:"command"`
		Cwd           string `json:"cwd"`
		IdleTimeoutMs int    `json:"idle_timeout_ms"`
		TimeoutMs     int    `json:"timeout_ms"`
		Shell         string `json:"shell"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return true, "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return true, "", fmt.Errorf("command is required")
	}
	if strings.TrimSpace(args.Cwd) != "" {
		if info, err := os.Stat(args.Cwd); err != nil || !info.IsDir() {
			return true, "", fmt.Errorf("cwd %q is not an existing directory", args.Cwd)
		}
	}
	idle := execDefaultIdleTimeout
	if args.IdleTimeoutMs > 0 {
		idle = time.Duration(args.IdleTimeoutMs) * time.Millisecond
	}
	if idle > execMaxWatchTimeout {
		idle = execMaxWatchTimeout
	}

	shellName, shellArgs := shellCommand(args.Shell, args.Command)
	cmd := exec.Command(shellName, shellArgs...)
	if strings.TrimSpace(args.Cwd) != "" {
		cmd.Dir = args.Cwd
	}
	applyPlatformAttrs(cmd)

	var lastOutput atomic.Int64
	lastOutput.Store(time.Now().UnixNano())
	out := newTailBuffer(execHeadBytes, execTailBytes)
	w := &outputWatcher{buf: out, last: &lastOutput}
	cmd.Stdout = w
	cmd.Stderr = w

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return true, "", fmt.Errorf("start: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var hardClock <-chan time.Time
	if args.TimeoutMs > 0 {
		t := time.NewTimer(time.Duration(args.TimeoutMs) * time.Millisecond)
		defer t.Stop()
		hardClock = t.C
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case waitErr := <-done:
			return true, renderExecResult(args.Command, started, waitErr, out.Snapshot()), nil
		case <-ctx.Done():
			killProcessTree(cmd)
			<-done
			return true, "", fmt.Errorf("exec cancelled: %w", ctx.Err())
		case <-hardClock:
			killProcessTree(cmd)
			<-done
			return true, "", fmt.Errorf("timeout_ms reached; partial output:\n%s", out.Snapshot())
		case <-tick.C:
			silentFor := time.Since(time.Unix(0, lastOutput.Load()))
			if silentFor >= idle {
				killProcessTree(cmd)
				<-done
				return true, "", fmt.Errorf("no output for %s (idle timeout); partial output:\n%s", silentFor.Round(time.Second), out.Snapshot())
			}
		}
	}
}

// pickAutoWindowsShell implements the documented Windows resolution order:
// Git Bash first (model-written commands are overwhelmingly POSIX syntax;
// Windows PowerShell 5.1 even lacks '&&'), then PowerShell (always present,
// the choice of VS Code/Copilot-style hooks), leaving cmd as the last
// resort handled by the caller.
func pickAutoWindowsShell(bashAvailable bool) string {
	if bashAvailable {
		return "bash"
	}
	return "powershell"
}

func renderExecResult(command string, started time.Time, waitErr error, output string) string {
	meta := map[string]any{
		"command":     command,
		"duration_ms": time.Since(started).Milliseconds(),
	}
	body := strings.TrimRight(output, "\n")
	if waitErr == nil {
		meta["exit_code"] = 0
	} else {
		var ee *exec.ExitError
		switch {
		case errors.As(waitErr, &ee):
			meta["exit_code"] = ee.ExitCode()
		default:
			meta["error"] = waitErr.Error()
		}
	}
	return yamlMD(meta, body)
}

// outputWriter feeds captured output into a bounded buffer while refreshing
// the last-activity timestamp consumed by the idle watchdog.
type outputWatcher struct {
	buf  *tailBuffer
	last *atomic.Int64
	mu   sync.Mutex
}

func (w *outputWatcher) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.last.Store(time.Now().UnixNano())
	w.mu.Unlock()
	return w.buf.Write(p)
}

// tailBuffer keeps the first headCap bytes and the last tailCap bytes of the
// combined output, eliding the middle once both are full.
type tailBuffer struct {
	mu      sync.Mutex
	head    []byte
	tail    []byte
	headCap int
	tailCap int
	dropped int64
}

func newTailBuffer(headCap, tailCap int) *tailBuffer {
	return &tailBuffer{headCap: headCap, tailCap: tailCap}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	total := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.head) < b.headCap {
		room := b.headCap - len(b.head)
		take := room
		if take > len(p) {
			take = len(p)
		}
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		b.tail = append(b.tail, p...)
		if over := len(b.tail) - b.tailCap; over > 0 {
			b.dropped += int64(over)
			b.tail = append([]byte(nil), b.tail[over:]...)
		}
	}
	return total, nil
}

// Snapshot returns head + elision marker + tail (or whatever exists).
func (b *tailBuffer) Snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.dropped == 0:
		return string(b.head) + string(b.tail)
	default:
		return string(b.head) + fmt.Sprintf("\n… [%d bytes elided] …\n", b.dropped) + string(b.tail)
	}
}
