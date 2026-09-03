// Package ws implements the WebSocket client that connects the pet overlay to
// the NusaShell backend (ws://127.0.0.1:9999/ws). It receives JSON state
// events and dispatches them to a handler. Reconnection uses exponential
// backoff capped at MaxBackoff.
//
// The gorilla/websocket Dialer/Conn are hidden behind the Dialer and Conn
// interfaces so the reconnect/dispatch logic is unit-testable with a fake
// dialer (no real server needed).
package ws

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"
)

// Conn is the subset of gorilla/websocket.Conn used by the client.
type Conn interface {
	ReadMessage() (messageType int, data []byte, err error)
	Close() error
}

// Dialer dials a WebSocket URL and returns a Conn.
type Dialer interface {
	Dial(ctx context.Context, url string) (Conn, error)
}

// Handler receives raw WebSocket messages. The caller parses them into events
// and drives the state machine.
type Handler interface {
	Handle(data []byte)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(data []byte)

func (f HandlerFunc) Handle(data []byte) { f(data) }

// Backoff controls reconnect delay. Delay = Base * factor^attempt, capped at
// Max, with jitter omitted for determinism in tests.
type Backoff struct {
	Base   time.Duration
	Max    time.Duration
	Factor float64
}

// DefaultBackoff is a sane exponential backoff for a local daemon.
var DefaultBackoff = Backoff{
	Base:   500 * time.Millisecond,
	Max:    10 * time.Second,
	Factor: 2.0,
}

// Delay returns the wait before the given attempt (0-indexed).
func (b Backoff) Delay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if b.Base <= 0 {
		return 0
	}
	factor := b.Factor
	if factor <= 0 {
		factor = 1
	}
	d := float64(b.Base) * math.Pow(factor, float64(attempt))
	if b.Max > 0 && d > float64(b.Max) {
		return b.Max
	}
	return time.Duration(d)
}

// Client is a reconnecting WebSocket client. Run blocks until ctx is canceled.
type Client struct {
	dialer  Dialer
	url     string
	handler Handler
	backoff Backoff
	log     *slog.Logger

	mu     sync.Mutex
	closed bool
	conn   Conn
	cancel context.CancelFunc
}

// NewClient creates a client. If log is nil, slog.Default is used.
func NewClient(dialer Dialer, url string, handler Handler, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		dialer:  dialer,
		url:     url,
		handler: handler,
		backoff: DefaultBackoff,
		log:     log,
	}
}

// SetBackoff overrides the reconnect backoff.
func (c *Client) SetBackoff(b Backoff) {
	c.mu.Lock()
	c.backoff = b
	c.mu.Unlock()
}

// Run dials, reads messages, and reconnects with backoff until ctx is canceled.
// It returns ctx.Err() when the context is done.
func (c *Client) Run(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		runCancel()
		return context.Canceled
	}
	c.cancel = runCancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.cancel = nil
		c.conn = nil
		c.mu.Unlock()
		runCancel()
	}()

	attempt := 0
	for {
		if c.isClosed() || runCtx.Err() != nil {
			return clientContextError(runCtx)
		}
		conn, err := c.dialer.Dial(runCtx, c.url)
		if err != nil {
			if runCtx.Err() != nil {
				return clientContextError(runCtx)
			}
			c.log.Warn("ws: dial failed", "url", c.url, "err", err, "attempt", attempt)
			if !c.sleep(runCtx, attempt) {
				return clientContextError(runCtx)
			}
			attempt++
			continue
		}
		c.setConn(conn)
		c.log.Info("ws: connected", "url", c.url)
		attempt = 0
		c.readLoop(runCtx, conn)
		_ = conn.Close()
		c.clearConn()
		if c.isClosed() || runCtx.Err() != nil {
			return clientContextError(runCtx)
		}
		c.log.Info("ws: disconnected, reconnecting")
		// brief pause before redialing
		if !c.sleep(runCtx, 0) {
			return clientContextError(runCtx)
		}
	}
}

// readLoop reads messages until the conn errors or ctx is canceled.
func (c *Client) readLoop(ctx context.Context, conn Conn) {
	for {
		if ctx.Err() != nil {
			return
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			if !errors.Is(err, errClosed) && ctx.Err() == nil {
				c.log.Warn("ws: read error", "err", err)
			}
			return
		}
		if c.handler != nil {
			c.handler.Handle(data)
		}
	}
}

// sleep waits for the backoff delay or ctx cancellation. Returns false if ctx
// was canceled.
func (c *Client) sleep(ctx context.Context, attempt int) bool {
	c.mu.Lock()
	b := c.backoff
	c.mu.Unlock()
	d := b.Delay(attempt)
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Close stops the client. Run will return on the next loop iteration.
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	conn := c.conn
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
}

func (c *Client) setConn(conn Conn) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
}

func (c *Client) clearConn() {
	c.mu.Lock()
	c.conn = nil
	c.mu.Unlock()
}

func clientContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Canceled
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// errClosed is a sentinel used by fake conns to signal a clean close.
var errClosed = errors.New("ws: connection closed")
