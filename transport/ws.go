package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"nusashell/contracts"
)

// handleWS upgrades to WebSocket. The client sends {id, method, payload}
// request frames and receives {id, ok, result|error} replies plus server
// events as {type, payload}.
//
// Loopback requests bypass pairing auth and keep the loopback origin
// allowlist. Non-loopback requests require a valid paired session (enforced
// by AuthMiddleware and re-checked here) plus a same-origin browser Origin —
// the WS endpoint exposes RPC and MCP command execution, so cross-origin or
// unauthenticated remote upgrades would be RCE. Remote sessions also cannot
// invoke pairing management methods over WS frames.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Loopback: accept with the loopback origin allowlist (no session needed).
	if IsLoopbackRequest(r) {
		s.acceptLoopbackWS(w, r)
		return
	}
	// Remote: require a valid paired session and a same-origin browser Origin.
	if !s.requireRemoteSession(w, r) {
		return
	}
	if !remoteWSOriginAllowed(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(contracts.ErrResult(contracts.CodePairingUnauthorized, "cross-origin WebSocket denied"))
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: remoteOriginPatterns(r),
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	conn.SetReadLimit(maxRPCBodyBytes)
	// The session is re-validated on every incoming frame and by a periodic
	// watchdog so a revoked device loses access mid-connection instead of
	// keeping it until disconnect. The watchdog covers a revoked client that
	// only listens to the event stream without sending frames.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.watchRemoteSession(ctx, conn, r)
	s.serveWS(ctx, conn, r, false)
}

// acceptLoopbackWS accepts a WebSocket from a loopback caller using the
// loopback origin allowlist. Loopback callers bypass pairing auth.
func (s *Server) acceptLoopbackWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: loopbackOriginPatterns(),
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	conn.SetReadLimit(maxRPCBodyBytes)
	s.serveWS(r.Context(), conn, r, true)
}

// remoteSessionAlive reports whether the request's paired session is still
// valid. Used to re-check remote WS frames and SSE streams mid-connection so
// a revoked session stops working instead of living until disconnect.
func (s *Server) remoteSessionAlive(r *http.Request) bool {
	return s.Pairing != nil && s.HasValidSession(r)
}

// remoteSessionWatchInterval bounds how long a revoked remote session keeps
// a live WebSocket connection while only listening (no incoming frames).
const remoteSessionWatchInterval = 5 * time.Second

// watchRemoteSession periodically re-validates a remote WebSocket's session
// and closes the connection once the session is no longer valid. Per-frame
// re-checks in serveWS deny commands immediately; this watchdog covers the
// passive-listener case. The goroutine exits when the connection or the
// request context ends.
func (s *Server) watchRemoteSession(ctx context.Context, conn *websocket.Conn, r *http.Request) {
	go func() {
		ticker := time.NewTicker(remoteSessionWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.remoteSessionAlive(r) {
					_ = conn.Close(websocket.StatusPolicyViolation, "session revoked")
					return
				}
			}
		}
	}()
}

// serveWS runs the read/write loop. When loopback is false (remote paired
// session), pairing management methods are denied over WS frames and the
// session is re-validated per frame so revocation takes effect immediately.
func (s *Server) serveWS(ctx context.Context, conn *websocket.Conn, r *http.Request, loopback bool) {
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
		// A revoked remote session must not run even one more frame — the
		// upgrade-time check alone would let it work until disconnect.
		if !loopback && !s.remoteSessionAlive(r) {
			_ = conn.Close(websocket.StatusPolicyViolation, "session revoked")
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
		// Remote paired sessions cannot invoke pairing management methods
		// over WS. The public /pairing/* routes are the only remote path.
		if !loopback && IsPairingManagementMethod(req.Method) {
			errBody, _ := json.Marshal(contracts.WSResponse{
				ID:    req.ID,
				Error: &contracts.RPCError{Code: contracts.CodePairingUnauthorized, Message: "pairing management is local-only"},
			})
			_ = conn.Write(ctx, websocket.MessageText, errBody)
			continue
		}
		if !loopback && IsRemoteAccessSettingsChange(req.Method, req.Payload) {
			errBody, _ := json.Marshal(contracts.WSResponse{
				ID:    req.ID,
				Error: &contracts.RPCError{Code: contracts.CodePairingUnauthorized, Message: "remote access settings are host-only"},
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

// remoteWSOriginAllowed reports whether a browser Origin header on a remote
// (non-loopback) WebSocket upgrade is same-origin with the effective request
// scheme + host. Non-browser clients (no Origin) are always allowed.
// Cross-origin and scheme-mismatched browser origins are rejected to prevent
// CSRF (the server exposes MCP command execution).
//
// This keeps the stricter scheme+host rule it has always had; the HTTP routes
// use isSameOriginRequest (host-only) because their scheme depends on
// X-Forwarded-Proto trust, while a WS upgrade can only be forwarded by a
// loopback peer here.
func remoteWSOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser client
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return false // malformed or schemeless origin
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return u.Scheme == scheme && u.Host == r.Host
}

// remoteOriginPatterns returns the origin patterns allowed for a remote
// paired WS upgrade: the effective request host only.
func remoteOriginPatterns(r *http.Request) []string {
	if r.Host == "" {
		return nil
	}
	return []string{r.Host}
}

// loopbackOriginPatterns returns the origin patterns allowed for WS
// connections from loopback callers (who bypass pairing auth). Non-loopback
// callers are gated by the pairing session middleware plus the same-origin
// check in remoteWSOriginAllowed.
func loopbackOriginPatterns() []string {
	return []string{
		"localhost:*",
		"127.0.0.1:*",
		"::1:*",
	}
}
