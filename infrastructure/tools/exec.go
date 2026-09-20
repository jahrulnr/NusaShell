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
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

const (
	execDefaultIdleTimeout = 180 * time.Second
	execMaxWatchTimeout    = time.Hour
	// execInlineMaxBytes is the in-band stdout/stderr budget shown to the
	// model: first half + last half, middle dropped. Matches Cursor Shell's
	// "truncated to 20000 characters" head+tail sample.
	execInlineMaxBytes   = 20_000
	execHeadBytes        = execInlineMaxBytes / 2
	execTailBytes        = execInlineMaxBytes / 2
	execTruncationMarker = "\n... (output truncated) ...\n"
)

func execToolInfos() []application.ToolInfo {
	return []application.ToolInfo{
		{Name: "exec", Description: "Run a shell command as a child process and return combined stdout/stderr (op=run, default). op=run with background=true detaches the process instead: it returns immediately with exec_id, pid, and log_path — stdout/stderr keep streaming live to the log file — while status/wait/kill/list ops manage it (id required for status/wait/kill). At most 8 background processes may run per conversation; a killed or exited process frees its slot. Background processes have no idle watchdog (a quiet server stays up), still honor an explicit timeout_ms hard cap, survive across tool calls, and are all killed when the app shuts down. Default shell: POSIX sh on Unix/macOS; on Windows auto-resolves Git Bash then PowerShell (cmd only via shell=\"cmd\"). Optional shell kind: bash, powershell, pwsh, cmd, wsl. Foreground runs have no absolute wall-clock limit: a running command that keeps producing output keeps running; silence longer than idle_timeout_ms (default 180000, ignored for background) cancels the run as failed. Long-lived processes are killed together with their children. On Windows, select shells via the shell parameter rather than invoking cmd.exe or powershell.exe inside a bash command line — MSYS path conversion mangles drive-letter paths such as Z:/x. Combined output is streamed live. In-band stdout/stderr is capped at 20000 characters as a 50/50 head+tail sample with \"... (output truncated) ...\" in the middle. When the full log is larger, overflow_path is an absolute file under the platform temp dir (nusashell/); file_read it from offset 0 for the complete stdout/stderr. For a background process, file_read log_path instead.", InputSchema: obj("object", props("op", strEnum("run (default) executes command and waits; status inspects; wait blocks until exit or timeout_ms; kill terminates the process tree (SIGTERM, then SIGKILL); list shows this conversation's background processes", "run", "status", "wait", "kill", "list"), "command", str("Shell command to run (required for op=run)"), "id", str("exec_id returned by a backgrounded run (required for op=status, wait, kill)"), "background", map[string]any{"type": "boolean", "description": "op=run only: detach and return exec_id + log_path immediately instead of waiting"}, "cwd", str("Optional working directory (absolute path); with shell=wsl WSL maps it under /mnt"), "idle_timeout_ms", intSchema("Foreground op=run only: cancel when no output for this long (default 180000, max 3600000)"), "timeout_ms", intSchema("op=run (foreground or background): optional wall-clock cap; op=wait: max wait duration (default waits until exit)"), "shell", strEnum("Shell kind override (default auto: Git Bash when installed, else PowerShell on Windows; sh elsewhere)", "auto", "bash", "powershell", "pwsh", "cmd", "wsl")))},
	}
}

// execArgs is the full exec call payload across all ops.
type execArgs struct {
	Op            string `json:"op"`
	Command       string `json:"command"`
	ID            string `json:"id"`
	Cwd           string `json:"cwd"`
	IdleTimeoutMs int    `json:"idle_timeout_ms"`
	TimeoutMs     int    `json:"timeout_ms"`
	Shell         string `json:"shell"`
	Background    bool   `json:"background"`
}

// dispatchExec is the Toolbox entry for the exec built-in: it routes the op
// field between the synchronous runner (run) and the background-process
// registry (background spawn, status, wait, kill, list).
func (t *Toolbox) dispatchExec(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (bool, string, error) {
	if name != "exec" {
		return false, "", nil
	}
	var args execArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return true, "", fmt.Errorf("invalid args: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(args.Op)) {
	case "", "run":
		if args.Background {
			out, err := t.execSpawnBackground(ctx, &args)
			return true, out, err
		}
		out, err := runExecSync(ctx, &args, onChunk)
		return true, out, err
	case "status":
		out, err := t.execStatus(ctx, args.ID)
		return true, out, err
	case "wait":
		out, err := t.execWait(ctx, args.ID, args.TimeoutMs)
		return true, out, err
	case "kill":
		out, err := t.execKill(ctx, args.ID)
		return true, out, err
	case "list":
		out, err := t.execList(ctx)
		return true, out, err
	default:
		return true, "", fmt.Errorf("unknown exec op %q; valid ops: run, status, wait, kill, list", args.Op)
	}
}

// runExecSync executes the foreground op=run path: spawn, stream output,
// idle watchdog, optional hard cap, kill the whole tree on cancel/timeout.
func runExecSync(ctx context.Context, args *execArgs, onChunk func(string)) (string, error) {
	if strings.TrimSpace(args.Command) == "" {
		return "", fmt.Errorf("command is required")
	}
	if strings.TrimSpace(args.Cwd) != "" {
		if info, err := os.Stat(args.Cwd); err != nil || !info.IsDir() {
			return "", fmt.Errorf("cwd %q is not an existing directory", args.Cwd)
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
		return "", fmt.Errorf("start: %w", err)
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
			return yamlMD(meta, body), nil
		case <-ctx.Done():
			killProcessTree(cmd)
			<-done
			closeAndDropSpill(spill, spillPath)
			return "", fmt.Errorf("exec cancelled: %w\npartial output:\n%s", ctx.Err(), out.Snapshot())
		case <-hardClock:
			killProcessTree(cmd)
			<-done
			closeAndDropSpill(spill, spillPath)
			return "", fmt.Errorf("timeout_ms reached; partial output:\n%s", out.Snapshot())
		case <-tick.C:
			silentFor := clock.NewTime().Since(clock.NewTime(time.Unix(0, lastOutput.Load())).Time())
			if silentFor >= idle {
				killProcessTree(cmd)
				<-done
				closeAndDropSpill(spill, spillPath)
				return "", fmt.Errorf("no output for %s (idle timeout); partial output:\n%s", silentFor.Round(time.Second), out.Snapshot())
			}
		}
	}
}

// execSpawnBackground implements op=run with background=true: the process is
// detached from the tool-call context, registered under the caller's
// conversation (enforcing the per-conversation budget), and its combined
// stdout/stderr streams live to terminal/<exec_id>.log under the platform
// temp dir. The call returns immediately with exec_id + log_path.
func (t *Toolbox) execSpawnBackground(ctx context.Context, args *execArgs) (string, error) {
	if strings.TrimSpace(args.Command) == "" {
		return "", fmt.Errorf("command is required")
	}
	if strings.TrimSpace(args.Cwd) != "" {
		if info, err := os.Stat(args.Cwd); err != nil || !info.IsDir() {
			return "", fmt.Errorf("cwd %q is not an existing directory", args.Cwd)
		}
	}
	convID := application.ConversationIDFromContext(ctx)
	shellName, shellArgs := shellCommand(args.Shell, args.Command)
	cmd := exec.Command(shellName, shellArgs...)
	if strings.TrimSpace(args.Cwd) != "" {
		cmd.Dir = args.Cwd
	}
	applyPlatformAttrs(cmd)

	reg := t.execRegistry()
	id := domain.NewID(domain.IDPrefixExec)
	logFile, logPath, err := createExecLogFile(id)
	if err != nil {
		return "", fmt.Errorf("create exec log: %w", err)
	}
	var lastOutput atomic.Int64
	lastOutput.Store(clock.NewTime().EpochNano())
	buf := newTailBuffer(execHeadBytes, execTailBytes)
	w := &outputWatcher{buf: buf, last: &lastOutput, spill: logFile}
	cmd.Stdout = w
	cmd.Stderr = w

	var timeout time.Duration
	if args.TimeoutMs > 0 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}
	p, err := reg.add(id, convID, args.Command, cmd, logFile, logPath, buf, timeout)
	if err != nil {
		_ = logFile.Close()
		_ = os.Remove(logPath)
		return "", err
	}
	if err := cmd.Start(); err != nil {
		reg.remove(p.id)
		_ = logFile.Close()
		_ = os.Remove(logPath)
		return "", fmt.Errorf("start: %w", err)
	}
	p.markStarted()
	go p.waitLoop()
	return yamlMD(map[string]any{
		"exec_id":    p.id,
		"status":     "running",
		"pid":        cmd.Process.Pid,
		"command":    args.Command,
		"log_path":   logPath,
		"bg_running": reg.runningCount(convID),
		"bg_max":     reg.limit,
	}, ""), nil
}

// execLookup resolves an exec_id scoped to the caller's conversation:
// processes owned by other conversations report not-found, never leak.
func (t *Toolbox) execLookup(ctx context.Context, id string) (*bgExec, error) {
	id = strings.TrimSpace(id)
	if !validExecID(id) {
		return nil, fmt.Errorf("invalid or missing exec id %q", id)
	}
	p, ok := t.execRegistry().get(id)
	if !ok || p.convID != application.ConversationIDFromContext(ctx) {
		return nil, fmt.Errorf("background process %q not found (op=list shows this conversation's processes)", id)
	}
	return p, nil
}

func (t *Toolbox) execStatus(ctx context.Context, id string) (string, error) {
	p, err := t.execLookup(ctx, id)
	if err != nil {
		return "", err
	}
	return yamlMD(p.meta(), ""), nil
}

// execWait blocks until the process exits, timeout_ms elapses, or the call
// is cancelled. A wait timeout is not an error: the result reports the still
// running status plus the output tail captured so far.
func (t *Toolbox) execWait(ctx context.Context, id string, timeoutMs int) (string, error) {
	p, err := t.execLookup(ctx, id)
	if err != nil {
		return "", err
	}
	var timeoutC <-chan time.Time
	if timeoutMs > 0 {
		t := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
		defer t.Stop()
		timeoutC = t.C
	}
	select {
	case <-p.done:
		return yamlMD(p.meta(), strings.TrimRight(p.buf.Snapshot(), "\n")), nil
	case <-timeoutC:
		meta := p.meta()
		meta["wait_timeout"] = true
		return yamlMD(meta, strings.TrimRight(p.buf.Snapshot(), "\n")), nil
	case <-ctx.Done():
		return "", fmt.Errorf("exec wait cancelled: %w\npartial output:\n%s", ctx.Err(), p.buf.Snapshot())
	}
}

// execKill terminates the whole process tree: SIGTERM first so servers can
// shut down cleanly, escalating to SIGKILL after the grace window.
func (t *Toolbox) execKill(ctx context.Context, id string) (string, error) {
	p, err := t.execLookup(ctx, id)
	if err != nil {
		return "", err
	}
	p.terminate()
	return yamlMD(p.meta(), ""), nil
}

func (t *Toolbox) execList(ctx context.Context) (string, error) {
	reg := t.execRegistry()
	convID := application.ConversationIDFromContext(ctx)
	procs := reg.list(convID)
	items := make([]any, 0, len(procs))
	for _, p := range procs {
		items = append(items, p.meta())
	}
	return yamlJSONL(map[string]any{
		"count":  len(items),
		"bg_max": reg.limit,
	}, items), nil
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

// Snapshot returns head + truncation marker + tail (or whatever exists).
func (b *tailBuffer) Snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.dropped == 0:
		return string(b.head) + string(b.tail)
	default:
		return string(b.head) + execTruncationMarker + string(b.tail)
	}
}
