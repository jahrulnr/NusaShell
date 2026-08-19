// Trajectory recording writes a JSONL log of learning layer events for
// debugging and observability. Each line is one event: extraction, review,
// edge build, consolidation, decay, prune, search.
//
// Storage: learning_trajectory.jsonl — append-only, one event per line.
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
	path := filepath.Join(dataDir, "learning_trajectory.jsonl")
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
		Timestamp: time.Now(),
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

// trajectoryFileName is the trajectory log path inside a data directory.
func trajectoryFileName(dataDir string) string {
	return filepath.Join(dataDir, "learning_trajectory.jsonl")
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
