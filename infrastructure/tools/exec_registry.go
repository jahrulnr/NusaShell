package tools

// Detached background exec processes (exec background=true plus the
// status/wait/kill/list ops). The registry is owned by the shared Toolbox and
// enforces a per-conversation budget so an agent cannot spawn unbounded
// processes. Processes outlive the spawning tool call: they die on exit,
// op=kill, an optional timeout_ms hard cap, or Toolbox.Close at shutdown.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nusashell/domain"
	"nusashell/infrastructure/nusatemp"
	clock "nusashell/pkg/time"
)

// execMaxBackgroundPerConversation caps simultaneously running detached
// processes per conversation. Finished or killed processes free their slot.
const execMaxBackgroundPerConversation = 8

// execKillGrace is how long op=kill waits for SIGTERM before escalating to
// the platform kill (SIGKILL on Unix). On Windows both steps are Kill.
const execKillGrace = 3 * time.Second

// execLogDirName is the nusatemp subdirectory holding live background logs.
// The overflow sweeper cleans aged files inside it but never removes the
// directory itself, so a quiet long-running process keeps its log path.
const execLogDirName = "terminal"

// bgExec is one managed detached process.
type bgExec struct {
	id      string
	convID  string
	command string
	cmd     *exec.Cmd
	logFile *os.File
	logPath string
	buf     *tailBuffer
	timeout time.Duration
	started time.Time
	done    chan struct{} // closed once Wait bookkeeping is recorded
	exited  atomic.Bool

	mu         sync.Mutex
	pid        int
	exitCode   int
	waitErr    error
	killed     bool
	timedOut   bool
	finishedAt time.Time
}

// execRegistry tracks live bgExec entries by id.
type execRegistry struct {
	mu     sync.Mutex
	procs  map[string]*bgExec
	limit  int
	closed bool
}

func newExecRegistry() *execRegistry {
	return &execRegistry{procs: map[string]*bgExec{}, limit: execMaxBackgroundPerConversation}
}

// runningLocked reports liveness; safe under r.mu because exited is atomic.
func (p *bgExec) runningLocked() bool { return !p.exited.Load() }

// add reserves a per-conversation slot and registers the (not yet started)
// process. Budget check and insertion are one atomic section so parallel
// spawns cannot overrun the limit. The caller generates the id up front so
// the log file name matches the returned exec_id.
func (r *execRegistry) add(id, convID, command string, cmd *exec.Cmd, logFile *os.File, logPath string, buf *tailBuffer, timeout time.Duration) (*bgExec, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fmt.Errorf("toolbox is shutting down")
	}
	running := 0
	for _, q := range r.procs {
		if q.convID == convID && q.runningLocked() {
			running++
		}
	}
	if running >= r.limit {
		return nil, fmt.Errorf("background limit reached: %d running (max %d per conversation); kill or wait for one to finish first", running, r.limit)
	}
	p := &bgExec{
		id:      id,
		convID:  convID,
		command: command,
		cmd:     cmd,
		logFile: logFile,
		logPath: logPath,
		buf:     buf,
		timeout: timeout,
		started: clock.NewTime().Time(),
		done:    make(chan struct{}),
	}
	r.procs[p.id] = p
	return p, nil
}

// runningCount reports live processes owned by one conversation.
func (r *execRegistry) runningCount(convID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, q := range r.procs {
		if q.convID == convID && q.runningLocked() {
			n++
		}
	}
	return n
}

// remove drops a reservation after a failed cmd.Start.
func (r *execRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.procs, id)
	r.mu.Unlock()
}

// get returns the process by id regardless of owning conversation; the
// caller enforces conversation scoping.
func (r *execRegistry) get(id string) (*bgExec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.procs[id]
	return p, ok
}

func (r *execRegistry) list(convID string) []*bgExec {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*bgExec, 0, len(r.procs))
	for _, p := range r.procs {
		if p.convID == convID {
			out = append(out, p)
		}
	}
	return out
}

// close terminates every managed process. Waits are bounded: a detached
// grandchild holding the stdout pipe can keep cmd.Wait blocked after the
// direct child dies, and shutdown must not stall on bookkeeping.
func (r *execRegistry) close() {
	r.mu.Lock()
	r.closed = true
	procs := make([]*bgExec, 0, len(r.procs))
	for _, p := range r.procs {
		procs = append(procs, p)
	}
	r.mu.Unlock()
	for _, p := range procs {
		killProcessTree(p.cmd)
	}
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Add(1)
		go func(p *bgExec) {
			defer wg.Done()
			select {
			case <-p.done:
			case <-time.After(2 * time.Second):
			}
		}(p)
	}
	wg.Wait()
}

// waitLoop owns the process lifetime bookkeeping: it enforces the optional
// hard timeout, records the exit, closes the log file, then signals done.
func (p *bgExec) waitLoop() {
	if p.timeout > 0 {
		timer := time.AfterFunc(p.timeout, func() {
			p.mu.Lock()
			p.timedOut = true
			p.mu.Unlock()
			killProcessTree(p.cmd)
		})
		defer timer.Stop()
	}
	waitErr := p.cmd.Wait()
	exitCode := -1
	var ee *exec.ExitError
	switch {
	case waitErr == nil:
		exitCode = 0
	case errors.As(waitErr, &ee):
		exitCode = ee.ExitCode()
	}
	_ = p.logFile.Sync()
	_ = p.logFile.Close()
	p.mu.Lock()
	p.waitErr = waitErr
	p.exitCode = exitCode
	p.finishedAt = clock.NewTime().Time()
	p.mu.Unlock()
	p.exited.Store(true)
	close(p.done)
}

// markStarted records the child pid once cmd.Start succeeded.
func (p *bgExec) markStarted() {
	p.mu.Lock()
	p.pid = p.cmd.Process.Pid
	p.mu.Unlock()
}

// terminate asks the tree to exit (SIGTERM on Unix) and escalates to the
// platform kill after the grace window. Idempotent.
func (p *bgExec) terminate() {
	if p.exited.Load() {
		return
	}
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	termProcessTree(p.cmd)
	select {
	case <-p.done:
		return
	case <-time.After(execKillGrace):
	}
	killProcessTree(p.cmd)
	// The tree is dead either way; a detached grandchild that kept the
	// stdout pipe can keep cmd.Wait blocked, so don't park the call on
	// bookkeeping forever.
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
	}
}

// statusLocked reports running|exited|killed|timeout. Callers hold p.mu.
func (p *bgExec) statusLocked() string {
	switch {
	case !p.exited.Load():
		return "running"
	case p.timedOut:
		return "timeout"
	case p.killed:
		return "killed"
	default:
		return "exited"
	}
}

// meta builds the YAML header shared by the status/wait/kill results.
func (p *bgExec) meta() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	end := p.finishedAt
	if end.IsZero() {
		end = clock.NewTime().Time()
	}
	meta := map[string]any{
		"exec_id":     p.id,
		"status":      p.statusLocked(),
		"pid":         p.pid,
		"command":     p.command,
		"duration_ms": end.Sub(p.started).Milliseconds(),
		"log_path":    p.logPath,
	}
	if p.exited.Load() {
		meta["exit_code"] = p.exitCode
	}
	return meta
}

// createExecLogFile opens the live log for one background process under
// nusatemp/terminal/.
func createExecLogFile(id string) (*os.File, string, error) {
	dir := filepath.Join(nusatemp.Path(), execLogDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, id+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

// validExecID rejects malformed or foreign ids before map lookup.
func validExecID(id string) bool {
	if !strings.HasPrefix(id, domain.IDPrefixExec+"_") {
		return false
	}
	for _, r := range id[len(domain.IDPrefixExec)+1:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
