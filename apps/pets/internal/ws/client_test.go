package ws

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeConn yields a scripted sequence of messages then returns an error.
type fakeConn struct {
	mu        sync.Mutex
	msgs      [][]byte
	err       error
	closed    bool
	readIdx   int
	closeCh   chan struct{}
	closeOnce sync.Once
}

func (f *fakeConn) ReadMessage() (int, []byte, error) {
	if f.closeCh != nil {
		<-f.closeCh
		return 0, nil, errClosed
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readIdx >= len(f.msgs) {
		return 0, nil, f.err
	}
	data := f.msgs[f.readIdx]
	f.readIdx++
	return 1, data, nil
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.closeOnce.Do(func() {
		if f.closeCh != nil {
			close(f.closeCh)
		}
	})
	return nil
}

// dialResult is one Dial outcome: either conn or err.
type dialResult struct {
	conn *fakeConn
	err  error
}

// fakeDialer returns dial results in order. Once exhausted it returns a
// terminal error so the client stops redialing.
type fakeDialer struct {
	mu       sync.Mutex
	results  []dialResult
	dials    int
	terminal error
	dialed   chan struct{}
	dialOnce sync.Once
}

func (d *fakeDialer) Dial(ctx context.Context, url string) (Conn, error) {
	if d.dialed != nil {
		d.dialOnce.Do(func() { close(d.dialed) })
	}
	d.mu.Lock()
	idx := d.dials
	d.dials++
	d.mu.Unlock()
	if idx >= len(d.results) {
		return nil, d.terminal
	}
	r := d.results[idx]
	if r.err != nil {
		return nil, r.err
	}
	return r.conn, nil
}

// quietLogger discards output so reconnect-backoff tests stay readable.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBackoffDelay(t *testing.T) {
	t.Parallel()
	b := Backoff{Base: 100 * time.Millisecond, Max: 1 * time.Second, Factor: 2}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{4, 1 * time.Second}, // capped
		{10, 1 * time.Second},
	}
	for _, c := range cases {
		if got := b.Delay(c.attempt); got != c.want {
			t.Fatalf("Delay(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

func TestClientReceivesMessages(t *testing.T) {
	t.Parallel()
	conn := &fakeConn{
		msgs: [][]byte{[]byte(`{"state":"thinking","message":"hi"}`), []byte(`{"state":"done"}`)},
		err:  io.EOF,
	}
	dialer := &fakeDialer{
		results:  []dialResult{{conn: conn}},
		terminal: errClosed,
	}

	var mu sync.Mutex
	var got [][]byte
	h := HandlerFunc(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, data)
	})

	c := NewClient(dialer, "ws://x", h, quietLogger())
	c.SetBackoff(Backoff{Base: time.Millisecond, Max: 10 * time.Millisecond, Factor: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("handler got %d messages, want 2: %+v", len(got), got)
	}
	if string(got[0]) != `{"state":"thinking","message":"hi"}` {
		t.Fatalf("got[0] = %s", got[0])
	}
}

func TestClientReconnectsAfterDisconnect(t *testing.T) {
	t.Parallel()
	conn1 := &fakeConn{msgs: [][]byte{[]byte("a")}, err: io.EOF}
	conn2 := &fakeConn{msgs: [][]byte{[]byte("b")}, err: errClosed}
	dialer := &fakeDialer{
		results:  []dialResult{{conn: conn1}, {conn: conn2}},
		terminal: errClosed,
	}

	var mu sync.Mutex
	var got []string
	h := HandlerFunc(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, string(data))
	})

	c := NewClient(dialer, "ws://x", h, quietLogger())
	c.SetBackoff(Backoff{Base: time.Millisecond, Max: 5 * time.Millisecond, Factor: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("expected >=2 messages across reconnect, got %d: %+v", len(got), got)
	}
	if got[0] != "a" || got[1] != "b" {
		t.Fatalf("order wrong: %+v", got)
	}
	if dialer.dials < 2 {
		t.Fatalf("expected >=2 dials, got %d", dialer.dials)
	}
}

func TestClientReconnectsAfterDialFailure(t *testing.T) {
	t.Parallel()
	conn := &fakeConn{msgs: [][]byte{[]byte("ok")}, err: errClosed}
	dialer := &fakeDialer{
		results: []dialResult{
			{err: errors.New("connection refused")}, // first dial fails
			{conn: conn},                            // second succeeds
		},
		terminal: errClosed,
	}

	var mu sync.Mutex
	var got []string
	h := HandlerFunc(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, string(data))
	})

	c := NewClient(dialer, "ws://x", h, quietLogger())
	c.SetBackoff(Backoff{Base: time.Millisecond, Max: 5 * time.Millisecond, Factor: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "ok" {
		t.Fatalf("expected [ok] after dial failure+reconnect, got %+v", got)
	}
	if dialer.dials < 2 {
		t.Fatalf("expected >=2 dials, got %d", dialer.dials)
	}
}

func TestClientCloseStops(t *testing.T) {
	t.Parallel()
	dialer := &fakeDialer{terminal: errors.New("nope")}
	c := NewClient(dialer, "ws://x", HandlerFunc(func([]byte) {}), quietLogger())
	c.SetBackoff(Backoff{Base: 50 * time.Millisecond, Max: 50 * time.Millisecond, Factor: 2})
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	time.Sleep(80 * time.Millisecond)
	c.Close()
	select {
	case err := <-done:
		_ = err
	case <-time.After(time.Second):
		t.Fatal("Run did not return after Close")
	}
}

func TestClientCloseInterruptsRead(t *testing.T) {
	t.Parallel()
	conn := &fakeConn{closeCh: make(chan struct{})}
	dialer := &fakeDialer{
		results:  []dialResult{{conn: conn}},
		terminal: errClosed,
		dialed:   make(chan struct{}),
	}
	c := NewClient(dialer, "ws://x", HandlerFunc(func([]byte) {}), quietLogger())
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-dialer.dialed:
	case <-time.After(time.Second):
		t.Fatal("client did not dial")
	}
	c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return while ReadMessage was blocked")
	}
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if !closed {
		t.Fatal("Close did not close the active connection")
	}
}

func TestClientAllowsNilHandler(t *testing.T) {
	t.Parallel()
	conn := &fakeConn{msgs: [][]byte{[]byte("ignored")}, err: errClosed}
	dialer := &fakeDialer{results: []dialResult{{conn: conn}}, terminal: errClosed}
	c := NewClient(dialer, "ws://x", nil, quietLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Run error = %v, want deadline exceeded", err)
	}
}

func TestClientContextCancelStops(t *testing.T) {
	t.Parallel()
	dialer := &fakeDialer{terminal: errors.New("nope")}
	c := NewClient(dialer, "ws://x", HandlerFunc(func([]byte) {}), quietLogger())
	c.SetBackoff(Backoff{Base: time.Second, Max: time.Second, Factor: 2})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}
