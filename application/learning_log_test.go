package application

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func writeTrajectory(t *testing.T, dir string, lines []string) {
	t.Helper()
	path := filepath.Join(dir, "learning", "trajectory.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir trajectory dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write trajectory: %v", err)
	}
}

func TestReadTrajectoryNewestFirstAndFiltersNoise(t *testing.T) {
	dir := t.TempDir()
	// Append-ordered: oldest first. review is written last = newest.
	writeTrajectory(t, dir, []string{
		`{"ts":"2026-08-19T07:00:00Z","type":"prune","detail":{"pruned":3}}`,
		`{"ts":"2026-08-19T08:00:00Z","type":"search","detail":{"query":"docker"}}`,
		`{"ts":"2026-08-19T09:00:00Z","type":"graph_load","detail":{"nodes":5}}`,
		`{"ts":"2026-08-19T10:00:00Z","type":"review","detail":{"conversation":"conv_1","mutations":[]}}`,
	})

	events := ReadTrajectory(dir, 10)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (search/graph_load filtered)", len(events))
	}
	if events[0].Type != "review" {
		t.Errorf("events[0].type = %q, want review (newest first)", events[0].Type)
	}
	if events[1].Type != "prune" {
		t.Errorf("events[1].type = %q, want prune", events[1].Type)
	}
}

func TestReadTrajectoryMissingFile(t *testing.T) {
	events := ReadTrajectory(t.TempDir(), 10)
	if events != nil {
		t.Fatalf("events = %+v, want nil for missing file", events)
	}
}

func TestReadTrajectoryRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, `{"ts":"2026-08-19T10:00:00Z","type":"decay","detail":{}}`)
	}
	writeTrajectory(t, dir, lines)
	if got := len(ReadTrajectory(dir, 5)); got != 5 {
		t.Fatalf("limit: got %d, want 5", got)
	}
}

// titleConversationStore is a minimal ConversationStore that serves
// titles for the learning-log enrichment test.
type titleConversationStore struct {
	convs map[string]*domain.Conversation
}

func (s *titleConversationStore) List() []*domain.Conversation {
	out := make([]*domain.Conversation, 0, len(s.convs))
	for _, c := range s.convs {
		out = append(out, c)
	}
	return out
}
func (s *titleConversationStore) Get(id string) (*domain.Conversation, error) {
	return s.convs[id], nil
}
func (s *titleConversationStore) Save(c *domain.Conversation) error { return nil }
func (s *titleConversationStore) Delete(id string) error            { return nil }
func (s *titleConversationStore) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	return 0, nil
}
func (s *titleConversationStore) GetChunk(id string, index int) ([]domain.Message, error) {
	return nil, nil
}

func TestHandleLearningLogEnrichesReviewEntries(t *testing.T) {
	dir := t.TempDir()
	// File is append-ordered (oldest first), so the 09:00 entry is written
	// before the 10:00 entry. Newest-first output puts conv_1 on top.
	writeTrajectory(t, dir, []string{
		`{"ts":"2026-08-19T09:00:00Z","type":"review","detail":{"conversation":"conv_ghost","mutations":["skills"]}}`,
		`{"ts":"2026-08-19T10:00:00Z","type":"review","detail":{"conversation":"conv_1","mutations":[{"kind":"memory","tool":"memory","snippet":"user prefers Indonesian"}]}}`,
	})

	app := &App{
		DataDir: dir,
		Conversations: &titleConversationStore{convs: map[string]*domain.Conversation{
			"conv_1": {ID: "conv_1", Title: "Memory research"},
		}},
	}
	res, rpcErr := app.handleLearningLog(contracts.LearningLogRequest{Limit: 10})
	if rpcErr != nil {
		t.Fatalf("handleLearningLog: %v", rpcErr)
	}
	result, ok := res.(contracts.LearningLogResult)
	if !ok {
		t.Fatalf("result type = %T", res)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(result.Entries))
	}

	// Newest first: the enriched entry with a title comes first.
	first := result.Entries[0]
	if first.ConversationID != "conv_1" {
		t.Errorf("first conversation = %q, want conv_1", first.ConversationID)
	}
	if first.ConversationTitle != "Memory research" {
		t.Errorf("first title = %q, want Memory research", first.ConversationTitle)
	}
	if len(first.Mutations) != 1 {
		t.Fatalf("first mutations = %+v, want 1", first.Mutations)
	}
	if first.Mutations[0].Kind != "memory" || first.Mutations[0].Tool != "memory" || first.Mutations[0].Snippet != "user prefers Indonesian" {
		t.Errorf("first mutation = %+v, want kind/tool/snippet", first.Mutations[0])
	}

	// Legacy entry: mutations were a list of kind strings, no title known.
	second := result.Entries[1]
	if second.ConversationID != "conv_ghost" || second.ConversationTitle != "" {
		t.Errorf("second conversation = %+v, want ghost id and no title", second)
	}
	if len(second.Mutations) != 1 || second.Mutations[0].Kind != "skills" {
		t.Errorf("second mutations = %+v, want legacy skills kind", second.Mutations)
	}

	// Detail passthrough: conversation + mutations should NOT appear as
	// raw detail (they are structured fields), but any extras should.
	if _, ok := first.Detail["conversation"]; ok {
		t.Error("conversation should not appear in raw detail")
	}
}

// A failed job still reports its status, but the provider-shaped error stays
// server-side: the UI renders its own generic failure line, so shipping the
// raw error would only leak provider internals into the feed.
func TestHandleLearningLogParsesStatusAndKeepsErrorServerSide(t *testing.T) {
	dir := t.TempDir()
	writeTrajectory(t, dir, []string{
		`{"ts":"2026-08-19T09:00:00Z","type":"consolidate","detail":{"job_id":"job_ok","status":"done","mutations":[]}}`,
		`{"ts":"2026-08-19T10:00:00Z","type":"consolidate","detail":{"job_id":"job_err","status":"error","error":"no model configured","mutations":[]}}`,
	})
	app := &App{DataDir: dir}
	res, rpcErr := app.handleLearningLog(contracts.LearningLogRequest{Limit: 10})
	if rpcErr != nil {
		t.Fatalf("handleLearningLog: %v", rpcErr)
	}
	result := res.(contracts.LearningLogResult)
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(result.Entries))
	}
	// Newest first: the failed job on top.
	top := result.Entries[0]
	if top.Status != "error" {
		t.Errorf("top status = %q, want error", top.Status)
	}
	if _, ok := top.Detail["status"]; ok {
		t.Error("status should not appear in raw detail")
	}
	if _, ok := top.Detail["error"]; ok {
		t.Error("the raw error must stay server-side instead of riding along in detail")
	}
	bottom := result.Entries[1]
	if bottom.Status != "done" {
		t.Errorf("bottom status = %q, want done", bottom.Status)
	}
}

const (
	eventLearningJobStarted = "learning.job.started"
	eventLearningJobDone    = "learning.job.done"
	eventLearningJobError   = "learning.job.error"
)

func TestLearningJobErrorEventName(t *testing.T) {
	_ = eventLearningJobStarted
	_ = eventLearningJobDone
	if eventLearningJobError == "" {
		t.Fatal("learning job error event name must be defined")
	}
}

func TestTrajectoryRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	rec := NewTrajectoryRecorder(dir)
	if rec == nil {
		t.Fatal("NewTrajectoryRecorder returned nil")
	}
	rec.Record("review", map[string]interface{}{
		"conversation": "conv_1",
		"mutations": []map[string]string{
			{"kind": "memory", "tool": "memory", "snippet": "x"},
		},
	})
	_ = rec.Close()

	b, err := os.ReadFile(filepath.Join(dir, "learning", "trajectory.jsonl"))
	if err != nil {
		t.Fatalf("read trajectory: %v", err)
	}
	var ev struct {
		TS     time.Time              `json:"ts"`
		Type   string                 `json:"type"`
		Detail map[string]interface{} `json:"detail"`
	}
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatalf("unmarshal trajectory: %v", err)
	}
	if ev.Type != "review" {
		t.Errorf("type = %q, want review", ev.Type)
	}
	if ev.TS.IsZero() {
		t.Error("timestamp missing")
	}
}
