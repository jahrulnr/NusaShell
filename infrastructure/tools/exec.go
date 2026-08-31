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
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nusashell/application"
	clock "nusashell/pkg/time"
)

const (
	execDefaultIdleTimeout = 180 * time.Second
	execMaxWatchTimeout    = time.Hour
	execHeadBytes          = 8 << 10
	execTailBytes          = 16 << 10
)

func execToolInfos() []application.ToolInfo {
	return []application.ToolInfo{
		{Name: "exec", Description: "Run a shell command as a child process and return combined stdout/stderr. Default shell: POSIX sh on Unix/macOS; on Windows auto-resolves Git Bash then PowerShell (cmd only via shell=\"cmd\"). Optional shell kind: bash, powershell, pwsh, cmd, wsl. No absolute wall-clock limit: a running command that keeps producing output keeps running. Silence longer than idle_timeout_ms (default 180000) cancels the run as failed. Optional timeout_ms adds an explicit hard cap. Long-lived processes are killed together with their children. On Windows, select shells via the shell parameter rather than invoking cmd.exe or powershell.exe inside a bash command line — MSYS path conversion mangles drive-letter paths such as Z:/x. Combined output is streamed live (head+tail elision in-band). When the full log exceeds ~32KiB, overflow_path is an absolute file under the platform temp dir (nusashell/); file_read it from offset 0 for the complete stdout/stderr.", InputSchema: obj("object", props("command", str("Shell command to run"), "cwd", str("Optional working directory (absolute path); with shell=wsl WSL maps it under /mnt"), "idle_timeout_ms", intSchema("Cancel when no output for this long (default 180000, max 3600000)"), "timeout_ms", intSchema("Optional explicit wall-clock cap in milliseconds"), "shell", strEnum("Shell kind override (default auto: Git Bash when installed, else PowerShell on Windows; sh elsewhere)", "auto", "bash", "powershell", "pwsh", "cmd", "wsl")), "command")},
	}
}

// executeExecToolChunks runs the exec built-in and streams stdout/stderr
// chunks to onChunk as they arrive. It is the streaming variant of
// executeExecTool; when onChunk is nil the behavior is identical to the
// plain path (bounded capture only, emitted as the final result).
func executeExecToolChunks(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (bool, string, error) {
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
	lastOutput.Store(clock.NewTime().EpochNano())
	out := newTailBuffer(execHeadBytes, execTailBytes)
	spill, spillPath, spillErr := createToolOverflowFile("exec")
	w := &outputWatcher{buf: out, last: &lastOutput, chunk: onChunk}
	if spillErr == nil {
		w.spill = spill
	}
	cmd.Stdout = w
	cmd.Stderr = w

	started := clock.NewTime().Time()
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
			meta := map[string]any{
				"duration_ms": clock.NewTime().Since(started).Milliseconds(),
			}
			body := strings.TrimRight(out.Snapshot(), "\n")
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
			attachExecOverflow(meta, spill, spillPath, out)
			return true, yamlMD(meta, body), nil
		case <-ctx.Done():
			killProcessTree(cmd)
			<-done
			closeAndDropSpill(spill, spillPath)
			return true, "", fmt.Errorf("exec cancelled: %w\npartial output:\n%s", ctx.Err(), out.Snapshot())
		case <-hardClock:
			killProcessTree(cmd)
			<-done
			closeAndDropSpill(spill, spillPath)
			return true, "", fmt.Errorf("timeout_ms reached; partial output:\n%s", out.Snapshot())
		case <-tick.C:
			silentFor := clock.NewTime().Since(clock.NewTime(time.Unix(0, lastOutput.Load())).Time())
			if silentFor >= idle {
				killProcessTree(cmd)
				<-done
				closeAndDropSpill(spill, spillPath)
				return true, "", fmt.Errorf("no output for %s (idle timeout); partial output:\n%s", silentFor.Round(time.Second), out.Snapshot())
			}
		}
	}
}

// executeExecTool runs the exec built-in. Returns handled=false for other
// names.
func executeExecTool(ctx context.Context, name string, argsJSON []byte) (bool, string, error) {
	return executeExecToolChunks(ctx, name, argsJSON, nil)
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

// outputWriter feeds captured output into a bounded buffer while refreshing
// the last-activity timestamp consumed by the idle watchdog, and forwards
// each write to the streaming callback when one is attached.
type outputWatcher struct {
	buf   *tailBuffer
	last  *atomic.Int64
	chunk func(string)
	spill io.Writer
	mu    sync.Mutex
}

func (w *outputWatcher) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.last.Store(clock.NewTime().EpochNano())
	if w.chunk != nil {
		w.chunk(string(p))
	}
	if w.spill != nil {
		_, _ = w.spill.Write(p)
	}
	w.mu.Unlock()
	return w.buf.Write(p)
}

func attachExecOverflow(meta map[string]any, spill *os.File, path string, buf *tailBuffer) {
	if spill == nil || path == "" {
		return
	}
	_ = spill.Sync()
	info, err := spill.Stat()
	_ = spill.Close()
	var size int64
	if err == nil {
		size = info.Size()
	}
	keep := size > int64(toolInlineMaxBytes) || buf.droppedBytes() > 0
	attachSpillMeta(meta, path, size, keep)
}

func closeAndDropSpill(spill *os.File, path string) {
	if spill != nil {
		_ = spill.Close()
	}
	if path != "" {
		_ = os.Remove(path)
	}
}

func (b *tailBuffer) droppedBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
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
