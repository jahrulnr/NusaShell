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
package learn

import (
	"bytes"
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
			if id := DetailString(ev.Detail, "llm_conversation_id"); id != "" {
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
	// Close before replace: Windows cannot rename over an open file, and
	// os.Rename refuses to replace an existing destination on Windows.
	_ = r.file.Close()
	r.file = nil
	_ = os.Remove(path)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		f, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if openErr == nil {
			r.file = f
		}
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return transcripts
	}
	r.file = f
	return transcripts
}

// trajectoryFileName is the trajectory log path inside a data directory.
func trajectoryFileName(dataDir string) string {
	return filepath.Join(dataDir, "learning", "trajectory.jsonl")
}

// ReadTrajectoryPage reads one bounded page of learning events, newest first.
// Cursor is the exclusive byte offset returned by the previous page; zero
// starts from the file's current end. Reading backwards keeps the common
// first-page path proportional to the requested page instead of loading years
// of history. Since offsets point before the oldest returned line, appends do
// not shift an in-progress pagination snapshot.
func ReadTrajectoryPage(dataDir string, limit int, cursor int64) ([]TrajectoryEvent, int64, bool) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	f, err := os.Open(trajectoryFileName(dataDir))
	if err != nil {
		return nil, 0, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return nil, 0, false
	}
	end := cursor
	if end <= 0 || end > info.Size() {
		end = info.Size()
	}

	const chunkSize int64 = 32 * 1024
	var carry []byte
	events := make([]TrajectoryEvent, 0, limit)
	var oldestOffset int64
	for end > 0 {
		start := end - chunkSize
		if start < 0 {
			start = 0
		}
		chunk := make([]byte, end-start)
		if _, err := f.ReadAt(chunk, start); err != nil {
			return events, 0, false
		}
		combined := append(chunk, carry...)
		parts := bytes.Split(combined, []byte{'\n'})
		offsets := make([]int64, len(parts))
		offset := start
		for i, part := range parts {
			offsets[i] = offset
			offset += int64(len(part) + 1)
		}

		firstComplete := 1
		if start == 0 {
			firstComplete = 0
		}
		for i := len(parts) - 1; i >= firstComplete; i-- {
			line := bytes.TrimSpace(parts[i])
			if len(line) == 0 {
				continue
			}
			var event TrajectoryEvent
			if json.Unmarshal(line, &event) != nil || trajectoryEventIsNoise(event.Type) {
				continue
			}
			if len(events) == limit {
				return events, oldestOffset, true
			}
			events = append(events, event)
			oldestOffset = offsets[i]
		}
		carry = append(carry[:0], parts[0]...)
		end = start
	}
	return events, 0, false
}

func trajectoryEventIsNoise(eventType string) bool {
	switch eventType {
	case "search", "graph_load":
		return true
	default:
		return false
	}
}

// ReadTrajectory loads learning events from the trajectory log, newest
// first. It is kept for callers that only need the newest bounded slice.
func ReadTrajectory(dataDir string, limit int) []TrajectoryEvent {
	events, _, _ := ReadTrajectoryPage(dataDir, limit, 0)
	return events
}
