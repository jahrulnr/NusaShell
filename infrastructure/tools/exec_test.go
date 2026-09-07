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

func TestExecInheritsProcessPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH lookup")
	}
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "nusashell-path-fixture")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nprintf 'path-fixture\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tb := &Toolbox{}
	out, err := tb.Execute(context.Background(), "exec", []byte(`{"command":"command -v nusashell-path-fixture"}`))
	if err != nil {
		t.Fatalf("exec PATH lookup: %v", err)
	}
	if !strings.Contains(out, binPath) {
		t.Fatalf("exec did not inherit process PATH, output: %q", out)
	}
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
	if !strings.Contains(snap, "... (output truncated) ...") {
		t.Fatalf("missing truncation marker: %q", snap)
	}
	small := newTailBuffer(100, 100)
	small.Write([]byte("short"))
	if got := small.Snapshot(); got != "short" {
		t.Fatalf("small buffer altered: %q", got)
	}
}

func TestExecTailBufferKeepsEqualHeadAndTailAtTwentyK(t *testing.T) {
	if execHeadBytes != 10_000 || execTailBytes != 10_000 {
		t.Fatalf("exec in-band budget must be 10k+10k, got head=%d tail=%d", execHeadBytes, execTailBytes)
	}
	head := strings.Repeat("H", execHeadBytes)
	mid := strings.Repeat("M", 8_000)
	tail := strings.Repeat("T", execTailBytes)
	b := newTailBuffer(execHeadBytes, execTailBytes)
	if _, err := b.Write([]byte(head + mid + tail)); err != nil {
		t.Fatal(err)
	}
	snap := b.Snapshot()
	if !strings.HasPrefix(snap, head) {
		t.Fatalf("head lost: prefix=%q", snap[:min(len(snap), 32)])
	}
	if !strings.HasSuffix(snap, tail) {
		t.Fatalf("tail lost: suffix=%q", snap[max(0, len(snap)-32):])
	}
	if strings.Contains(snap, "M") {
		t.Fatalf("middle should be dropped, snapshot still contains M")
	}
	if !strings.Contains(snap, "... (output truncated) ...") {
		t.Fatalf("missing truncation marker: %q", snap[execHeadBytes-8:min(len(snap), execHeadBytes+40)])
	}
	if got := strings.Count(snap, "H") + strings.Count(snap, "T"); got != 20_000 {
		t.Fatalf("kept payload chars = %d, want 20000", got)
	}
}

func TestExecInBandTruncatesToTwentyKHeadAndTail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	out, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"command":"python3 -c 'print(\"H\"*10000 + \"M\"*8000 + \"T\"*10000, end=\"\")'"}`))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(out, strings.Repeat("H", 32)) {
		t.Fatalf("expected in-band head: %s", out[:min(len(out), 400)])
	}
	if !strings.Contains(out, strings.Repeat("T", 32)) {
		t.Fatalf("expected in-band tail: %s", out[max(0, len(out)-400):])
	}
	if strings.Contains(out, "M") {
		t.Fatalf("middle should be dropped from in-band exec output")
	}
	if !strings.Contains(out, "... (output truncated) ...") {
		t.Fatalf("missing truncation marker: %s", out[min(len(out), 9000):min(len(out), 10200)])
	}
	if !strings.Contains(out, "overflow_path:") {
		t.Fatalf("truncated exec must spill the full log: %s", out[:min(len(out), 500)])
	}
	path := overflowPathFrom(t, out)
	t.Cleanup(func() { _ = os.Remove(path) })
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(saved), "H") != 10000 || strings.Count(string(saved), "M") != 8000 || strings.Count(string(saved), "T") != 10000 {
		t.Fatalf("spill should keep the full 28000-char payload, got H=%d M=%d T=%d bytes=%d",
			strings.Count(string(saved), "H"), strings.Count(string(saved), "M"), strings.Count(string(saved), "T"), len(saved))
	}
}

// TestToolboxExecuteStreamedExec verifies the Toolbox streams exec output
// chunks through ExecuteStreamed and still returns the combined result.
func TestToolboxExecuteStreamedExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	var chunks []string
	out, err := tb.ExecuteStreamed(context.Background(), "exec",
		[]byte(`{"command":"echo alpha; echo beta"}`),
		func(text string) { chunks = append(chunks, text) })
	if err != nil {
		t.Fatalf("streamed exec: %v", err)
	}
	joined := strings.Join(chunks, "")
	if !strings.Contains(joined, "alpha") || !strings.Contains(joined, "beta") {
		t.Fatalf("missing streamed lines: %q", joined)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("final output incomplete: %q", out)
	}
}

// TestToolboxExecuteStreamedNonExec verifies non-exec tools fall back to the
// plain Execute path when streamed (grep returns an error path, never
// reaching the stream callback).
func TestToolboxExecuteStreamedNonExec(t *testing.T) {
	tb := &Toolbox{}
	out, err := tb.ExecuteStreamed(context.Background(), "grep",
		[]byte(`{"pattern":"x","path":"/nonexistent"}`),
		func(string) { t.Fatal("non-exec tool must not stream") })
	if err == nil || !strings.Contains(err.Error(), "path not found") {
		t.Fatalf("expected grep to fail, err=%v", err)
	}
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
}

func TestExecOverflowSpillsFullLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	out, err := tb.Execute(context.Background(), "exec",
		[]byte(`{"command":"python3 -c 'print(\"x\"*50000)'"}`))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(out, "overflow_path:") {
		t.Fatalf("expected spill for 50k stdout, got: %s", out[:min(len(out), 500)])
	}
	path := overflowPathFrom(t, out)
	t.Cleanup(func() { _ = os.Remove(path) })
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(saved), "x") < 50000 {
		t.Fatalf("spill incomplete: %d bytes", len(saved))
	}
	if !strings.Contains(out, "next_offset_bytes: 0") {
		t.Fatalf("exec overflow must be complete-file (next_offset_bytes 0): %s", out[:400])
	}
}

func TestExecSmallOutputDoesNotSpill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	out, err := tb.Execute(context.Background(), "exec", []byte(`{"command":"echo tiny"}`))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if strings.Contains(out, "overflow_path:") {
		t.Fatalf("small exec must not spill: %s", out)
	}
}
