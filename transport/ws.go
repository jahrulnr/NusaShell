package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"nusashell/contracts"
)

// handleWS upgrades to WebSocket. The client sends {id, method, payload}
// request frames and receives {id, ok, result|error} replies plus server
// events as {type, payload}.
//
// Origin is restricted to loopback hosts (localhost, 127.0.0.1, ::1) to
// prevent CSRF from browser-based attackers. The server has no auth and
// exposes MCP command execution, so cross-origin WS would be RCE.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: loopbackOriginPatterns(),
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	conn.SetReadLimit(maxRPCBodyBytes)

	ctx := r.Context()
	_, events, unsubscribe := s.App.Bus.Subscribe()
	defer unsubscribe()

	// writer: forward bus events to the socket until the subscription
	// closes or the request context is cancelled.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.Logger.Error("ws event writer panic recovered", "panic", r)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				b, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				_ = conn.Write(writeCtx, websocket.MessageText, b)
				cancel()
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var req contracts.WSRequest
		if err := json.Unmarshal(data, &req); err != nil {
			errBody, _ := json.Marshal(contracts.WSResponse{
				ID:    req.ID,
				Error: &contracts.RPCError{Code: contracts.CodeValidation, Message: "malformed frame"},
			})
			_ = conn.Write(ctx, websocket.MessageText, errBody)
			continue
		}
		result, rpcErr := s.App.Dispatch(ctx, req.Method, req.Payload)
		resp := contracts.WSResponse{ID: req.ID}
		if rpcErr != nil {
			s.Logger.Debug("ws rpc error", "method", req.Method, "code", rpcErr.Code)
			resp.Error = rpcErr
		} else {
			b, err := json.Marshal(result)
			if err != nil {
				resp.Error = &contracts.RPCError{Code: contracts.CodeInternal, Message: err.Error()}
			} else {
				resp.OK = true
				resp.Result = b
			}
		}
		b, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
			return
		}
	}
}

// loopbackOriginPatterns returns the origin patterns allowed for WS
// connections. Only loopback hosts are accepted to prevent CSRF from
// browser-based attackers (the server has no auth and exposes MCP
// command execution).
func loopbackOriginPatterns() []string {
	return []string{
		"localhost:*",
		"127.0.0.1:*",
		"::1:*",
	}
}
