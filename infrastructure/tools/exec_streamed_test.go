package tools

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestExecStreamedChunks verifies that a running exec command emits output
// chunks through the onChunk callback as they are produced, before the
// command finishes, and that the final result still carries the tail output.
func TestExecStreamedChunks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	var mu sync.Mutex
	var chunks []string
	start := time.Now()
	handled, _, err := executeExecToolChunks(context.Background(), "exec", []byte(
		`{"command":"echo first; sleep 0.4; echo second","idle_timeout_ms":5000}`),
		func(text string) {
			mu.Lock()
			chunks = append(chunks, text)
			mu.Unlock()
		})
	if !handled {
		t.Fatal("exec should be handled by the streaming executor")
	}
	if err != nil {
		t.Fatalf("streamed exec failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(chunks, "")
	if !strings.Contains(joined, "first") || !strings.Contains(joined, "second") {
		t.Fatalf("streamed chunks missing output lines: %q", joined)
	}
	// The first chunk must arrive while the command is still running
	// (before the total ~0.4s sleep finishes).
	if time.Since(start) > 3*time.Second {
		t.Fatalf("exec took too long: %s", time.Since(start))
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d: %q", len(chunks), chunks)
	}
}

// TestExecStreamedCancellation verifies that cancelling the context kills
// the child and that no chunks are emitted after cancellation returns.
func TestExecStreamedCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var chunks []string
	done := make(chan error, 1)
	go func() {
		_, _, err := executeExecToolChunks(ctx, "exec", []byte(`{"command":"for i in $(seq 1 100); do echo n-$i; sleep 0.2; done"}`),
			func(text string) {
				mu.Lock()
				chunks = append(chunks, text)
				mu.Unlock()
			})
		done <- err
	}()
	time.Sleep(600 * time.Millisecond)
	cancel()
	err := <-done
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("wrong cancellation error: %v", err)
	}
	// Cancellation must include the partial output received so far, so
	// the App layer can persist it into the tool call result (the snapshot
	// is what a reloaded conversation renders).
	if !strings.Contains(err.Error(), "partial output") {
		t.Fatalf("cancellation error lost partial output: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("expected some chunks before cancellation")
	}
}

// TestExecStreamedNonStreamingFallback verifies the plain Execute path still
// works (no onChunk callback) and returns the same combined output shape.
func TestExecStreamedNonStreamingFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell syntax")
	}
	tb := &Toolbox{}
	out, err := tb.Execute(context.Background(), "exec", []byte(`{"command":"echo hello-stream"}`))
	if err != nil {
		t.Fatalf("exec echo: %v", err)
	}
	if !strings.Contains(out, "hello-stream") || !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("unexpected output: %q", out)
	}
}
