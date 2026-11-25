//go:build linux

package toolexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
)

func newTool(t *testing.T) service.Tool {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool, err := NewExecTool(mustOutputRepo(t, root), NewLocalRunner(nil))
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}
	return tool
}

func mustOutputRepo(t *testing.T, root string) drepo.ToolOutput {
	t.Helper()
	repo, err := repoconversation.NewToolOutputRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func testContext() context.Context {
	return service.WithConversationID(context.Background(), "c1")
}

func mustCall(t *testing.T, id, input string, budget int) entity.ToolCall {
	t.Helper()
	call, err := entity.NewToolCall(id, "exec", input, enum.TooltypeOrigin)
	if err != nil {
		t.Fatalf("NewToolCall: %v", err)
	}
	if budget > 0 {
		call.SetOutputBudget(budget)
	}
	return call
}

type fakeRunner struct {
	command   string
	workspace string
	stdin     io.Reader
	deadline  time.Time
}

func (r *fakeRunner) Run(ctx context.Context, command, workspace string, stdin io.Reader, output io.Writer) (service.ProcessExit, error) {
	r.command, r.workspace, r.stdin = command, workspace, stdin
	r.deadline, _ = ctx.Deadline()
	_, err := output.Write([]byte("remote output"))
	return service.ProcessExit{Code: 7}, err
}

func TestExecUsesInjectedRunner(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	tool, err := NewExecTool(mustOutputRepo(t, root), runner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.WithWorkspaceDir(testContext(), "/remote/work")
	var preview string
	result, err := tool.Execute(ctx, mustCall(t, "remote-1", `{"command":"echo ignored"}`, 0), func(chunk string) { preview += chunk })
	if err != nil {
		t.Fatal(err)
	}
	if runner.command != "echo ignored" || runner.workspace != "/remote/work" {
		t.Fatalf("runner saw %q in %q", runner.command, runner.workspace)
	}
	if preview != "remote output" || !strings.HasPrefix(result.Text, "remote output\n(exit 7, ") || !strings.HasSuffix(result.Text, "s)") {
		t.Fatalf("preview=%q result=%q", preview, result.Text)
	}
}

// ctx.Err() at suffix time is uncorrelated with what the runner did —
// a process reaped cleanly just as the deadline fired reports
// "(killed: context deadline exceeded)", telling the model a finished
// command was killed.
func TestExecCleanExitDoesNotReportKilled(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool, err := NewExecTool(mustOutputRepo(t, root), &reapedAtDeadlineRunner{})
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}
	res, err := tool.Execute(testContext(), mustCall(t, "k-1", `{"command":"fast","timeout_ms":50}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "killed") || strings.Contains(res.Text, "timed out") {
		t.Fatalf("a clean exit rendered as killed or timed out: %q", res.Text)
	}
}

// reapedAtDeadlineRunner reproduces the LocalRunner interleaving the
// hunter named: cmd.Wait() returns nil because the process was already
// reaped when ctx fired — a race the runner resolves as Code:0.
type reapedAtDeadlineRunner struct{}

func (r *reapedAtDeadlineRunner) Run(ctx context.Context, _, _ string, _ io.Reader, output io.Writer) (service.ProcessExit, error) {
	_, _ = output.Write([]byte("done"))
	<-ctx.Done()
	return service.ProcessExit{Code: 0}, nil
}

func TestExecStdinReachesRunner(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	tool, err := NewExecTool(mustOutputRepo(t, root), runner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Execute(testContext(),
		mustCall(t, "stdin-1", `{"command":"cat","stdin":"line1\nline2"}`, 0), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	raw, err := io.ReadAll(runner.stdin)
	if err != nil || string(raw) != "line1\nline2" {
		t.Fatalf("runner stdin = %q, %v", raw, err)
	}
	// No stdin field → nil reader, never an empty pipe.
	_, err = tool.Execute(testContext(), mustCall(t, "stdin-2", `{"command":"cat"}`, 0), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.stdin != nil {
		t.Fatalf("absent stdin must not open a pipe, got %v", runner.stdin)
	}
}

func TestExecStdinPipesToProcess(t *testing.T) {
	res, err := newTool(t).Execute(testContext(),
		mustCall(t, "stdin-3", `{"command":"read a; echo got-$a","stdin":"answer42"}`, 0), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "got-answer42") {
		t.Fatalf("stdin did not reach the shell: %q", res.Text)
	}
}

func TestExecToolSchema(t *testing.T) {
	s := newTool(t).Schema()
	if s.Name() != "exec" || !s.Enabled() ||
		s.Permission() != enum.ToolPermissionAgent || !s.CallableByAgent() {
		t.Fatalf("unexpected schema: %+v", s)
	}
	var params struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(s.Parameters(), &params); err != nil ||
		len(params.Required) != 1 || params.Required[0] != "command" {
		t.Fatalf("command not the required param: %s", s.Parameters())
	}
}

func TestExecEcho(t *testing.T) {
	res, err := newTool(t).Execute(testContext(),
		mustCall(t, "echo-1", `{"command":"echo hello"}`, 0), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Text
	if !strings.Contains(out, "hello") {
		t.Fatalf("output missing echo: %q", out)
	}
}

func TestExecMergesStderrInOrder(t *testing.T) {
	res, err := newTool(t).Execute(testContext(),
		mustCall(t, "merge-1", `{"command":"echo o1; echo e1 >&2; echo o2"}`, 0), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Text
	o1, e1, o2 := strings.Index(out, "o1"), strings.Index(out, "e1"), strings.Index(out, "o2")
	if o1 < 0 || e1 < 0 || o2 < 0 || !(o1 < e1 && e1 < o2) {
		t.Fatalf("stderr not merged in arrival order: %q", out)
	}
}

func TestExecNonzeroExitIsOutput(t *testing.T) {
	res, err := newTool(t).Execute(testContext(),
		mustCall(t, "exit-1", `{"command":"echo partial; exit 3"}`, 0), nil)
	if err != nil {
		t.Fatalf("nonzero exit must not be a Go error: %v", err)
	}
	out := res.Text
	if !strings.Contains(out, "partial") || !strings.Contains(out, "(exit 3, ") {
		t.Fatalf("exit status not reported: %q", out)
	}
}

func TestExecTimeoutKillsTree(t *testing.T) {
	start := time.Now()
	res, err := newTool(t).Execute(testContext(),
		mustCall(t, "kill-1", `{"command":"sleep 30","timeout_ms":200}`, 0), nil)
	if err != nil {
		t.Fatalf("killed process must not be a Go error: %v", err)
	}
	out := res.Text
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("kill took too long: %v", elapsed)
	}
	// A kill of our own says so, with the cap that fired and what to do
	// next — a bare signal leaves the model guessing who killed it, and this
	// line replaces the signal status rather than stacking on it.
	want := "(timed out after 200ms — partial output above; rerun with exec_session if it needs longer)"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("timeout result = %q, want it to end with %q", out, want)
	}
	if strings.Contains(out, "signal:") {
		t.Fatalf("a timeout must not read as a foreign kill: %q", out)
	}
}

// Every result carries how long the command took, in the status line that
// already closes it: the difference between a 40-millisecond and a
// 40-second run is otherwise invisible to the model.
func TestExecStatusLineCarriesDuration(t *testing.T) {
	start := time.Now()
	res, err := newTool(t).Execute(testContext(),
		mustCall(t, "dur-1", `{"command":"echo done"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if !strings.HasPrefix(res.Text, "done\n(exit 0, ") || !strings.HasSuffix(res.Text, "s)") {
		t.Fatalf("status line = %q, want done then (exit 0, <duration>)", res.Text)
	}
	printed := strings.TrimSuffix(strings.TrimPrefix(res.Text[strings.LastIndex(res.Text, "\n(")+1:], "(exit 0, "), "s)")
	got, perr := strconv.ParseFloat(printed, 64)
	if perr != nil {
		t.Fatalf("duration %q is not a number: %v", printed, perr)
	}
	if got > elapsed.Seconds()+0.05 {
		t.Fatalf("printed %vss for a run the test measured at %v", got, elapsed)
	}

	// A silent command still says it ran, with the same duration.
	res, err = newTool(t).Execute(testContext(), mustCall(t, "dur-2", `{"command":"sleep 0.3"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Text, "(no output, exit 0, ") || !strings.HasSuffix(res.Text, "s)") {
		t.Fatalf("silent command = %q, want (no output, exit 0, <duration>)", res.Text)
	}
	if strings.HasPrefix(res.Text, "\n") {
		t.Fatalf("a status-only result must not open with a blank line: %q", res.Text)
	}
}

// A caller that went away — a closed tab, a dropped socket — kills the
// process with no actor to name: the neutral line says canceled, never a
// timeout the model would answer by raising a bound nobody reached.
func TestExecCallerGoneIsNotATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(service.WithConversationID(context.Background(), "c1"))
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	res, err := newTool(t).Execute(ctx,
		mustCall(t, "cancel-1", `{"command":"echo started; sleep 30"}`, 0), nil)
	if err != nil {
		t.Fatalf("a killed process must not be a Go error: %v", err)
	}
	// The caller went away; nobody decided it, so the neutral form is used
	// and the head the command produced is still there.
	if !strings.HasPrefix(res.Text, "started\n(canceled after ") || !strings.HasSuffix(res.Text, "— partial output above)") {
		t.Fatalf("cancelerless cancel = %q, want the head and the neutral line", res.Text)
	}
	if strings.Contains(res.Text, "timed out") {
		t.Fatalf("an owner cancel must not read as a timeout: %q", res.Text)
	}
}

func TestExecOversizedSpillsAndTruncates(t *testing.T) {
	const budget = 400
	call := mustCall(t, "oversized-1", `{"command":"seq 1 2000"}`, budget)
	res, err := newTool(t).Execute(testContext(), call, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Text
	if len(out) > budget {
		t.Fatalf("result exceeds budget: %d > %d", len(out), budget)
	}
	_, spill, ok := strings.Cut(out, "full output: ")
	if !ok {
		t.Fatalf("spill marker missing: %q", out)
	}
	spill, _, _ = strings.Cut(spill, " (")
	if !strings.HasPrefix(out, "1\n2\n3") || !strings.Contains(out, "2000\n(exit 0, ") || !strings.HasSuffix(out, "s)") {
		t.Fatalf("missing head or tail: %q (tail %q)", out[:80], out[max(0, len(out)-80):])
	}
	if !strings.Contains(out, "bytes omitted") || !strings.Contains(out, "full output: "+spill) {
		t.Fatalf("marker missing count or spill path: %q", out)
	}
	// The file persists for the session — the marker's path must stay
	// valid for a follow-up read.
	data, err := os.ReadFile(spill)
	if err != nil {
		t.Fatalf("spill file must persist after execute: %v", err)
	}
	if !strings.HasPrefix(string(data), "1\n2\n3") || !strings.Contains(string(data), "2000\n(exit 0, ") {
		t.Fatalf("spill file missing head or tail: %q...%q", data[:20], data[len(data)-24:])
	}
}

func TestExecSpillBelongsToConversationAndSurvivesAge(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool, err := NewExecTool(mustOutputRepo(t, root), NewLocalRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.WithConversationID(context.Background(), "c1")
	result, err := tool.Execute(ctx, mustCall(t, "owned", `{"command":"seq 1 2000"}`, 400), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, path, ok := strings.Cut(result.Text, "full output: ")
	if !ok {
		t.Fatalf("spill marker missing: %q", result.Text)
	}
	path, _, _ = strings.Cut(path, " (")
	if filepath.Dir(path) != filepath.Join(root, "c1", "tool-output") {
		t.Fatalf("spill outside conversation: %s", path)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(ctx, mustCall(t, "later", `{"command":"true"}`, 400), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("conversation-owned spill expired before conversation deletion: %v", err)
	}
}

func TestExecDoesNotCreateMissingConversation(t *testing.T) {
	root := t.TempDir()
	tool, err := NewExecTool(mustOutputRepo(t, root), NewLocalRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Execute(testContext(), mustCall(t, "missing", `{"command":"echo hello"}`, 400), nil)
	if err == nil {
		t.Fatal("exec must not recreate a deleted conversation")
	}
	if _, err := os.Stat(filepath.Join(root, "c1")); !os.IsNotExist(err) {
		t.Fatalf("missing conversation was created: %v", err)
	}
}

func TestExecRejectsInvalidConversationIDBeforeRunning(t *testing.T) {
	root := t.TempDir()
	tool, err := NewExecTool(mustOutputRepo(t, root), NewLocalRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "unexpected-command")
	ctx := service.WithConversationID(context.Background(), "../c2")
	input := fmt.Sprintf(`{"command":%q}`, "touch "+marker)
	_, err = tool.Execute(ctx, mustCall(t, "bad-id", input, 400), nil)
	if err == nil || !strings.Contains(err.Error(), "conversation id") {
		t.Fatalf("invalid conversation id must be rejected: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command ran for invalid conversation id: %v", err)
	}
}

func TestExecRejectsConversationFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "c1"), []byte("not a room"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool, err := NewExecTool(mustOutputRepo(t, root), NewLocalRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Execute(testContext(), mustCall(t, "file-room", `{"command":"echo hello"}`, 400), nil)
	if err == nil || !strings.Contains(err.Error(), "exec conversation c1 is not a directory") {
		t.Fatalf("file must not be accepted as a conversation: %v", err)
	}
}

func TestExecExitSuffixOverflowSpillsWholeOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool, err := NewExecTool(mustOutputRepo(t, root), NewLocalRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	const budget = 400
	command := `printf '%0400d' 0; exit 7`
	res, err := tool.Execute(testContext(), mustCall(t, "suffix-overflow", `{"command":"`+command+`"}`, budget), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Text) > budget || !strings.Contains(res.Text, "bytes omitted") || !strings.HasSuffix(res.Text, "s)") {
		t.Fatalf("suffix-overflow excerpt = %q", res.Text)
	}
	_, path, ok := strings.Cut(res.Text, "full output: ")
	if !ok {
		t.Fatalf("missing spill marker: %q", res.Text)
	}
	path, _, _ = strings.Cut(path, " (")
	if filepath.Dir(path) != filepath.Join(root, "c1", "tool-output") {
		t.Fatalf("spill path outside room: %q", path)
	}
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The padding is exactly the budget; the status line follows it, so the
	// whole result is in the file and ends with the exit line.
	if cut := strings.LastIndex(string(full), "\n(exit 7, "); cut != budget {
		t.Fatalf("spill holds %d bytes before the status line, want %d: %q", cut, budget, full)
	}
	if !strings.HasSuffix(string(full), "s)") {
		t.Fatalf("spill does not end with the status line: %q", full)
	}
}

func TestRepeatedCallIDKeepsDistinctSpillMarkers(t *testing.T) {
	tool := newTool(t)
	paths := make([]string, 0, 2)
	for range 2 {
		result, err := tool.Execute(testContext(),
			mustCall(t, "repeat-id", `{"command":"seq 1 2000"}`, 400), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, path, ok := strings.Cut(result.Text, "full output: ")
		if !ok {
			t.Fatalf("missing spill marker: %q", result.Text)
		}
		path, _, _ = strings.Cut(path, " (")
		paths = append(paths, path)
		t.Cleanup(func() { _ = os.Remove(path) })
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("marker target unavailable: %v", err)
		}
	}
	if paths[0] == paths[1] {
		t.Fatalf("reused call ID overwrote earlier spill: %s", paths[0])
	}
}

func TestSpillCapReportsTruncation(t *testing.T) {
	result, err := newTool(t).Execute(testContext(),
		mustCall(t, "spill-cap", `{"command":"yes x | head -c 33554433"}`, 400), nil)
	if err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("spilled output (truncated at %d MiB): ", maxExecSpillBytes>>20)
	_, rest, ok := strings.Cut(result.Text, marker)
	if !ok {
		t.Fatalf("spill cap marker missing: %q", result.Text)
	}
	path, _, _ := strings.Cut(rest, " (")
	t.Cleanup(func() { _ = os.Remove(path) })
	info, err := os.Stat(path)
	if err != nil || info.Size() != maxExecSpillBytes {
		t.Fatalf("spill cap file = %v, %v", info, err)
	}
}

func TestExecTimeoutMsOverflowClamps(t *testing.T) {
	// timeout_ms near int64 max overflows the Duration multiply — the
	// clamp must land before conversion, so a fast command still runs, and
	// the deadline it runs under is the cap itself.
	for _, ms := range []int64{math.MaxInt64, 300001, 600001, 9000000000000000000} {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
			t.Fatal(err)
		}
		runner := &fakeRunner{}
		tool, err := NewExecTool(mustOutputRepo(t, root), runner)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		res, err := tool.Execute(testContext(),
			mustCall(t, "huge-timeout",
				fmt.Sprintf(`{"command":"echo ok","timeout_ms":%d}`, ms), 0), nil)
		if err != nil {
			t.Fatalf("Execute(timeout_ms=%d): %v", ms, err)
		}
		if !strings.Contains(res.Text, "remote output") || strings.Contains(res.Text, "killed") {
			t.Fatalf("timeout_ms=%d must clamp to the cap, not expire instantly: %q", ms, res.Text)
		}
		if got := runner.deadline.Sub(start); got < maxExecTimeout-5*time.Second || got > maxExecTimeout+5*time.Second {
			t.Fatalf("timeout_ms=%d gave a %v deadline, want the %v cap", ms, got, maxExecTimeout)
		}
	}
}

func TestExecNegativeTimeoutIgnored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := newTool(t).Execute(service.WithConversationID(ctx, "c1"),
		mustCall(t, "neg-timeout", `{"command":"sleep 30","timeout_ms":-1}`, 0), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("negative timeout_ms must not shorten the run oddly: %v", elapsed)
	}
	if !strings.Contains(res.Text, "killed") {
		t.Fatalf("expected cancel kill, got %q", res.Text)
	}
}

func TestExecEmitsChunks(t *testing.T) {
	var chunks []string
	res, err := newTool(t).Execute(testContext(),
		mustCall(t, "emit-1", `{"command":"echo a; echo b"}`, 0),
		func(chunk string) { chunks = append(chunks, chunk) })
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Text
	// The streamed chunks reassemble into what the process wrote; the status
	// line is added after the stream, never emitted as a chunk.
	if streamed := strings.Join(chunks, ""); len(chunks) == 0 || !strings.HasPrefix(out, streamed) ||
		!strings.HasSuffix(out, "s)") || streamed == out {
		t.Fatalf("emitted chunks %q do not reassemble into %q", chunks, out)
	}
}

func TestExecSpillWriteFailureHonestMarker(t *testing.T) {
	// A non-directory at the output path makes the spill open fail.
	// The result must report that failure, not claim to have full output.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool, err := NewExecTool(mustOutputRepo(t, root), NewLocalRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	spillDir := filepath.Join(root, "c1", "tool-output")
	if err := os.WriteFile(spillDir, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(testContext(),
		mustCall(t, "full-1", `{"command":"seq 1 2000"}`, 400), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "spill to "+spillDir+string(os.PathSeparator)) || !strings.Contains(res.Text, " failed:") ||
		strings.Contains(res.Text, "full output:") {
		t.Fatalf("spill failure must surface in the marker: %q", res.Text)
	}
}

func TestExecRequiresCommand(t *testing.T) {
	for _, input := range []string{`{"command":"  "}`, `{"command":""}`, `{"timeout_ms":1000}`} {
		if _, err := newTool(t).Execute(testContext(),
			mustCall(t, "empty-1", input, 0), nil); err == nil {
			t.Fatalf("expected error for %s", input)
		}
	}
}

// failingSpillWriter accepts a bounded number of bytes and then either
// short-writes or fails — the fault injection the injected writer port
// makes possible.
type failingSpillWriter struct {
	accept int
	err    error
	wrote  int
	calls  int
}

func (w *failingSpillWriter) Write(p []byte) (int, error) {
	w.calls++
	n := min(len(p), w.accept)
	w.wrote += n
	if w.err != nil {
		return n, w.err
	}
	return n, nil // short write without an error
}

func (w *failingSpillWriter) Close() error { return nil }

// The spill counts the bytes the writer accepted, and the first failure
// latches: later writes must not retry or keep counting, or the marker
// could still claim "full output" for a broken file.
func TestSpillSinkLatchesWriteFailure(t *testing.T) {
	short := &failingSpillWriter{accept: 4}
	sink := &outputSink{writer: short}
	sink.writeSpill([]byte("0123456789"))
	if sink.spillWritten != 4 {
		t.Fatalf("counted %d bytes, want the 4 the writer accepted", sink.spillWritten)
	}
	if !errors.Is(sink.spillErr, io.ErrShortWrite) {
		t.Fatalf("spillErr = %v, want io.ErrShortWrite", sink.spillErr)
	}
	sink.writeSpill([]byte("more"))
	if short.calls != 1 || sink.spillWritten != 4 {
		t.Fatalf("failure not latched: calls=%d written=%d", short.calls, sink.spillWritten)
	}
}

func TestSpillSinkCountsOnlyAcceptedBytes(t *testing.T) {
	broken := &failingSpillWriter{accept: 2, err: errors.New("disk on fire")}
	sink := &outputSink{writer: broken}
	sink.writeSpill([]byte("0123456789"))
	if sink.spillWritten != 2 || sink.spillErr == nil ||
		!strings.Contains(sink.spillErr.Error(), "disk on fire") {
		t.Fatalf("written=%d err=%v, want 2/disk on fire", sink.spillWritten, sink.spillErr)
	}
	sink.writeSpill([]byte("more"))
	if broken.calls != 1 {
		t.Fatalf("error not latched: calls=%d", broken.calls)
	}

	full := &failingSpillWriter{accept: math.MaxInt}
	ok := &outputSink{writer: full}
	ok.writeSpill([]byte("0123456789"))
	if ok.spillWritten != 10 || ok.spillErr != nil {
		t.Fatalf("written=%d err=%v, want 10/nil", ok.spillWritten, ok.spillErr)
	}
}

// stoppedContext builds the ctx a stopped turn hands a tool: canceled with a
// cause, which is how the tool can name who stopped it.
func stoppedContext() (context.Context, context.CancelCauseFunc) {
	return context.WithCancelCause(service.WithConversationID(context.Background(), "c1"))
}

// The owner's Stop must not erase the work the process already did: the
// result keeps the head and closes with who stopped it and how long it ran.
func TestExecInterruptedByOwnerKeepsTheHead(t *testing.T) {
	ctx, stop := stoppedContext()
	defer stop(nil)
	go func() {
		time.Sleep(150 * time.Millisecond)
		stop(service.ErrInterruptedByUser)
	}()
	res, err := newTool(t).Execute(ctx,
		mustCall(t, "stop-1", `{"command":"echo first line; echo second line; sleep 30"}`, 0), nil)
	if err != nil {
		t.Fatalf("a stopped run must return its result, not an error: %v", err)
	}
	if !strings.HasPrefix(res.Text, "first line\nsecond line\n(interrupted by user after ") {
		t.Fatalf("stopped run = %q, want the head then the interrupt line", res.Text)
	}
	if !strings.HasSuffix(res.Text, "s — partial output above)") {
		t.Fatalf("interrupt line lost its duration: %q", res.Text)
	}
}

// A stop before the process said anything leaves nothing but the line — and
// still no error, so the turn persists it as the call's outcome.
func TestExecInterruptedBeforeOutputIsJustTheLine(t *testing.T) {
	ctx, stop := stoppedContext()
	defer stop(nil)
	go func() {
		time.Sleep(100 * time.Millisecond)
		stop(service.ErrInterruptedByUser)
	}()
	res, err := newTool(t).Execute(ctx, mustCall(t, "stop-2", `{"command":"sleep 30"}`, 0), nil)
	if err != nil {
		t.Fatalf("a stopped run must not be a Go error: %v", err)
	}
	if !strings.HasPrefix(res.Text, "(interrupted by user after ") || !strings.HasSuffix(res.Text, "— partial output above)") {
		t.Fatalf("silent stop = %q, want only the interrupt line", res.Text)
	}
	if strings.Contains(res.Text, "no output") || strings.Contains(res.Text, "signal") {
		t.Fatalf("silent stop = %q, want no exit-status wording", res.Text)
	}
}

// A restart is not the owner's decision either: the same result says the
// server went down under it.
func TestExecRestartNamesTheServer(t *testing.T) {
	ctx, stop := stoppedContext()
	defer stop(nil)
	go func() {
		time.Sleep(150 * time.Millisecond)
		stop(service.ErrServerRestarted)
	}()
	res, err := newTool(t).Execute(ctx,
		mustCall(t, "restart-1", `{"command":"echo working; sleep 30"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Text, "working\n(nusashell restarted after ") {
		t.Fatalf("restart = %q, want the head then the restart line", res.Text)
	}
}

// An agent's own cancel has its own words — the sentinel no path sets yet is
// wired through, so the contract does not have to change when one does.
func TestExecInterruptedByAgentHasItsOwnWords(t *testing.T) {
	ctx, stop := stoppedContext()
	defer stop(nil)
	go func() {
		time.Sleep(150 * time.Millisecond)
		stop(service.ErrInterruptedByAgent)
	}()
	res, err := newTool(t).Execute(ctx,
		mustCall(t, "agent-stop", `{"command":"echo thinking; sleep 30"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Text, "thinking\n(interrupted by agent after ") {
		t.Fatalf("agent cancel = %q, want the head then the agent line", res.Text)
	}
}

// A command that ends on its own never claims an interruption, whatever the
// ctx does after it exits.
func TestExecNormalExitNeverSaysInterrupted(t *testing.T) {
	res, err := newTool(t).Execute(testContext(),
		mustCall(t, "plain-1", `{"command":"echo done"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "interrupted") || strings.Contains(res.Text, "canceled") {
		t.Fatalf("normal exit = %q, want no interruption wording", res.Text)
	}
}

// A finished but slow run tells the model which tool would have been better:
// the harness cannot know a command's duration in advance, so the result says
// it once it knows — above the status line, which stays last because that is
// the line clients read to decide how the call ended.
func TestExecLongRunSuggestsExecSession(t *testing.T) {
	tool := newTool(t)
	ctx := testContext()

	result, err := tool.Execute(ctx, mustCall(t, "quick", `{"command":"echo quick"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Text, "exec_session") {
		t.Fatalf("a fast run must not suggest exec_session: %q", result.Text)
	}

	previous := longRunHintAfter
	longRunHintAfter = 0
	defer func() { longRunHintAfter = previous }()

	// A clean long run: hint above, status line closing the result.
	result, err = tool.Execute(ctx, mustCall(t, "slow", `{"command":"echo slow"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(result.Text, "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "(exit 0, ") {
		t.Fatalf("last line = %q, want the status line", last)
	}
	if !strings.Contains(result.Text, "ran 0s; use exec_session and poll for runs like this\n(exit 0, ") {
		t.Fatalf("hint must sit above the status line: %q", result.Text)
	}

	// A failed long run is the case the clients read: the exit code must stay
	// on the last line, or a failure renders as a success.
	result, err = tool.Execute(ctx, mustCall(t, "failed", `{"command":"echo boom; exit 3"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(result.Text, "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "(exit 3, ") {
		t.Fatalf("last line = %q, want (exit 3, …)", last)
	}
	if len(lines) < 2 || !strings.Contains(lines[len(lines)-2], "ran 0s; use exec_session and poll for runs like this") {
		t.Fatalf("hint missing above the failed status: %q", result.Text)
	}

	// Output that does not end its own line: the separator is added once, so
	// the hint never starts on a blank line.
	result, err = tool.Execute(ctx, mustCall(t, "no-newline", `{"command":"printf boom"}`, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Text, "boom\nran 0s; use exec_session and poll for runs like this\n(exit 0, ") {
		t.Fatalf("no-newline output = %q, want one newline before the hint", result.Text)
	}
	if strings.Contains(result.Text, "boom\n\n") {
		t.Fatalf("no-newline output left a blank line: %q", result.Text)
	}

	// The hint is part of the record, not a display extra: a spilled result's
	// file carries it too, above the status line that closes the file.
	result, err = tool.Execute(ctx, mustCall(t, "slow-spill", `{"command":"seq 1 2000"}`, 400), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, path, ok := strings.Cut(result.Text, "full output: ")
	if !ok {
		t.Fatalf("spill marker missing: %q", result.Text)
	}
	path, _, _ = strings.Cut(path, " (")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	spilled := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(spilled) < 2 {
		t.Fatalf("spill file = %q, want the output plus the closing lines", raw)
	}
	if last := spilled[len(spilled)-1]; !strings.HasPrefix(last, "(exit 0, ") {
		t.Fatalf("spill file last line = %q, want the status line", last)
	}
	if prev := spilled[len(spilled)-2]; !strings.Contains(prev, "ran 0s; use exec_session and poll for runs like this") {
		t.Fatalf("spill file hint missing above the status: %q", prev)
	}
}
