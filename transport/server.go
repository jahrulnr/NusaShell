// Package transport maps wire traffic onto the application service:
// HTTP POST /rpc, SSE GET /events, WebSocket /ws, and the embedded frontend.
package transport

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
	mux.HandleFunc("POST /rpc", s.handleRPC)
	mux.HandleFunc("GET /events", s.handleSSE)
	mux.HandleFunc("GET /ws", s.handleWS)
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
	return logRequests(s.Logger, s.mux)
}

// maxRPCBodyBytes fits the documented attachment contract (four 4 MiB files
// as base64 data URLs, plus JSON envelope overhead) and leaves headroom for
// plugin ZIP uploads (plugin.install), which can carry pruned node_modules.
const maxRPCBodyBytes = 64 << 20

// ---- HTTP RPC ----

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	var req contracts.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRPCBodyBytes)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, contracts.ErrResult(contracts.CodeValidation, "malformed request body"))
		return
	}
	// Audit aid: accept the method as ?event=<method> so the browser
	// Network tab shows which RPC call each /rpc request performs. The
	// query value wins when both are present; clients that send only the
	// body method are unaffected.
	if ev := r.URL.Query().Get("event"); ev != "" {
		req.Method = ev
	}
	start := time.Now()
	result, rpcErr := s.App.Dispatch(r.Context(), req.Method, req.Payload)
	if rpcErr != nil {
		s.Logger.Debug("rpc error", "method", req.Method, "code", rpcErr.Code, "elapsed_ms", time.Since(start).Milliseconds())
		writeJSON(w, http.StatusOK, contracts.ErrResult(rpcErr.Code, rpcErr.Message))
		return
	}
	s.Logger.Debug("rpc", "method", req.Method, "elapsed_ms", time.Since(start).Milliseconds())
	writeJSON(w, http.StatusOK, contracts.OKResult(result))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- SSE ----

const sseHeartbeat = 15 * time.Second

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_, events, unsubscribe := s.App.Bus.Subscribe()
	defer unsubscribe()

	ctx := r.Context()
	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	// announce the connection so clients can mark themselves online
	writeSSE(w, flusher, contracts.NewEvent("sse.hello", map[string]string{"transport": "sse"}))

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			writeSSE(w, flusher, ev)
		case <-heartbeat.C:
			writeSSE(w, flusher, contracts.NewEvent("ping", map[string]string{}))
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, ev contracts.Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

// ---- request logging middleware ----

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Debug("http", "method", r.Method, "path", r.URL.Path, "elapsed_ms", time.Since(start).Milliseconds())
	})
}
