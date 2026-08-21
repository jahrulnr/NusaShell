package aiutil

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// Regression coverage for the rewritten idleTimeoutReader:
//   1. reads after the watchdog fired report ErrIdleTimeout, never the
//      underlying "closed connection" error flavor,
//   2. bytes delivered by a read overlapping the firing are preserved,
//   3. a stale firing can never re-arm itself (gen counter guard).

type errReadCloser struct{ err error }

func (e *errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e *errReadCloser) Close() error             { return nil }

// slowReadCloser delivers one byte per delayed read and never EOFs.
type slowReadCloser struct{ delay time.Duration }

func (s *slowReadCloser) Read(p []byte) (int, error) {
	time.Sleep(s.delay)
	if len(p) == 0 {
		return 0, io.EOF
	}
	p[0] = 'x'
	return 1, nil
}
func (s *slowReadCloser) Close() error { return nil }

func waitWatchdogFired(t *testing.T, r *idleTimeoutReader) {
	t.Helper()
	for i := 0; i < 200; i++ {
		r.mu.Lock()
		f := r.fired && r.gen > 0
		r.mu.Unlock()
		if f {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("watchdog never fired")
}

func TestIdleTimeoutReadAfterFireClassifiesAsTimeout(t *testing.T) {
	body := &errReadCloser{err: errors.New("use of closed network connection")}
	r := newIdleTimeoutReader(body, 20*time.Millisecond)
	defer r.Close()
	waitWatchdogFired(t, r)
	n, err := r.Read(make([]byte, 16))
	if n != 0 || !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("read after watchdog fire: got (%d, %v), want (0, ErrIdleTimeout)", n, err)
	}
}

func TestIdleTimeoutPreservesDataAcrossBoundary(t *testing.T) {
	// Each read takes longer than the whole idle window, so the watchdog
	// fires while a read is blocked holding one byte.
	body := &slowReadCloser{delay: 60 * time.Millisecond}
	r := newIdleTimeoutReader(body, 20*time.Millisecond)
	defer r.Close()

	var got []byte
	buf := make([]byte, 8)
	for {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			if !errors.Is(err, ErrIdleTimeout) {
				t.Fatalf("boundary err = %v, want ErrIdleTimeout", err)
			}
			break
		}
		if len(got) > 64 {
			t.Fatal("timeout never surfaced")
		}
	}
	if len(got) == 0 {
		t.Fatal("data delivered across the timeout boundary was dropped")
	}
}

func TestIdleTimeoutDoesNotRearmAfterFire(t *testing.T) {
	r := newIdleTimeoutReader(&errReadCloser{err: io.ErrClosedPipe}, 20*time.Millisecond)
	defer r.Close()
	waitWatchdogFired(t, r)
	r.mu.Lock()
	gen := r.gen
	r.mu.Unlock()
	for i := 0; i < 50; i++ {
		_, _ = r.Read(make([]byte, 4))
	}
	time.Sleep(40 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen != gen {
		t.Fatalf("stale firing re-armed the watchdog: gen %d -> %d", gen, r.gen)
	}
}

func TestIdleTimeoutHealthyStreamUnaffected(t *testing.T) {
	data := strings.Repeat("data: hello\n\n", 200)
	r := newIdleTimeoutReader(io.NopCloser(strings.NewReader(data)), time.Second)
	defer r.Close()
	events := 0
	err := ReadSSE(context.Background(), r, 0, func(ev Event) error {
		events++
		return nil
	})
	if err != nil {
		t.Fatalf("healthy stream errored: %v", err)
	}
	if events != 200 {
		t.Fatalf("events = %d, want 200", events)
	}
}
