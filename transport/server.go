// Package transport maps wire traffic onto the application service:
// HTTP POST /rpc, WebSocket /ws, and the embedded frontend.
package transport

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/resources"
)

// Server wires the application service to all transports.
type Server struct {
	App    *application.App
	Logger *slog.Logger
	Static http.Handler
	Dev    bool
	mux    *http.ServeMux
}

// New builds a Server with all routes wired.
func New(app *application.App, logger *slog.Logger, static http.Handler, dev bool) *Server {
	mux := http.NewServeMux()
	s := &Server{App: app, Logger: logger, Static: static, Dev: dev, mux: mux}
	mux.HandleFunc("POST /rpc/{method...}", s.handleRPC)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /stream", s.handleStream)
	mux.HandleFunc("GET /local-file", s.handleLocalFile)
	// Sound assets: serve embedded notification sounds (turn-complete,
	// turn-error) from resources/sounds/. Registered before the catch-all
	// so /sounds/* does not fall through to the frontend file server.
	if soundFS, err := resources.SoundAssets(); err == nil {
		mux.Handle("GET /sounds/", http.StripPrefix("/sounds/", http.FileServer(http.FS(soundFS))))
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		s.Static.ServeHTTP(w, r)
	})
	return s
}

// RoutesMux returns the underlying mux so callers can register
// additional routes (e.g. plugin handlers) before serving.
func (s *Server) RoutesMux() *http.ServeMux { return s.mux }

func (s *Server) Routes() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		s.mux.ServeHTTP(w, r)
		s.Logger.Debug("http", "method", r.Method, "path", r.URL.Path, "elapsed_ms", time.Since(start).Milliseconds())
	})
}

// maxRPCBodyBytes fits the documented attachment contract (four 4 MiB files
// as base64 data URLs, plus JSON envelope overhead) and leaves headroom for
// plugin ZIP uploads (plugin.install), which can carry pruned node_modules.
const maxRPCBodyBytes = 64 << 20

// ---- HTTP RPC ----

// handleRPC dispatches path-based RPC calls. The method is derived from
// the URL path (e.g. /rpc/agent/conversations/list → "agent.conversations.list")
// so each call is individually visible in the browser Network tab. The
// request body carries the payload (and optionally a redundant method
// field for debug readability); the path is authoritative.
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	method := strings.ReplaceAll(r.PathValue("method"), "/", ".")
	var req contracts.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRPCBodyBytes)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, contracts.ErrResult(contracts.CodeValidation, "malformed request body"))
		return
	}
	start := time.Now()
	result, rpcErr := s.App.Dispatch(r.Context(), method, req.Payload)
	if rpcErr != nil {
		s.Logger.Debug("rpc error", "method", method, "code", rpcErr.Code, "elapsed_ms", time.Since(start).Milliseconds())
		writeJSON(w, http.StatusOK, contracts.ErrResult(rpcErr.Code, rpcErr.Message))
		return
	}
	s.Logger.Debug("rpc", "method", method, "elapsed_ms", time.Since(start).Milliseconds())
	writeJSON(w, http.StatusOK, contracts.OKResult(result))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- request logging middleware ----
