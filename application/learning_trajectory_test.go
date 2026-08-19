package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrajectoryRecorderWritesEvents(t *testing.T) {
	dir := t.TempDir()
	r := NewTrajectoryRecorder(dir)
	if r == nil {
		t.Fatal("expected non-nil recorder")
	}

	r.Record("search", map[string]interface{}{
		"query":  "docker",
		"result": 3,
	})
	r.Record("review", map[string]interface{}{
		"conversation": "conv_1",
		"user_msgs":    5,
	})

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "learning", "trajectory.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 events, got %d", len(lines))
	}
	if !strings.Contains(lines[0], `"type":"search"`) {
		t.Errorf("line 0 missing type=search: %s", lines[0])
	}
	if !strings.Contains(lines[0], `"query":"docker"`) {
		t.Errorf("line 0 missing query: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"type":"review"`) {
		t.Errorf("line 1 missing type=review: %s", lines[1])
	}
}

func TestTrajectoryRecorderNilIsNoOp(t *testing.T) {
	var r *TrajectoryRecorder
	r.Record("test", nil) // should not panic
	r.Close()             // should not panic
}

func TestTrajectoryRecorderEmptyDirReturnsNil(t *testing.T) {
	r := NewTrajectoryRecorder("")
	if r != nil {
		t.Error("expected nil for empty dataDir")
	}
}
