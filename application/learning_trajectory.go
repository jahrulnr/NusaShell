// Trajectory recording writes a JSONL log of learning layer events for
// debugging and observability. Each line is one event: extraction, review,
// edge build, consolidation, decay, prune, search.
//
// Storage: learning/trajectory.jsonl — append-only, one event per line.
// The file is human-auditable: tail -f to watch learning happen live.
//
// This is a debug/observability tool, not a source of truth. The actual
// learning state lives in memory.json, learning_edges.jsonl, etc. The
// trajectory log answers "what did the learning layer do and when?"
package application

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	clock "nusashell/pkg/time"
)

// TrajectoryEvent is one recorded learning layer event.
type TrajectoryEvent struct {
	Timestamp time.Time              `json:"ts"`
	Type      string                 `json:"type"` // extract|review|edge_build|consolidate|decay|prune|search
	Detail    map[string]interface{} `json:"detail,omitempty"`
}

// TrajectoryRecorder writes learning events to a JSONL file.
type TrajectoryRecorder struct {
	mu   sync.Mutex
	file *os.File
}

// NewTrajectoryRecorder opens or creates the trajectory log at dataDir.
// Returns nil (no-op) if the file cannot be opened — trajectory recording
// is best-effort and must never break the learning layer.
func NewTrajectoryRecorder(dataDir string) *TrajectoryRecorder {
	if dataDir == "" {
		return nil
	}
	path := filepath.Join(dataDir, "learning", "trajectory.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	return &TrajectoryRecorder{file: f}
}

// Record appends an event to the trajectory log. Safe for concurrent use.
// No-op if recorder is nil.
func (r *TrajectoryRecorder) Record(eventType string, detail map[string]interface{}) {
	if r == nil || r.file == nil {
		return
	}
	event := TrajectoryEvent{
		Timestamp: clock.NewTime().Time(),
		Type:      eventType,
		Detail:    detail,
	}
	b, err := json.Marshal(event)
	if err != nil {
		return
	}
	b = append(b, '\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.file.Write(b)
}

// Close closes the underlying file handle.
func (r *TrajectoryRecorder) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
}

// DeleteEvents rewrites the trajectory log without events matching the
// predicate, and returns the `llm_conversation_id` values of the removed
// events so callers can also delete the transcripts. Best-effort: a log
// that cannot be rewritten is left untouched. The append handle is
// reopened against the rewritten file so subsequent records keep landing
// in the log.
func (r *TrajectoryRecorder) DeleteEvents(match func(TrajectoryEvent) bool) []string {
	if r == nil || r.file == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	path := r.file.Name()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	kept := make([]string, 0, len(lines))
	var transcripts []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev TrajectoryEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			kept = append(kept, line)
			continue
		}
		if match(ev) {
			if id := detailString(ev.Detail, "llm_conversation_id"); id != "" {
				transcripts = append(transcripts, id)
			}
			continue
		}
		kept = append(kept, line)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		return nil
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil
	}
	_ = r.file.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		r.file = nil
		return transcripts
	}
	r.file = f
	return transcripts
}

// trajectoryFileName is the trajectory log path inside a data directory.
func trajectoryFileName(dataDir string) string {
	return filepath.Join(dataDir, "learning", "trajectory.jsonl")
}

// ReadTrajectory loads learning events from the trajectory log, newest
// first. Events that are pure UI query noise (search, graph_load) are
// excluded so the log surfaces learning-layer activity. Returns an empty
// slice when the file is missing or unreadable — the log view must never
// fail because the debug log is absent.
func ReadTrajectory(dataDir string, limit int) []TrajectoryEvent {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	b, err := os.ReadFile(trajectoryFileName(dataDir))
	if err != nil {
		return nil
	}
	var events []TrajectoryEvent
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e TrajectoryEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		switch e.Type {
		case "search", "graph_load":
			continue // UI query noise, not learning-layer activity
		}
		events = append(events, e)
	}
	// Reverse so the newest event is first.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	if len(events) > limit {
		events = events[:limit]
	}
	return events
}
