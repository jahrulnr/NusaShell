package toolexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

const (
	defaultExecTimeout = 120 * time.Second
	// maxExecTimeout is the hard ceiling on one command. Measured from the
	// exec tool-output store on 25 Nov 2025 (4,905 calls): p50 5.4s, p90
	// 53.4s, p99 120s, max 300s — only two runs ever passed 240s, and the old
	// 600s bound never saved a legitimate run: the single call that reached it
	// was a hung harness. It IS dservice.ToolCallDeadline — the one ceiling
	// every blocking tool call runs under: the exec clamp cannot be looser
	// than the seam above it, and the store's own numbers say nothing
	// legitimate ever needed the room.
	maxExecTimeout    = service.ToolCallDeadline
	maxExecSpillBytes = 32 << 20 // cap spill file growth
	// waitDelay caps how long Wait may block on output pipes a stray
	// grandchild inherited — without it a detached process stalls the
	// tool forever even after its shell is dead.
	waitDelay = 5 * time.Second
)

// longRunHintAfter is how long a command may take before the result says that
// exec_session is the tool for runs like it. A variable so a test can prove
// the branch without sleeping twenty seconds.
var longRunHintAfter = 20 * time.Second

// longRunHint is the line a finished but slow run carries: the tool cannot know
// a command's duration before it runs, so it reports once it knows.
func longRunHint(elapsed time.Duration) string {
	return fmt.Sprintf("ran %.0fs; use exec_session and poll for runs like this", elapsed.Seconds())
}

// ExecTool runs a shell command and returns its merged output. It is
// inherently unsandboxed — there is no root to confine it to — so the
// schema's ToolPermissionAgent is the gate the application turn
// consults before invoking it.
type ExecTool struct {
	schema  valueobject.ToolSchema
	outputs drepo.ToolOutput
	runner  service.CommandRunner
}

func NewExecTool(outputs drepo.ToolOutput, runner service.CommandRunner) (service.Tool, error) {
	if outputs == nil {
		return nil, errors.New("tool output repository is required")
	}
	if runner == nil {
		return nil, errors.New("command runner is required")
	}
	schema, err := valueobject.NewToolSchema(
		"exec",
		"Run a shell command and return its combined stdout and stderr.",
		json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"},"stdin":{"type":"string","description":"Optional input piped to the command's stdin — for programs that read scripted answers (e.g. sudo -S). One-shot, not an interactive session"},"timeout_ms":{"type":"integer","description":"Timeout in milliseconds — default 120000, capped at 240000"},"allow_broad_kill":{"type":"boolean","description":"Allow a command that stops every matching process (pkill, killall, fuser -k, kill -9 of a pattern). Off by default: it can kill other threads' work, an agent's process, or this server. kill -9 -1 stays refused either way"}},"required":["command"]}`),
		true,
		enum.ToolPermissionAgent,
	)
	if err != nil {
		return nil, err
	}
	return &ExecTool{schema: schema, outputs: outputs, runner: runner}, nil
}

func (tool *ExecTool) Schema() valueobject.ToolSchema {
	return tool.schema
}

func (tool *ExecTool) Execute(ctx context.Context, call entity.ToolCall, emit service.ToolEmitter) (service.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return service.ToolResult{}, err
	}
	convID := service.ConversationID(ctx)
	if err := valueobject.ValidID(convID); err != nil {
		return service.ToolResult{}, fmt.Errorf("exec conversation id: %w", err)
	}
	var input struct {
		Command   string `json:"command"`
		Stdin     string `json:"stdin"`
		TimeoutMS int    `json:"timeout_ms"`
		// AllowBroadKill is the deliberate machine-wide stop: the model has to
		// say so, and the refusal that teaches `pgrep -af` comes first.
		AllowBroadKill bool `json:"allow_broad_kill"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &input); err != nil {
		return service.ToolResult{}, fmt.Errorf("invalid exec input: %w", err)
	}
	if strings.TrimSpace(input.Command) == "" {
		return service.ToolResult{}, errors.New("command is required")
	}
	// A pattern-kill reaches processes this call does not own; the refusal
	// names the safe path so the model can act on it.
	if err := killRefusal(input.Command, input.AllowBroadKill); err != nil {
		return service.ToolResult{}, err
	}

	timeout := defaultExecTimeout
	if input.TimeoutMS > 0 {
		// Clamp in milliseconds first — a huge timeout_ms converted
		// straight to a Duration would overflow int64 into a negative
		// and kill the command instantly.
		timeout = time.Duration(min(input.TimeoutMS, int(maxExecTimeout.Milliseconds()))) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	budget := call.OutputBudget()
	// Reserve before starting the process, but create the file only on spill.
	spill, err := tool.outputs.Reserve(ctx, convID, call.ID())
	if err != nil {
		return service.ToolResult{}, err
	}
	spillPath := spill.Reference()

	sink := &outputSink{emit: emit, budget: budget, spill: spill}
	// The turn's workspace is the shell's working directory. The runner
	// chooses where the command actually executes; output still flows into
	// one sink regardless of backend. stdin is one-shot input, nil when
	// the call asked for none.
	var stdin io.Reader
	if input.Stdin != "" {
		stdin = strings.NewReader(input.Stdin)
	}
	// Measured across the whole run — process start through Wait, the
	// pipes' WaitDelay included — since that is the wait the reader feels.
	start := time.Now()
	exit, err := tool.runner.Run(ctx, input.Command, service.WorkspaceDir(ctx), stdin, sink)
	elapsed := time.Since(start)
	if err != nil {
		if ctx.Err() == nil {
			return service.ToolResult{}, fmt.Errorf("exec: %w", err)
		}
		// The run died because it was stopped, not because the runner broke:
		// what it captured is still the answer, and the suffix names who
		// stopped it. A tool result the turn can persist beats an error that
		// throws the captured head away.
		exit = service.ProcessExit{Err: err}
	}

	sink.mu.Lock()
	head := sink.buf.String()
	tail := string(sink.tail)
	total := sink.total
	spilled := sink.spilled
	spillErr := sink.spillErr
	// Exit status is data the model must see — a nonzero exit is not a
	// Go error. It belongs at the end of the stream, so it lands inside
	// the tail of the excerpt and at the end of the spill file, and it
	// always carries how long the command took: a 40-second run and a
	// 40-millisecond one otherwise read alike.
	// The status describes the process, not the caller's ctx. A clean
	// exit that races a canceled deadline is still a clean exit; a kill
	// arrives as Signal or Err from the runner.
	clean := exit.Code == 0 && exit.Signal == "" && exit.Err == nil
	status := "exit 0"
	switch {
	case exit.Code > 0:
		status = fmt.Sprintf("exit %d", exit.Code)
	case exit.Signal != "":
		status = exit.Signal
	case exit.Err != nil:
		status = fmt.Sprintf("%v", exit.Err)
	}
	if clean && total == 0 {
		// Say the command ran: a status-only result reads like nothing
		// happened.
		status = "no output, exit 0"
	}
	// The status line closes the captured output; the separator is added
	// only when the command did not end its own line, so a normal result
	// reads "done\n(exit 0, 0.1s)" rather than leaving a blank line.
	lastByte := byte(0)
	if spilled && len(tail) > 0 {
		lastByte = tail[len(tail)-1]
	} else if len(head) > 0 {
		lastByte = head[len(head)-1]
	}
	line := fmt.Sprintf("(%s, %.1fs)", status, elapsed.Seconds())
	// A long run is a fact, not a guess: neither the model nor the harness can
	// know a command's duration before it runs, so the tool does not route on
	// it — it reports once it knows. The hint names the tool that would have
	// been the better call, and never appears for a run this tool itself
	// stopped (that line already explains the stop). It sits ABOVE the status
	// line: the status line closes the result, and the clients read the last
	// line to decide how the call ended.
	longRun := ""
	if elapsed >= longRunHintAfter && !(exit.Code == 0 && !clean) {
		longRun = longRunHint(elapsed) + "\n"
	}
	// A process this tool stopped on purpose says who stopped it and how long
	// it ran first: a bare "(signal: killed)" cannot tell the owner's Stop
	// from an agent's own cancel or a server restart, and a timeout that reads
	// like a foreign SIGKILL sends the model hunting for the wrong cause.
	// A kill from outside — no cause on the ctx — keeps the plain signal line.
	if exit.Code == 0 && !clean {
		cause := context.Cause(ctx)
		switch {
		case errors.Is(cause, context.DeadlineExceeded) && elapsed >= timeout:
			// The cap that fired must be ours: a caller's shorter deadline
			// ends the run earlier, and naming our 120s there would be a lie.
			line = fmt.Sprintf("(timed out after %s — partial output above; rerun with exec_session if it needs longer)", waitText(timeout))
		default:
			if stopped := stopLine(cause, elapsed); stopped != "" {
				line = stopped
			}
		}
	}
	suffix := longRun + line
	if total > 0 && lastByte != '\n' {
		suffix = "\n" + suffix
	}
	if sink.writer != nil {
		sink.writeSpill([]byte(suffix))
		_ = sink.writer.Close()
	}
	spillWritten := sink.spillWritten
	sink.mu.Unlock()

	if !spilled {
		full := head + suffix
		if len(full) <= budget {
			return service.ToolResult{Text: full}, nil
		}
		// Never crossed the budget mid-run but overshot after the exit
		// suffix (or on a final chunk boundary) — spill the whole string
		// now; it is still fully in memory.
		ref := entity.SpillRef(spillPath, 0)
		toWrite := full
		if len(toWrite) > maxExecSpillBytes {
			toWrite = toWrite[:maxExecSpillBytes]
			ref = entity.SpillRef(spillPath, maxExecSpillBytes>>20)
		}
		f, err := spill.Open()
		if err == nil {
			var written int
			written, err = f.Write([]byte(toWrite))
			if err == nil && written != len(toWrite) {
				err = io.ErrShortWrite
			}
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			ref = fmt.Sprintf("spill to %s failed: %v", spillPath, err)
		}
		return service.ToolResult{Text: call.OutputWithMiddleTruncateWindow(full, full, len(full), ref)}, nil
	}

	// Spilled mid-run: the file holds the stream up to the spill cap, and
	// the excerpt is assembled from the frozen head and the rolling tail.
	tail += suffix
	ref := entity.SpillRef(spillPath, 0)
	switch {
	case spillErr != nil:
		ref = fmt.Sprintf("spill to %s failed: %v", spillPath, spillErr)
	case spillWritten < total+len(suffix):
		// The cap dropped stream bytes — "full" would be a lie.
		ref = entity.SpillRef(spillPath, maxExecSpillBytes>>20)
	}
	return service.ToolResult{Text: call.OutputWithMiddleTruncateWindow(head, tail, total+len(suffix), ref)}, nil
}

// outputSink is the single writer behind cmd.Stdout+Stderr: one pipe
// merges both streams in arrival order, and every Write forwards a live
// preview chunk to emit. Memory stays bounded by the call budget: while
// output fits it buffers outright; the first Write past the budget opens
// the spill file, freezes buf as the head region, and keeps only a
// rolling tail window — the stream continues into the file up to
// maxExecSpillBytes.
type outputSink struct {
	mu           sync.Mutex
	buf          bytes.Buffer // head region — frozen once spilled
	tail         []byte       // rolling tail once spilled
	writer       io.WriteCloser
	spill        drepo.ToolOutputSpill
	spillErr     error
	spilled      bool
	spillWritten int
	total        int // everything ever written, for the omitted-byte count
	budget       int
	emit         service.ToolEmitter
}

func (s *outputSink) writeSpill(p []byte) {
	if s.writer == nil || s.spillErr != nil || len(p) == 0 || s.spillWritten >= maxExecSpillBytes {
		return
	}
	n := len(p)
	if s.spillWritten+n > maxExecSpillBytes {
		n = maxExecSpillBytes - s.spillWritten
	}
	if n > 0 {
		// Count only bytes actually written and latch the first failure —
		// the guard above then stops further writes and the marker
		// reports "failed" instead of claiming "full output".
		written, err := s.writer.Write(p[:n])
		s.spillWritten += written
		if err == nil && written < n {
			err = io.ErrShortWrite
		}
		s.spillErr = err
	}
}

func (s *outputSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.total += len(p)
	if s.spilled {
		if s.writer != nil {
			s.writeSpill(p)
		}
		s.tail = append(s.tail, p...)
		if len(s.tail) > s.budget {
			s.tail = s.tail[len(s.tail)-s.budget:]
		}
	} else {
		s.buf.Write(p)
		if s.buf.Len() > s.budget {
			s.spilled = true
			if f, err := s.spill.Open(); err == nil {
				s.writer = f
				s.writeSpill(s.buf.Bytes())
			} else {
				// Degrade to head+tail without the file — the marker
				// reports the failure; memory still stays bounded.
				s.spillErr = err
			}
			s.tail = append([]byte(nil), s.buf.Bytes()[max(0, s.buf.Len()-s.budget):]...)
			s.buf.Truncate(min(s.budget, s.buf.Len()))
		}
	}
	s.mu.Unlock()
	// Emit outside the lock: the emitter is synchronous, and a slow
	// consumer must not serialize the two streams against each other.
	if s.emit != nil {
		s.emit(string(p))
	}
	return len(p), nil
}
