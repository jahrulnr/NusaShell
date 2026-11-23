package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// Client is one MCP connection. Requests are multiplexed by id, so tool
// calls may run concurrently; Close ends the stream and fails anything in
// flight.
type Client struct {
	conn io.ReadWriteCloser
	w    *bufio.Writer
	wmu  sync.Mutex

	mu      sync.Mutex
	next    int64
	pending map[string]chan message
	err     error
	done    chan struct{}

	// Info is the server's name and version from initialize.
	Info struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
}

// Connect runs the initialize handshake over conn. conn is closed when the
// handshake fails or when the client is closed.
func Connect(ctx context.Context, conn io.ReadWriteCloser, clientName, clientVersion string) (*Client, error) {
	c := &Client{conn: conn, w: bufio.NewWriter(conn), pending: map[string]chan message{}, done: make(chan struct{})}
	go c.readLoop()
	var init struct {
		ServerInfo json.RawMessage `json:"serverInfo"`
	}
	err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": clientName, "version": clientVersion},
	}, &init)
	if err == nil {
		_ = json.Unmarshal(init.ServerInfo, &c.Info)
		err = c.notify("notifications/initialized", map[string]any{})
	}
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	return c, nil
}

// Tools lists the server's tools.
func (c *Client) Tools(ctx context.Context) ([]Tool, error) {
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.request(ctx, "tools/list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// Call runs one tool. A tool that failed comes back as a Result with
// IsError set; the error is for protocol and transport failures.
func (c *Client) Call(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var out Result
	err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &out)
	return out, err
}

// Done is closed once the connection is gone.
func (c *Client) Done() <-chan struct{} { return c.done }

// Close ends the connection.
func (c *Client) Close() error {
	err := c.conn.Close()
	<-c.done
	return err
}

func (c *Client) request(ctx context.Context, method string, params any, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.next++
	id := json.RawMessage(strconv.FormatInt(c.next, 10))
	ch := make(chan message, 1)
	c.pending[string(id)] = ch
	c.mu.Unlock()
	drop := func() {
		c.mu.Lock()
		delete(c.pending, string(id))
		c.mu.Unlock()
	}
	if err := c.send(message{JSONRPC: "2.0", ID: &id, Method: method, Params: raw}); err != nil {
		drop()
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		drop()
		_ = c.notify("notifications/cancelled", map[string]any{"requestId": id, "reason": "cancelled"})
		return ctx.Err()
	case <-c.done:
		return c.closedErr()
	}
}

func (c *Client) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.send(message{JSONRPC: "2.0", Method: method, Params: raw})
}

func (c *Client) send(m message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeMessage(c.w, m, false)
}

func (c *Client) closedErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) readLoop() {
	r := bufio.NewReader(c.conn)
	var err error
	for {
		var body []byte
		body, _, err = readMessage(r)
		if err != nil {
			break
		}
		var m message
		if json.Unmarshal(body, &m) != nil || m.ID == nil {
			continue // a server notification or request: nothing to do
		}
		if m.Method != "" {
			// A server-initiated request (sampling, roots): unsupported.
			id := *m.ID
			_ = c.send(message{JSONRPC: "2.0", ID: &id, Error: &rpcError{Code: -32601, Message: "method not found"}})
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[string(*m.ID)]
		delete(c.pending, string(*m.ID))
		c.mu.Unlock()
		if ok {
			ch <- m
		}
	}
	if err == io.EOF {
		err = fmt.Errorf("mcp server closed the connection")
	}
	c.mu.Lock()
	c.err = fmt.Errorf("mcp connection lost: %w", err)
	c.mu.Unlock()
	close(c.done)
}
