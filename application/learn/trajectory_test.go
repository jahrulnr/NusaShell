package learn

import (
	"encoding/json"
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

func TestReadTrajectoryPageIsStableAcrossAppends(t *testing.T) {
	dir := t.TempDir()
	r := NewTrajectoryRecorder(dir)
	if r == nil {
		t.Fatal("expected non-nil recorder")
	}
	for _, typ := range []string{"job_1", "search", "job_2", "graph_load", "job_3", "job_4", "job_5"} {
		r.Record(typ, map[string]interface{}{"name": typ})
	}

	first, cursor, more := ReadTrajectoryPage(dir, 2, 0)
	if len(first) != 2 || first[0].Type != "job_5" || first[1].Type != "job_4" {
		t.Fatalf("first page = %#v", first)
	}
	if cursor <= 0 || !more {
		t.Fatalf("first page cursor=%d more=%v", cursor, more)
	}

	// A newer append must not shift the cursor into a duplicate or skip an
	// older event while the user is paging through a snapshot.
	r.Record("job_6", map[string]interface{}{"name": "job_6"})
	second, next, more := ReadTrajectoryPage(dir, 2, cursor)
	if len(second) != 2 || second[0].Type != "job_3" || second[1].Type != "job_2" {
		t.Fatalf("second page = %#v", second)
	}
	if next <= 0 || !more {
		t.Fatalf("second page cursor=%d more=%v", next, more)
	}
	third, next, more := ReadTrajectoryPage(dir, 2, next)
	if len(third) != 1 || third[0].Type != "job_1" || next != 0 || more {
		t.Fatalf("third page=%#v cursor=%d more=%v", third, next, more)
	}
	_ = r.Close()
}

func TestReadTrajectoryPageAcrossChunkBoundaries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "learning", "trajectory.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	const eventCount = 45
	var content strings.Builder
	for i := 0; i < eventCount; i++ {
		event := TrajectoryEvent{
			Type: "large_event",
			Detail: map[string]interface{}{
				"index":   i,
				"padding": strings.Repeat("x", 3000),
			},
		}
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Marshal event %d: %v", i, err)
		}
		content.Write(line)
		content.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var got []int
	cursor := int64(0)
	for {
		page, next, more := ReadTrajectoryPage(dir, 7, cursor)
		for _, event := range page {
			index, ok := event.Detail["index"].(float64)
			if !ok {
				t.Fatalf("event index type = %T", event.Detail["index"])
			}
			got = append(got, int(index))
		}
		if !more {
			break
		}
		if next <= 0 || next == cursor {
			t.Fatalf("cursor did not advance backward: current=%d next=%d", cursor, next)
		}
		cursor = next
	}

	if len(got) != eventCount {
		t.Fatalf("got %d events, want %d", len(got), eventCount)
	}
	for i, index := range got {
		want := eventCount - 1 - i
		if index != want {
			t.Fatalf("event %d index=%d, want %d", i, index, want)
		}
	}
}

func TestTrajectoryRecorderNilIsNoOp(t *testing.T) {
	var r *TrajectoryRecorder
	r.Record("test", nil)
	r.Close()
}

func TestTrajectoryRecorderEmptyDirReturnsNil(t *testing.T) {
	r := NewTrajectoryRecorder("")
	if r != nil {
		t.Error("expected nil for empty dataDir")
	}
}

func TestTrajectoryRecorderDeleteEventsRewritesWhileOpen(t *testing.T) {
	dir := t.TempDir()
	r := NewTrajectoryRecorder(dir)
	if r == nil {
		t.Fatal("expected non-nil recorder")
	}
	r.Record("consolidate", map[string]interface{}{
		"job_id":              "job_a",
		"llm_conversation_id": "conv_learn_a",
	})
	r.Record("consolidate", map[string]interface{}{"job_id": "job_b"})

	removed := r.DeleteEvents(func(ev TrajectoryEvent) bool {
		return DetailString(ev.Detail, "job_id") == "job_a"
	})
	if len(removed) != 1 || removed[0] != "conv_learn_a" {
		t.Fatalf("transcript ids = %#v", removed)
	}

	events := ReadTrajectory(dir, 100)
	for _, ev := range events {
		if DetailString(ev.Detail, "job_id") == "job_a" {
			t.Fatalf("job_a still present: %+v", ev)
		}
	}
	r.Record("consolidate", map[string]interface{}{"job_id": "job_c"})
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events = ReadTrajectory(dir, 100)
	foundC := false
	for _, ev := range events {
		if DetailString(ev.Detail, "job_id") == "job_c" {
			foundC = true
		}
	}
	if !foundC {
		t.Fatal("append after DeleteEvents must still work")
	}
}
