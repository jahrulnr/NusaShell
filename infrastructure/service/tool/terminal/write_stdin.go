package toolexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

const (
	defaultWriteYield = 250 * time.Millisecond
	minWriteYield     = 250 * time.Millisecond
	maxWriteYield     = 30 * time.Second
	// Empty writes are polls: they wait longer by default and may park
	// longer — a tailing process rewards patience; the cap keeps a single
	// call from monopolizing the turn. A session that never exits is waited
	// for through polls, so the cap also bounds the aggregate: at 300s per
	// poll one parked call could hold a turn for the whole exec ceiling,
	// while 60s still lets a slow process finish inside a few polls.
	defaultPollYield = 5 * time.Second
	minPollYield     = 5 * time.Second
	maxPollYield     = 60 * time.Second
	// postWriteSettle gives a tty child a beat to react after a write so
	// its output lands inside this call's window — codex pauses 100 ms
	// for the same reason.
	postWriteSettle = 100 * time.Millisecond
)

// WriteStdinTool feeds an open session and polls its output — an empty
// chars is a pure poll, so there is no separate read verb. A single
// Ctrl-C byte is an interrupt, not literal input — the mapping lives in
// the session's Interrupt, which injects the byte on a pty and signals
// the process group on pipes. Whole calls run inside the store's Interact
// so concurrent calls on one session serialize and a reaped id fails
// closed instead of landing on a reused session.
type WriteStdinTool struct {
	schema valueobject.ToolSchema
	store  dservice.TerminalSessions

	mu sync.Mutex
	// pending is the partial line typed into each shell session since its last
	// newline. A caller can split `pkill chrome` and the newline that runs it
	// across two calls, so the guard must judge the whole line when the
	// newline arrives — not only the text of that one write.
	pending map[string]pendingLine
}

// pendingLine is one session's unexecuted text. truncated marks a line too long
// to keep whole: the guard cannot judge what it no longer has, so the line is
// refused when its newline arrives rather than checked against a fragment.
type pendingLine struct {
	text      string
	truncated bool
}

// pendingCap bounds what one session may hold unexecuted: a line longer than
// this is not a command anybody meant, and the guard keeps the tail (the part
// a kill would sit in) rather than growing without limit.
const pendingCap = 8 << 10

// pendingKey names one session's pending line.
func pendingKey(convID string, id int) string {
	return convID + "\x00" + strconv.Itoa(id)
}

// takePending consumes a session's pending line.
func (tool *WriteStdinTool) takePending(key string) pendingLine {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	line := tool.pending[key]
	delete(tool.pending, key)
	return line
}

// keepPending stores the unexecuted tail of a line. A tail past the cap cannot
// be kept whole, and a fragment is worse than nothing: it is recorded as
// truncated, and the newline that would run it is refused.
func (tool *WriteStdinTool) keepPending(key, tail string, truncated bool) {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	if tool.pending == nil || len(tool.pending) > 256 {
		// A fresh map when it grows past what a room's sessions can own: the
		// guard loses a stale prefix, never memory.
		tool.pending = map[string]pendingLine{}
	}
	if tail == "" && !truncated {
		delete(tool.pending, key)
		return
	}
	if len(tail) > pendingCap {
		tail = ""
		truncated = true
	}
	tool.pending[key] = pendingLine{text: tail, truncated: truncated}
}

// forgetPending drops a session's pending line when the session is gone.
func (tool *WriteStdinTool) forgetPending(key string) {
	tool.mu.Lock()
	delete(tool.pending, key)
	tool.mu.Unlock()
}

func NewWriteStdinTool(store dservice.TerminalSessions) (dservice.Tool, error) {
	if store == nil {
		return nil, errors.New("terminal session store is required")
	}
	schema, err := valueobject.NewToolSchema(
		"write_stdin",
		"Write characters to an existing terminal session from exec_session and return its recent output. Writing requires the session's tty backend — pipes sessions have closed stdin and accept only the interrupt. An empty chars polls without writing. A single \\u0003 sends Ctrl-C (SIGINT) instead of literal input.",
		json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"integer","description":"Session id returned by exec_session"},"chars":{"type":"string","description":"Characters to write to the session's stdin — empty polls without writing; \"\\u0003\" alone sends Ctrl-C. Only tty sessions have open stdin"},"yield_time_ms":{"type":"integer","description":"Milliseconds to wait for output — writes default 250, range 250-30000; empty polls default 5000, range 5000-60000"},"allow_broad_kill":{"type":"boolean","description":"Allow a typed shell command that stops every matching process (pkill, killall, fuser -k, kill -9 of a pattern). Off by default: it can kill other threads' work, an agent's process, or this server"}},"required":["session_id"]}`),
		true,
		enum.ToolPermissionAgent,
	)
	if err != nil {
		return nil, err
	}
	return &WriteStdinTool{schema: schema, store: store}, nil
}

func (tool *WriteStdinTool) Schema() valueobject.ToolSchema {
	return tool.schema
}

func (tool *WriteStdinTool) Execute(ctx context.Context, call entity.ToolCall, emit dservice.ToolEmitter) (dservice.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return dservice.ToolResult{}, err
	}
	convID := dservice.ConversationID(ctx)
	if err := valueobject.ValidID(convID); err != nil {
		return dservice.ToolResult{}, fmt.Errorf("write_stdin conversation id: %w", err)
	}
	var input struct {
		SessionID   int    `json:"session_id"`
		Chars       string `json:"chars"`
		YieldTimeMS int    `json:"yield_time_ms"`
		// AllowBroadKill is the deliberate machine-wide stop, same hatch as
		// exec: a shell session is guarded, a REPL or editor never is.
		AllowBroadKill bool `json:"allow_broad_kill"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &input); err != nil {
		return dservice.ToolResult{}, fmt.Errorf("invalid write_stdin input: %w", err)
	}

	var text string
	err := tool.store.Interact(ctx, convID, input.SessionID, func(sess dservice.TerminalSession) error {
		// A session guard-rail, not a sandbox: text typed into a shell is a
		// command, so a pattern-kill in it is refused the same way exec
		// refuses one. Text typed into a REPL or an editor is input — typing
		// "pkill" into vim must stay possible — and a line without a newline
		// is a partial keystroke, not an execution. The exception follows the
		// command the session was opened with, not whatever runs in its
		// foreground: a vim inside a bash session still reads as a shell, and
		// that trade is deliberate — finding the foreground process is a
		// different feature. An agent that means harm can still write a script
		// and run it; what this stops is the shortcut nobody weighed.
		key := pendingKey(convID, input.SessionID)
		if input.Chars != "" && isShellSession(sess.Command()) {
			switch {
			case strings.Contains(input.Chars, "\n"):
				// The whole line, including what earlier calls typed, is what
				// runs now — so that is what the guard judges. A caller that
				// splits `pkill` and the newline across two calls does not get
				// a free pass.
				held := tool.takePending(key)
				if held.truncated {
					return fmt.Errorf("the typed line is longer than the guard can verify (%d KiB); the newline was not sent — write the command to a file and run the file, or split it into shorter lines", pendingCap>>10)
				}
				line := held.text + input.Chars
				// allow_broad_kill buys a deliberate machine-wide stop, never
				// `kill -9 -1` — killRefusal keeps that one absolute.
				if err := killRefusal(line, input.AllowBroadKill); err != nil {
					return err
				}
				tail := line[strings.LastIndex(line, "\n")+1:]
				tool.keepPending(key, tail[:min(len(tail), pendingCap)], len(tail) > pendingCap)
			default:
				// Nothing executed yet: remember the prefix, judge nothing.
				held := tool.takePending(key)
				combined := held.text + input.Chars
				truncated := held.truncated || len(combined) > pendingCap
				if len(combined) > pendingCap {
					combined = combined[len(combined)-pendingCap:]
				}
				tool.keepPending(key, combined, truncated)
			}
		}
		var yield time.Duration
		if input.Chars != "" {
			if input.Chars == "\x03" {
				if err := sess.Interrupt(); err != nil {
					// Codex swallows a write racing with process exit and
					// still collects the final output — an interrupt that
					// lands on a just-dead session is not an error.
					if exited, _ := sess.Exited(); !exited {
						return err
					}
				}
			} else if err := sess.Write([]byte(input.Chars)); err != nil {
				exited, _ := sess.Exited()
				if errors.Is(err, dservice.ErrTerminalStdinClosed) && !exited {
					// Codex's StdinClosed wording — the model learns
					// the remedy, not just the failure. A session that
					// has already exited is not a closed-stdin problem.
					return errors.New("stdin is closed for this session; rerun exec_session with tty=true to keep stdin open")
				}
				if !exited {
					return err
				}
			} else {
				// Let the child react before polling — its response
				// then lands in this window instead of the next poll.
				timer := time.NewTimer(postWriteSettle)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
			yield = clampYield(input.YieldTimeMS, defaultWriteYield, minWriteYield, maxWriteYield)
		} else {
			// Poll semantics: the floor is the codex default so a caller
			// asking for a short wait still gets a meaningful window.
			yield = clampYield(input.YieldTimeMS, defaultPollYield, minPollYield, maxPollYield)
		}

		start := time.Now()
		poll := sess.Collect(ctx, yield, func(chunk string) {
			if emit != nil {
				emit(chunk)
			}
		})
		text = formatPoll(sess.ID(), poll, start, call.OutputWithMiddleTruncateWindow(poll.Output, poll.Output, len(poll.Output), ""))
		// An empty poll is a fact, not an empty answer: the session is alive
		// but emitted nothing in the window, and naming that window lets the
		// model decide to poll again or write to the process. Bytes that did
		// arrive — even ones the buffer cap dropped — keep the line out, and
		// so does a canceled wait: a window cut short is not a quiet process.
		if input.Chars == "" && poll.TotalBytes == 0 && ctx.Err() == nil {
			text += fmt.Sprintf("\nno new output for %s", waitText(yield))
		}
		if ctx.Err() != nil {
			// The turn was stopped while the session kept running. What this
			// call drained is still the answer, and the line names who stopped
			// it — the session survives, so the model can poll it again.
			if stopped := stopLine(context.Cause(ctx), time.Since(start)); stopped != "" {
				text += "\n" + stopped
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, dservice.ErrTerminalSessionGone):
		tool.forgetPending(pendingKey(convID, input.SessionID))
		return dservice.ToolResult{}, fmt.Errorf("session %d not found — it may have exited or been reaped", input.SessionID)
	case err != nil:
		return dservice.ToolResult{}, err
	}
	return dservice.ToolResult{Text: text}, nil
}

// isShellSession reports whether a session was opened with a shell — the only
// sessions whose typed lines are commands.
func isShellSession(command string) bool {
	name, _ := commandWord(command)
	return isShell(name)
}
