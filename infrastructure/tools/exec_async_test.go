package tools

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"nusashell/application"
)

// yamlField extracts a `key: value` line from a yamlMD front-matter block.
func yamlField(t *testing.T, out, key string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	t.Fatalf("missing %q in output:\n%s", key, out)
	return ""
}

func waitForExecFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("log %s never contained %q; contents: %q", path, want, string(data))
}

func TestExecBackgroundReturnsIDAndKeepsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	defer tb.Close()
	out, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"command":"sleep 30","background":true}`))
	if err != nil {
		t.Fatalf("background spawn: %v", err)
	}
	id := yamlField(t, out, "exec_id")
	if !strings.HasPrefix(id, "exec_") {
		t.Fatalf("exec_id shape wrong: %q", id)
	}
	logPath := yamlField(t, out, "log_path")
	if !strings.Contains(logPath, string(os.PathSeparator)+"terminal"+string(os.PathSeparator)) {
		t.Fatalf("log_path not under terminal dir: %q", logPath)
	}
	if got := yamlField(t, out, "status"); got != "running" {
		t.Fatalf("status = %q, want running", got)
	}
	st, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"op":"status","id":"`+id+`"}`))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got := yamlField(t, st, "status"); got != "running" {
		t.Fatalf("status op = %q, want running: %s", got, st)
	}
	if _, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"op":"kill","id":"`+id+`"}`)); err != nil {
		t.Fatalf("kill: %v", err)
	}
}

func TestExecBackgroundLogReceivesLiveOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	defer tb.Close()
	out, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"command":"echo async-line; sleep 30","background":true}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	id := yamlField(t, out, "exec_id")
	waitForExecFile(t, yamlField(t, out, "log_path"), "async-line")
	_, _ = tb.Execute(context.Background(), "exec", []byte(`{"op":"kill","id":"`+id+`"}`))
}

func TestExecWaitReturnsExitCodeAndTail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	defer tb.Close()
	out, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"command":"echo job-done; exit 5","background":true}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	id := yamlField(t, out, "exec_id")
	waitOut, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"op":"wait","id":"`+id+`","timeout_ms":5000}`))
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := yamlField(t, waitOut, "exit_code"); got != "5" {
		t.Fatalf("exit_code = %q, want 5: %s", got, waitOut)
	}
	if !strings.Contains(waitOut, "job-done") {
		t.Fatalf("wait output missing tail body: %s", waitOut)
	}
	st, _ := tb.Execute(context.Background(), "exec", []byte(`{"op":"status","id":"`+id+`"}`))
	if got := yamlField(t, st, "status"); got == "running" {
		t.Fatalf("exited process still reported running: %s", st)
	}
}

func TestExecWaitTimeoutKeepsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	defer tb.Close()
	out, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"command":"sleep 30","background":true}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	id := yamlField(t, out, "exec_id")
	waitOut, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"op":"wait","id":"`+id+`","timeout_ms":300}`))
	if err != nil {
		t.Fatalf("wait timeout must not be an error: %v", err)
	}
	if got := yamlField(t, waitOut, "status"); got != "running" {
		t.Fatalf("status after wait timeout = %q, want running: %s", got, waitOut)
	}
	_, _ = tb.Execute(context.Background(), "exec", []byte(`{"op":"kill","id":"`+id+`"}`))
}

func TestExecKillTerminatesProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	defer tb.Close()
	out, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"command":"sleep 60","background":true}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	id := yamlField(t, out, "exec_id")
	killOut, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"op":"kill","id":"`+id+`"}`))
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if got := yamlField(t, killOut, "status"); got == "running" {
		t.Fatalf("kill did not terminate: %s", killOut)
	}
}

func TestExecListScopedPerConversation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	defer tb.Close()
	ctxA := application.WithConversationID(context.Background(), "conv_a")
	ctxB := application.WithConversationID(context.Background(), "conv_b")
	out, err := tb.Execute(ctxA, "exec", []byte(`{"command":"sleep 30","background":true}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	id := yamlField(t, out, "exec_id")

	listA, err := tb.Execute(ctxA, "exec", []byte(`{"op":"list"}`))
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if !strings.Contains(listA, id) {
		t.Fatalf("conv_a list missing %s: %s", id, listA)
	}
	listB, err := tb.Execute(ctxB, "exec", []byte(`{"op":"list"}`))
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if strings.Contains(listB, id) {
		t.Fatalf("conv_b must not see conv_a processes: %s", listB)
	}
	// Cross-conversation control is denied: conv_b cannot kill conv_a's proc.
	if _, err := tb.Execute(ctxB, "exec", []byte(`{"op":"kill","id":"`+id+`"}`)); err == nil {
		t.Fatal("cross-conversation kill must fail")
	}
	if _, err := tb.Execute(ctxA, "exec", []byte(`{"op":"kill","id":"`+id+`"}`)); err != nil {
		t.Fatalf("owner kill: %v", err)
	}
}

func TestExecBackgroundBudgetPerConversation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	defer tb.Close()
	tb.execRegistry().limit = 2
	ctxA := application.WithConversationID(context.Background(), "conv_a")
	ctxB := application.WithConversationID(context.Background(), "conv_b")
	var ids []string
	for i := 0; i < 2; i++ {
		out, err := tb.Execute(ctxA, "exec", []byte(`{"command":"sleep 30","background":true}`))
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		ids = append(ids, yamlField(t, out, "exec_id"))
	}
	if _, err := tb.Execute(ctxA, "exec", []byte(`{"command":"sleep 30","background":true}`)); err == nil ||
		!strings.Contains(err.Error(), "background") {
		t.Fatalf("third spawn must hit the per-conversation budget, err=%v", err)
	}
	// Budget is per-conversation: conv_b still has room.
	if _, err := tb.Execute(ctxB, "exec", []byte(`{"command":"sleep 30","background":true}`)); err != nil {
		t.Fatalf("conv_b spawn must not count conv_a procs: %v", err)
	}
	// Killing frees the slot.
	if _, err := tb.Execute(ctxA, "exec", []byte(`{"op":"kill","id":"`+ids[0]+`"}`)); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if _, err := tb.Execute(ctxA, "exec", []byte(`{"command":"sleep 30","background":true}`)); err != nil {
		t.Fatalf("spawn after kill must succeed: %v", err)
	}
}

func TestExecAsyncOpsValidateArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	defer tb.Close()
	for _, args := range []string{
		`{"op":"status"}`,                 // missing id
		`{"op":"kill","id":"exec_nope0"}`, // unknown id
		`{"op":"bogus"}`,                  // unknown op
		`{"op":"run"}`,                    // command still required
		`{"background":true}`,             // background without command
	} {
		if _, err := tb.Execute(context.Background(), "exec", []byte(args)); err == nil {
			t.Fatalf("args %s must fail", args)
		}
	}
}

func TestExecToolboxCloseKillsBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	out, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"command":"sleep 60","background":true}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	id := yamlField(t, out, "exec_id")
	tb.Close()
	st, err := tb.Execute(context.Background(), "exec", []byte(`{"op":"status","id":"`+id+`"}`))
	if err != nil {
		t.Fatalf("status after close: %v", err)
	}
	if got := yamlField(t, st, "status"); got == "running" {
		t.Fatalf("Close must kill managed processes: %s", st)
	}
}
