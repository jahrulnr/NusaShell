//go:build linux

package toolexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// pollStore hands the tool one scripted session and records the window each
// Collect asks for — the cap is the point of the test, and a real 60-second
// poll would make the suite a minute longer for it.
type pollStore struct {
	poll  dservice.TerminalPoll
	waits []time.Duration
	// block holds Collect until the ctx dies, so a canceled turn can be
	// reproduced without a real process.
	block bool
	// command is what the scripted session was opened with.
	command string
}

func (s *pollStore) Open(context.Context, string, dservice.TerminalOpenRequest) (dservice.TerminalSession, error) {
	return nil, dservice.ErrTerminalSessionGone
}

func (s *pollStore) Interact(_ context.Context, _ string, _ int, fn func(dservice.TerminalSession) error) error {
	return fn(&pollSession{store: s})
}

func (s *pollStore) Terminate(string, int) bool   { return false }
func (s *pollStore) TerminateConversation(string) {}
func (s *pollStore) TerminateAll()                {}

type pollSession struct{ store *pollStore }

func (s *pollSession) ID() int             { return 7 }
func (s *pollSession) Command() string     { return s.store.command }
func (s *pollSession) TTY() bool           { return true }
func (s *pollSession) Write([]byte) error  { return nil }
func (s *pollSession) Interrupt() error    { return nil }
func (s *pollSession) Terminate()          {}
func (s *pollSession) Exited() (bool, int) { return !s.store.poll.Running, s.store.poll.ExitCode }

func (s *pollSession) Collect(ctx context.Context, maxWait time.Duration, _ func(string)) dservice.TerminalPoll {
	s.store.waits = append(s.store.waits, maxWait)
	if s.store.block {
		<-ctx.Done()
	}
	return s.store.poll
}

// An empty poll parks at most a minute: a session that never exits is waited
// for through polls, so the window bounds the whole call, not just one turn
// of it. A window that drained nothing says so — with the wait the model
// actually sat in, not the number it asked for.
func TestWriteStdinPollCapAndEmptyWindow(t *testing.T) {
	store := &pollStore{poll: dservice.TerminalPoll{Running: true}}
	tool, err := NewWriteStdinTool(store)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(testContext(),
		mustCall(t, "poll-1", `{"session_id":7,"chars":"","yield_time_ms":300000}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.waits) != 1 || store.waits[0] != maxPollYield {
		t.Fatalf("poll windows = %v, want %v", store.waits, maxPollYield)
	}
	if !strings.Contains(res.Text, "no new output for 60s") {
		t.Fatalf("empty poll = %q, want the silent-window line", res.Text)
	}

	// The default window is unchanged, and it is the one named.
	store.waits = nil
	res, err = tool.Execute(testContext(), mustCall(t, "poll-2", `{"session_id":7,"chars":""}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.waits) != 1 || store.waits[0] != defaultPollYield {
		t.Fatalf("default poll window = %v, want %v", store.waits, defaultPollYield)
	}
	if !strings.Contains(res.Text, "no new output for 5s") {
		t.Fatalf("default empty poll = %q, want the silent-window line", res.Text)
	}
}

// A window that produced output never claims silence, and a write's window
// is not a poll at all.
func TestWriteStdinKeepsQuietAboutOutput(t *testing.T) {
	store := &pollStore{poll: dservice.TerminalPoll{Output: "partial line\n", Running: true, TotalBytes: 13}}
	tool, err := NewWriteStdinTool(store)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(testContext(), mustCall(t, "poll-3", `{"session_id":7,"chars":""}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "partial line") || strings.Contains(res.Text, "no new output") {
		t.Fatalf("poll with output = %q, want the output and no silence line", res.Text)
	}

	// A write that drew no answer is not a silent poll: the model asked for
	// something, and this window just did not see the reply yet.
	store.poll = dservice.TerminalPoll{Running: true}
	res, err = tool.Execute(testContext(), mustCall(t, "poll-4", `{"session_id":7,"chars":"ls\n"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "no new output") {
		t.Fatalf("write window = %q, want no silence line", res.Text)
	}
}

// A stopped turn keeps what the poll drained and names the stop in the same
// words a stopped command uses — the session itself survives, so the model
// can poll it again once the turn is resumed.
func TestWriteStdinCancelKeepsTheDrainedOutput(t *testing.T) {
	store := &pollStore{block: true, poll: dservice.TerminalPoll{Output: "line one\n", Running: true, TotalBytes: 9}}
	tool, err := NewWriteStdinTool(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(dservice.WithConversationID(context.Background(), "c1"))
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel(dservice.ErrInterruptedByUser)
	}()
	res, err := tool.Execute(ctx, mustCall(t, "poll-5", `{"session_id":7,"chars":""}`, 0), nil)
	if err != nil {
		t.Fatalf("a stopped poll must return its result, not an error: %v", err)
	}
	if !strings.Contains(res.Text, "line one") {
		t.Fatalf("poll result = %q, want the drained output", res.Text)
	}
	if !strings.HasSuffix(res.Text, "s — partial output above)") || !strings.Contains(res.Text, "(interrupted by user after ") {
		t.Fatalf("poll result = %q, want the interrupt line", res.Text)
	}
	if strings.Contains(res.Text, "no new output") {
		t.Fatalf("poll result = %q, want no silence line for a window that drained bytes", res.Text)
	}
}

// Text typed into a shell is a command, so a pattern-kill in it is refused —
// but only there, and only when it is a whole line: a REPL or editor receives
// input, and a partial keystroke has not run anything yet.
func TestWriteStdinGuardsShellCommandsOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		chars   string
		allow   bool
		refused bool
	}{
		{"shell, whole line", "bash", "pkill -f chrome\n", false, true},
		{"shell, kill -9 -1", "sh", "kill -9 -1\n", true, true},
		{"shell, typed partial line", "bash", "pkill -f chrome", false, false},
		{"shell, safe command", "bash", "ls -la\n", false, false},
		{"shell, escape hatch", "bash", "pkill -f chrome\n", true, false},
		{"repl, same text", "python3", "pkill -f chrome\n", false, false},
		{"editor, same text", "vim notes.md", "pkill -f chrome\n", false, false},
		{"shell, multi-line with kill", "zsh", "ls\nkillall node\n", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &pollStore{poll: dservice.TerminalPoll{Running: true}, command: tc.command}
			tool, err := NewWriteStdinTool(store)
			if err != nil {
				t.Fatal(err)
			}
			input := `{"session_id":7,"chars":` + quoteJSON(tc.chars) + `}`
			if tc.allow {
				input = `{"session_id":7,"allow_broad_kill":true,"chars":` + quoteJSON(tc.chars) + `}`
			}
			_, err = tool.Execute(testContext(), mustCall(t, "guard", input, 0), nil)
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "pgrep -af") {
					t.Fatalf("typed write = %v, want the teaching refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("typed write refused: %v", err)
			}
			if len(store.waits) != 1 {
				t.Fatalf("the write never reached the session (waits %v)", store.waits)
			}
		})
	}
}

// quoteJSON renders a string as a JSON literal for the call input.
func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Splitting a command across calls must not slip past the guard: the prefix a
// first call typed is judged when the second call supplies the newline.
func TestWriteStdinGuardsALineSplitAcrossCalls(t *testing.T) {
	store := &pollStore{poll: dservice.TerminalPoll{Running: true}, command: "bash"}
	tool, err := NewWriteStdinTool(store)
	if err != nil {
		t.Fatal(err)
	}
	// The prefix alone executes nothing, so it is not refused…
	if _, err := tool.Execute(testContext(), mustCall(t, "split-1", `{"session_id":7,"chars":"pkill -f chrome"}`, 0), nil); err != nil {
		t.Fatalf("partial line refused: %v", err)
	}
	// …but the newline that runs it sees the whole line.
	_, err = tool.Execute(testContext(), mustCall(t, "split-2", `{"session_id":7,"chars":"\n"}`, 0), nil)
	if err == nil || !strings.Contains(err.Error(), "pgrep -af") {
		t.Fatalf("split line = %v, want the teaching refusal", err)
	}

	// A safe split line still runs, and the buffer resets after each newline.
	for i, chars := range []string{"echo hel", "lo\n", "pkill"} {
		if _, err := tool.Execute(testContext(), mustCall(t, "safe-"+string(rune('a'+i)), `{"session_id":7,"chars":`+quoteJSON(chars)+`}`, 0), nil); err != nil {
			t.Fatalf("call %d (%q) refused: %v", i, chars, err)
		}
	}
	// The trailing "pkill" is pending, not refused, until its newline comes.
	if _, err := tool.Execute(testContext(), mustCall(t, "safe-d", `{"session_id":7,"chars":"\n"}`, 0), nil); err == nil {
		t.Fatal("the pending pattern-kill ran on its newline")
	}
}

// A line too long to keep whole cannot be judged, so it is never run: padding
// a pattern-kill past the buffer must not turn it into a fragment the guard
// reads as harmless.
func TestWriteStdinRefusesAPendingLineItCouldNotKeep(t *testing.T) {
	store := &pollStore{poll: dservice.TerminalPoll{Running: true}, command: "bash"}
	tool, err := NewWriteStdinTool(store)
	if err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat(" ", 12<<10)
	if _, err := tool.Execute(testContext(), mustCall(t, "long-1",
		`{"session_id":7,"chars":`+quoteJSON("pkill chrome; "+padding)+`}`, 0), nil); err != nil {
		t.Fatalf("the prefix alone executes nothing and was refused: %v", err)
	}
	_, err = tool.Execute(testContext(), mustCall(t, "long-2", `{"session_id":7,"chars":"\n"}`, 0), nil)
	if err == nil || !strings.Contains(err.Error(), "longer than the guard can verify") {
		t.Fatalf("oversized line = %v, want the refusal", err)
	}
	// The refusal is not sticky: a short, clean line still runs afterwards.
	if _, err := tool.Execute(testContext(), mustCall(t, "long-3", `{"session_id":7,"chars":"echo ok\n"}`, 0), nil); err != nil {
		t.Fatalf("a short line after the refusal was refused: %v", err)
	}
}
