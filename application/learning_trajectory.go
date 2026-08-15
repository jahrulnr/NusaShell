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
