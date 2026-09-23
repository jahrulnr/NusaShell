package transport

// handleStream serves the per-round SSE stream that carries live agent
// deltas (text, reasoning, tool output, and provider activity) for one round.
//
// Query parameters:
//   - run_id:      the TurnRun id (agent.turn.started / agent.turns.active)
//   - message_id:  the assistant message the round produces (unique per
//     round, including across auto-continue turns that reuse run_id)
//   - after:       optional last-seen Seq. Frames with Seq <= after are
//     skipped — re-opening with after=<lastSeq> after a dropped connection
//     replays exactly the missed frames (idempotent resume).
//
// Frames (event: name, data: JSON):
//   - round.delta: contracts.RoundDeltaFrame {seq, kind, tool_call_id, name,
//     args?, text, activity?, presentation?}; tool start frames carry args and
//     the versioned presentation contract, later chunks carry text only.
//     Activity frames announce provider-side work such as tool-call
//     construction before a valid tool card exists; they carry no arguments.
//   - round.done:  contracts.RoundDoneFrame {state, run_id, message_id,
//     round, usage, next, error} — terminal. next is non-nil when the agent
//     continues with another round (tool loop or auto-continue).
//
// The stream stays open until the round is sealed or the client disconnects.
// Interactive rounds are registered before agent.turn.started and the response
// flushes immediately, so a slow first provider delta does not produce a 404.
// A 404 means the requested round was unknown or its process-local entry had
// expired; the frontend reconciles against agent.turns.active and the snapshot.
//
// Loopback requests bypass pairing auth; non-loopback requests require a
// valid paired session cookie (enforced by AuthMiddleware and re-checked here
// before subscribe). The route is not origin-restricted because SSE cannot be
// used for cross-origin reads without CORS headers, and no CORS headers are
// set here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nusashell/contracts"
)

const streamPingInterval = 15 * time.Second

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	// Non-loopback callers need a valid paired session before subscribing.
	// (AuthMiddleware enforces this at the mux too; this re-check keeps the
	// handler correct when the mux is exercised directly, e.g. in tests.)
	remote := !IsLoopbackRequest(r)
	if remote && s.Pairing == nil {
		writeRemoteAccessDisabled(w)
		return
	}
	if remote && !s.remoteSessionAlive(r) {
		writePairingRequired(w)
		return
	}
	q := r.URL.Query()
	runID := q.Get("run_id")
	messageID := q.Get("message_id")
	if runID == "" || messageID == "" {
		http.Error(w, "run_id and message_id are required", http.StatusBadRequest)
		return
	}
	var after int64
	if v := q.Get("after"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid after", http.StatusBadRequest)
			return
		}
		after = parsed
	}

	if s.App == nil || s.App.RoundStreams == nil {
		http.Error(w, "round streams unavailable", http.StatusInternalServerError)
		return
	}
	sub, err := s.App.RoundStreams.Subscribe(r.Context(), runID, messageID, after)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		// Unknown round (GC'd, backend restarted, or never started). The
		// frontend falls back to a snapshot refresh + turns.active re-attach.
		http.Error(w, "round stream not found", http.StatusNotFound)
		return
	}
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	writeFrame := func(name string, v any) error {
		return writeSSEFrame(w, flusher, name, "", v)
	}
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	ping := time.NewTicker(streamPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			// Re-validate a remote session on each ping tick so a revoked
			// device loses the stream within one interval instead of keeping
			// it until disconnect.
			if remote && !s.remoteSessionAlive(r) {
				return
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case frame := <-sub.Frames():
			if err := writeFrame("round.delta", frame); err != nil {
				return
			}
		case <-sub.Done():
			// Drain frames already queued behind the seal, then send the
			// terminal round.done frame.
			for {
				select {
				case frame := <-sub.Frames():
					if err := writeFrame("round.delta", frame); err != nil {
						return
					}
				default:
					if err := writeFrame("round.done", sub.DoneFrame()); err != nil {
						return
					}
					return
				}
			}
		}
	}
}

func writeSSEFrame(w http.ResponseWriter, flusher http.Flusher, event, id string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (s *Server) handleAcpRunStream(w http.ResponseWriter, r *http.Request) {
	remote := !IsLoopbackRequest(r)
	if remote && s.Pairing == nil {
		writeRemoteAccessDisabled(w)
		return
	}
	if remote && !s.remoteSessionAlive(r) {
		writePairingRequired(w)
		return
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	if conversationID == "" {
		http.Error(w, "conversation_id is required", http.StatusBadRequest)
		return
	}
	if s.App == nil || s.App.AcpRunStreams == nil {
		http.Error(w, "ACP run streams unavailable", http.StatusInternalServerError)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sub := s.App.AcpRunStreams.Subscribe(conversationID)
	if sub == nil {
		http.Error(w, "ACP run stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	snapshot := sub.Snapshot()
	if err := writeSSEFrame(w, flusher, contracts.EventAcpRunStream, strconv.FormatInt(snapshot.Seq, 10), snapshot); err != nil {
		return
	}

	ping := time.NewTicker(streamPingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if remote && !s.remoteSessionAlive(r) {
				return
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case frame := <-sub.Frames():
			if err := writeSSEFrame(w, flusher, contracts.EventAcpRunStream, strconv.FormatInt(frame.Seq, 10), frame); err != nil {
				return
			}
		case <-sub.Done():
			return
		}
	}
}
