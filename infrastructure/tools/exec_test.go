package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	out, err := tb.Execute(context.Background(), "exec", []byte(`{"command":"echo hello-exec"}`))
	if err != nil {
		t.Fatalf("exec echo: %v", err)
	}
	if !strings.Contains(out, "hello-exec") || !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestExecExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	_, err := tb.Execute(context.Background(), "exec", []byte(`{"command":"exit 3"}`))
	out, _ := tb.Execute(context.Background(), "exec", []byte(`{"command":"sh -c 'echo out; exit 7'"}`))
	if !strings.Contains(out, "exit_code: 7") {
		t.Fatalf("expected exit_code 7 in output: %q (first err: %v)", out, err)
	}
}

func TestExecCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	dir := t.TempDir()
	tb := &Toolbox{}
	args := `{"command":"pwd","cwd":"` + jsonPath(dir) + `"}`
	out, err := tb.Execute(context.Background(), "exec", []byte(args))
	if err != nil {
		t.Fatalf("exec pwd: %v", err)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(out, resolved) && !strings.Contains(out, dir) {
		t.Fatalf("cwd not honored: %q", out)
	}
	_ = os.Remove(dir) // keep linters calm about unused os import on some platforms
}

func TestExecIdleTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	start := time.Now()
	args := `{"command":"sleep 30","idle_timeout_ms":500}`
	_, err := tb.Execute(context.Background(), "exec", []byte(args))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected idle timeout error")
	}
	if !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("wrong error: %v", err)
	}
	// Must be cancelled by the watchdog, not run to completion.
	if elapsed > 10*time.Second {
		t.Fatalf("idle timeout did not fire early enough: %s", elapsed)
	}
}

func TestExecLongRunningNotKilledWhileProducingOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	// Produces output every 200ms for ~2.5s with idle timeout 1s and an
	// explicit wall-clock cap of 4s. The run must survive past the idle
	// window because output keeps flowing.
	args := `{"command":"for i in 1 2 3 4 5 6 7 8 9 10 11 12; do echo tick-$i; sleep 0.2; done","idle_timeout_ms":1000,"timeout_ms":4000}`
	start := time.Now()
	out, err := tb.Execute(context.Background(), "exec", []byte(args))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("long-running exec failed: %v", err)
	}
	if !strings.Contains(out, "tick-12") {
		t.Fatalf("missing final tick: %q", out)
	}
	if elapsed < 2*time.Second {
		t.Fatalf("finished too fast to have streamed: %s", elapsed)
	}
}

func TestExecTailBuffer(t *testing.T) {
	b := newTailBuffer(8, 8)
	b.Write([]byte("0123456789ABCDEFGHIJ"))
	snap := b.Snapshot()
	if !strings.HasPrefix(snap, "01234567") || !strings.HasSuffix(snap, "CDEFGHIJ") {
		t.Fatalf("buffer shape wrong: %q", snap)
	}
	if !strings.Contains(snap, "elided") {
		t.Fatalf("missing elision marker: %q", snap)
	}
	small := newTailBuffer(100, 100)
	small.Write([]byte("short"))
	if got := small.Snapshot(); got != "short" {
		t.Fatalf("small buffer altered: %q", got)
	}
}
