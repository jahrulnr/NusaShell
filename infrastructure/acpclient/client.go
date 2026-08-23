package acpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Handler receives incoming agent→client requests (permission, fs).
type Handler interface {
	RequestPermission(ctx context.Context, params RequestPermissionParams) (RequestPermissionResult, error)
	ReadTextFile(ctx context.Context, params ReadTextFileParams) (ReadTextFileResult, error)
	WriteTextFile(ctx context.Context, params WriteTextFileParams) error
	SessionUpdate(params SessionUpdateParams)
}

// wire is the transport-neutral read/write/close surface for ACP JSON-RPC.
// stdioWire wraps a local subprocess; remoteWire wraps a WebSocket to a
// cloud agent. Both speak the same line-delimited JSON-RPC 2.0 protocol.
type wire interface {
	Write(body []byte) error
	Read() ([]byte, error)
	Close() error
}

// stdioWire wraps a local ACP subprocess's stdin/stdout.
type stdioWire struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	cancel context.CancelFunc
}

func (w *stdioWire) Write(body []byte) error {
	return writeFrame(w.stdin, body)
}

func (w *stdioWire) Read() ([]byte, error) {
	return readFrame(w.reader)
}

func (w *stdioWire) Close() error {
	w.stdin.Close()
	w.cancel()
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	return nil
}

// Conn is one ACP session transport (stdio or remote).
type Conn struct {
	w      wire
	cancel context.CancelFunc

	mu      sync.Mutex
	nextID  int64
	pending map[int]chan rpcReply
	closed  atomic.Bool

	handler Handler
}

type rpcReply struct {
	result json.RawMessage
	err    error
}

// Dial spawns command with args/env/cwd and starts the read loop.
func Dial(ctx context.Context, command string, args []string, env []string, cwd string, handler Handler) (*Conn, error) {
	if command == "" {
		return nil, fmt.Errorf("acp command is empty")
	}
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Env = env
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s: %w", command, err)
	}
	w := &stdioWire{cmd: cmd, stdin: stdin, reader: bufio.NewReader(stdout), cancel: cancel}
	c := &Conn{
		w:       w,
		cancel:  cancel,
		pending: map[int]chan rpcReply{},
		handler: handler,
	}
	go c.readLoop()
	go func() {
		_ = cmd.Wait()
		c.failAll(fmt.Errorf("ACP process exited"))
	}()
	return c, nil
}

// DialWire creates a Conn from an existing wire (e.g., remoteWire for
// cloud agents). Used when the transport is already established.
func DialWire(ctx context.Context, w wire, handler Handler) (*Conn, error) {
	_, cancel := context.WithCancel(ctx)
	c := &Conn{
		w:       w,
		cancel:  cancel,
		pending: map[int]chan rpcReply{},
		handler: handler,
	}
	go c.readLoop()
	return c, nil
}

func (c *Conn) Close() {
	if c.closed.Swap(true) {
		return
	}
	c.failAll(fmt.Errorf("ACP connection closed"))
	_ = c.w.Close()
	c.cancel()
}

func (c *Conn) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- rpcReply{err: err}
		close(ch)
		delete(c.pending, id)
	}
}

func (c *Conn) call(ctx context.Context, method string, params any, result any) error {
	id := int(atomic.AddInt64(&c.nextID, 1))
	ch := make(chan rpcReply, 1)
	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return fmt.Errorf("ACP connection closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	req := jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.mu.Lock()
	err = c.w.Write(body)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case reply := <-ch:
		if reply.err != nil {
			return reply.err
		}
		if result == nil || len(reply.result) == 0 || string(reply.result) == "null" {
			return nil
		}
		return json.Unmarshal(reply.result, result)
	}
}

func (c *Conn) notify(method string, params any) error {
	body, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.Write(body)
}

func (c *Conn) reply(id any, result any, rpcErr *rpcError) {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: rpcErr}
	if rpcErr == nil {
		raw, _ := json.Marshal(result)
		resp.Result = raw
	}
	body, _ := json.Marshal(resp)
	c.mu.Lock()
	_ = c.w.Write(body)
	c.mu.Unlock()
}

func (c *Conn) readLoop() {
	for {
		body, err := c.w.Read()
		if err != nil {
			c.failAll(err)
			return
		}
		var msg jsonRPCResponse
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if msg.Method != "" {
			c.handleIncoming(msg)
			continue
		}
		id, ok := asInt(msg.ID)
		if !ok {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if !ok {
			continue
		}
		if msg.Error != nil {
			ch <- rpcReply{err: &RPCError{Code: msg.Error.Code, Message: msg.Error.Message}}
		} else {
			ch <- rpcReply{result: msg.Result}
		}
		close(ch)
	}
}

func (c *Conn) handleIncoming(msg jsonRPCResponse) {
	ctx := context.Background()
	switch msg.Method {
	case "session/update":
		var p SessionUpdateParams
		if err := json.Unmarshal(msg.Params, &p); err == nil && c.handler != nil {
			c.handler.SessionUpdate(p)
		}
	case "session/request_permission":
		var p RequestPermissionParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			c.reply(msg.ID, nil, &rpcError{Code: -32602, Message: err.Error()})
			return
		}
		go func() {
			if c.handler == nil {
				c.reply(msg.ID, RequestPermissionResult{Outcome: PermissionOutcome{Outcome: "cancelled"}}, nil)
				return
			}
			res, err := c.handler.RequestPermission(context.Background(), p)
			if err != nil {
				c.reply(msg.ID, RequestPermissionResult{Outcome: PermissionOutcome{Outcome: "cancelled"}}, nil)
				return
			}
			c.reply(msg.ID, res, nil)
		}()
	case "fs/read_text_file":
		var p ReadTextFileParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			c.reply(msg.ID, nil, &rpcError{Code: -32602, Message: err.Error()})
			return
		}
		if c.handler == nil {
			c.reply(msg.ID, nil, &rpcError{Code: -32601, Message: "fs not available"})
			return
		}
		res, err := c.handler.ReadTextFile(ctx, p)
		if err != nil {
			c.reply(msg.ID, nil, &rpcError{Code: -32000, Message: err.Error()})
			return
		}
		c.reply(msg.ID, res, nil)
	case "fs/write_text_file":
		var p WriteTextFileParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			c.reply(msg.ID, nil, &rpcError{Code: -32602, Message: err.Error()})
			return
		}
		if c.handler == nil {
			c.reply(msg.ID, nil, &rpcError{Code: -32601, Message: "fs not available"})
			return
		}
		if err := c.handler.WriteTextFile(ctx, p); err != nil {
			c.reply(msg.ID, nil, &rpcError{Code: -32000, Message: err.Error()})
			return
		}
		c.reply(msg.ID, map[string]any{}, nil)
	default:
		if msg.ID != nil {
			c.reply(msg.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + msg.Method})
		}
	}
}

func asInt(id any) (int, bool) {
	switch v := id.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

func (c *Conn) Initialize(ctx context.Context) (InitializeResult, error) {
	var out InitializeResult
	err := c.call(ctx, "initialize", InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientCapabilities: ClientCapabilities{
			FS: &FSCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
		ClientInfo: Implementation{Name: "nusashell", Title: "NusaShell", Version: "0.1.0"},
	}, &out)
	return out, err
}

func (c *Conn) Authenticate(ctx context.Context, methodID string) error {
	return c.call(ctx, "authenticate", AuthenticateParams{MethodID: methodID}, nil)
}

func (c *Conn) NewSession(ctx context.Context, cwd string) (NewSessionResult, error) {
	var out NewSessionResult
	err := c.call(ctx, "session/new", NewSessionParams{Cwd: cwd, MCPServers: []any{}}, &out)
	return out, err
}

func (c *Conn) Prompt(ctx context.Context, sessionID, text string) (PromptResult, error) {
	var out PromptResult
	err := c.call(ctx, "session/prompt", PromptParams{
		SessionID: sessionID,
		Prompt:    []ContentBlock{{Type: "text", Text: text}},
	}, &out)
	return out, err
}

func (c *Conn) Cancel(sessionID string) error {
	return c.notify("session/cancel", CancelParams{SessionID: sessionID})
}

func (c *Conn) SetMode(ctx context.Context, sessionID, modeID string) error {
	return c.call(ctx, "session/set_mode", SetModeParams{SessionID: sessionID, ModeID: modeID}, nil)
}

func (c *Conn) SetModel(ctx context.Context, sessionID, modelID string) (SetModelResult, error) {
	var out SetModelResult
	err := c.call(ctx, "session/set_model", SetModelParams{SessionID: sessionID, ModelID: modelID}, &out)
	return out, err
}
