package transport

// handleStream serves the per-round SSE stream that carries live agent
// deltas (text, reasoning, tool output) for one round.
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
//   - round.delta: contracts.RoundDeltaFrame {seq, kind, text, ...}
//   - round.done:  contracts.RoundDoneFrame {state, run_id, message_id,
//     round, usage, next, error} — terminal. next is non-nil when the agent
//     continues with another round (tool loop or auto-continue).
//
// The stream stays open until the round is sealed or the client disconnects.
// A client that connects before the round has published anything waits up to
// the registry's wait window (the round was signaled by agent.turn.started
// but has not produced a first delta yet); afterwards it gets 404, and the
// frontend falls back to a snapshot refresh + agent.turns.active re-attach.
//
// No auth is required (loopback-only service, same as /ws); the route is not
// origin-restricted because SSE cannot be used for cross-origin reads
// without CORS headers, and no CORS headers are set here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const streamPingInterval = 15 * time.Second

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
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
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	ping := time.NewTicker(streamPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
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
