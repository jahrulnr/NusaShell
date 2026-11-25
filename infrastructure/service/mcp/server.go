package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// toolCallDeadline caps one tools/call run — a variable so a test can
// prove the seam without sleeping four minutes. Production keeps the
// domain ceiling; nothing else may set it.
var toolCallDeadline = dservice.ToolCallDeadline

// Server is one tool server: its identity, its tools, and the handler that
// runs a call. Call errors become isError results so the caller can read
// them; ctx ends when the client cancels the request or the stream closes.
type Server struct {
	Name    string
	Version string
	Tools   []Tool
	Call    func(ctx context.Context, name string, args json.RawMessage) (Result, error)
}

// Serve answers MCP requests from in on out until in closes; ctx bounds
// the tool calls.
// Tool calls run concurrently — a long wait must not hold up a quick read —
// and each can be cancelled with notifications/cancelled.
func Serve(ctx context.Context, in io.Reader, out io.Writer, s Server) error {
	if s.Name == "" || s.Call == nil {
		return fmt.Errorf("mcp server needs a name and a call handler")
	}
	ctx, cancel := context.WithCancel(ctx)
	r := bufio.NewReader(in)
	w := bufio.NewWriter(out)
	var (
		wmu     sync.Mutex
		wg      sync.WaitGroup
		mu      sync.Mutex
		running = map[string]context.CancelFunc{}
	)
	reply := func(id json.RawMessage, result any, rerr *rpcError, framed bool) {
		m := map[string]any{"jsonrpc": "2.0", "id": id}
		if rerr != nil {
			m["error"] = rerr
		} else {
			m["result"] = result
		}
		wmu.Lock()
		defer wmu.Unlock()
		_ = writeMessage(w, m, framed)
	}
	// A closed stream cannot carry results: stop the calls, then wait.
	defer func() { cancel(); wg.Wait() }()
	for {
		body, framed, err := readMessage(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var req message
		if json.Unmarshal(body, &req) != nil {
			continue
		}
		if req.ID == nil {
			// A notification: only cancellation needs action.
			if req.Method == "notifications/cancelled" {
				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				_ = json.Unmarshal(req.Params, &p)
				mu.Lock()
				if stop, ok := running[string(p.RequestID)]; ok {
					stop()
				}
				mu.Unlock()
			}
			continue
		}
		id := *req.ID
		switch req.Method {
		case "initialize":
			reply(id, map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			}, nil, framed)
		case "ping":
			reply(id, map[string]any{}, nil, framed)
		case "tools/list":
			tools := s.Tools
			if tools == nil {
				tools = []Tool{}
			}
			reply(id, map[string]any{"tools": tools}, nil, framed)
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			callCtx, stop := context.WithCancel(ctx)
			key := string(id)
			mu.Lock()
			running[key] = stop
			mu.Unlock()
			wg.Go(func() {
				defer func() {
					mu.Lock()
					delete(running, key)
					mu.Unlock()
					stop()
				}()
				// The seam deadline caps any tool this server runs —
				// adapters carry no timeout of their own. When it fires
				// the caller reads a timed-out result; the parent ctx
				// dying stays the cancel path, never a tool timeout.
				deadlineCtx, deadlineStop := context.WithTimeout(callCtx, toolCallDeadline)
				defer deadlineStop()
				var res Result
				if strings.TrimSpace(p.Name) == "" {
					res = ErrorResult("missing tool name")
				} else if out, err := s.Call(deadlineCtx, p.Name, p.Arguments); err != nil {
					if deadlineCtx.Err() == context.DeadlineExceeded && callCtx.Err() == nil {
						res = ErrorResult(dservice.ToolCallTimeoutText)
					} else {
						res = ErrorResult(err.Error())
					}
				} else {
					res = out
				}
				if res.Content == nil {
					res.Content = []Content{}
				}
				reply(id, res, nil, framed)
			})
		default:
			reply(id, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}, framed)
		}
	}
}
