package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

// errStubLearningTurn is the failure a learning turn reports when no model is
// reachable.
var errStubLearningTurn = errors.New("no learning model available")

// fakeLearningJobStore is an in-memory LearningJobStore. A missing id returns
// (nil, nil) so callers take the same "job not found" path as production.
type fakeLearningJobStore struct {
	mu    sync.Mutex
	items map[string]*domain.LearningJob
}

func (s *fakeLearningJobStore) List() []*domain.LearningJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*domain.LearningJob, 0, len(s.items))
	for _, j := range s.items {
		out = append(out, j)
	}
	return out
}

func (s *fakeLearningJobStore) Get(id string) (*domain.LearningJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.items[id], nil
}

func (s *fakeLearningJobStore) Save(j *domain.LearningJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]*domain.LearningJob{}
	}
	s.items[j.ID] = j
	return nil
}

// learningTurnStub stands in for the provider-backed learning turn so job
// plumbing is testable without wiring a provider. It asserts the turn is
// routed to the expected agent kind and receives a non-empty prompt.
func learningTurnStub(t *testing.T, wantKind AgentKind, text, convID string) func(context.Context, AgentKind, string, string) (string, string, error) {
	t.Helper()
	return func(_ context.Context, kind AgentKind, _, prompt string) (string, string, error) {
		if kind != wantKind {
			t.Errorf("learning turn kind = %q, want %q", kind, wantKind)
		}
		if strings.TrimSpace(prompt) == "" {
			t.Error("learning turn prompt must not be empty")
		}
		return text, convID, nil
	}
}

// readTrajectoryDetail returns the detail map of the newest trajectory event
// whose type matches wantType.
func readTrajectoryDetail(t *testing.T, dir, wantType string) map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "learning", "trajectory.jsonl"))
	if err != nil {
		t.Fatalf("read trajectory: %v", err)
	}
	var found map[string]interface{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e TrajectoryEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Type == wantType {
			found = e.Detail
		}
	}
	if found == nil {
		t.Fatalf("no %q event in trajectory:\n%s", wantType, b)
	}
	return found
}

// A consolidation job must hand back the id of the conversation that holds
// its LLM transcript. Without it the log has nothing to open, which is
// exactly how the details button became a control that did nothing.
func TestConsolidateJobReturnsLLMConversationID(t *testing.T) {
	exp := &domain.Experience{
		ID:      "exp_llm_1",
		Goal:    "remember I prefer dark mode",
		Signals: domain.ExperienceSignals{ExplicitTeaching: true},
	}
	app := &App{
		Experiences:   &fakeExperienceStore{items: []*domain.Experience{exp}},
		MemoryRecords: &fakeMemoryRecordStore{},
		learningTurn: learningTurnStub(t, AgentMemoryConsolidator,
			`[{"kind":"memory.upsert","payload":{"body":"prefers dark mode","type":"preference"}}]`, "conv_llm_1"),
	}
	_, convID, err := app.consolidateJob(&domain.LearningJob{ID: "job_llm_1", ExperienceID: "exp_llm_1", Kind: domain.LearningJobConsolidate})
	if err != nil {
		t.Fatalf("consolidateJob: %v", err)
	}
	if convID != "conv_llm_1" {
		t.Fatalf("conversation id = %q, want conv_llm_1", convID)
	}
}

// A failed learning turn must still yield the transcript id: a job that
// produced nothing is precisely the case a user wants to inspect.
func TestConsolidateJobKeepsConversationIDWhenTurnFails(t *testing.T) {
	exp := &domain.Experience{
		ID:      "exp_llm_x",
		Goal:    "remember I prefer dark mode",
		Signals: domain.ExperienceSignals{ExplicitTeaching: true},
	}
	app := &App{
		Experiences:   &fakeExperienceStore{items: []*domain.Experience{exp}},
		MemoryRecords: &fakeMemoryRecordStore{},
		learningTurn: func(_ context.Context, _ AgentKind, _, _ string) (string, string, error) {
			return "", "conv_llm_err", errStubLearningTurn
		},
	}
	_, convID, err := app.consolidateJob(&domain.LearningJob{ID: "job_llm_x", ExperienceID: "exp_llm_x", Kind: domain.LearningJobConsolidate})
	if err != nil {
		t.Fatalf("a failed turn must fall back, not fail the job: %v", err)
	}
	if convID != "conv_llm_err" {
		t.Fatalf("conversation id = %q, want conv_llm_err", convID)
	}
}

// The trajectory entry is the feed row, so it must carry both the job id and
// the transcript id, plus the outcome (status + what was saved).
func TestRunLearningJobRecordsLLMConversationInTrajectory(t *testing.T) {
	dir := t.TempDir()
	exp := &domain.Experience{
		ID:      "exp_llm_2",
		Goal:    "always run gofmt before commit",
		Signals: domain.ExperienceSignals{ExplicitTeaching: true},
	}
	jobs := &fakeLearningJobStore{items: map[string]*domain.LearningJob{
		"job_llm_2": {ID: "job_llm_2", ExperienceID: "exp_llm_2", Kind: domain.LearningJobConsolidate},
	}}
	app := &App{
		DataDir:       dir,
		Trajectory:    NewTrajectoryRecorder(dir),
		LearningJobs:  jobs,
		Experiences:   &fakeExperienceStore{items: []*domain.Experience{exp}},
		MemoryRecords: &fakeMemoryRecordStore{},
		learningTurn: learningTurnStub(t, AgentMemoryConsolidator,
			`[{"kind":"memory.upsert","payload":{"body":"run gofmt before commit","type":"constraint"}}]`, "conv_llm_2"),
	}
	app.runLearningJob("job_llm_2")
	if err := app.Trajectory.Close(); err != nil {
		t.Fatalf("close trajectory: %v", err)
	}

	detail := readTrajectoryDetail(t, dir, "consolidate")
	if detail["job_id"] != "job_llm_2" {
		t.Errorf("job_id = %v, want job_llm_2", detail["job_id"])
	}
	if detail["llm_conversation_id"] != "conv_llm_2" {
		t.Errorf("llm_conversation_id = %v, want conv_llm_2", detail["llm_conversation_id"])
	}
	if detail["status"] != string(domain.LearningJobDone) {
		t.Errorf("status = %v, want done", detail["status"])
	}
	mutations, ok := detail["mutations"].([]interface{})
	if !ok || len(mutations) == 0 {
		t.Fatalf("mutations = %v, want the applied operation listed", detail["mutations"])
	}
	first, _ := mutations[0].(map[string]interface{})
	snippet, _ := first["snippet"].(string)
	if !strings.Contains(strings.ToLower(snippet), "gofmt") {
		t.Errorf("mutation snippet = %v, want the saved body", first["snippet"])
	}
}

// The wire contract: llm_conversation_id is a structured column on the feed
// entry, kept separate from conversation_id (the conversation the job learned
// from) and never duplicated into the raw detail blob.
func TestHandleLearningLogLiftsLLMConversationID(t *testing.T) {
	dir := t.TempDir()
	writeTrajectory(t, dir, []string{
		`{"ts":"2026-08-19T10:00:00Z","type":"consolidate","detail":{"job_id":"job_1","llm_conversation_id":"conv_llm_3","status":"done","mutations":[{"kind":"memory.upsert","snippet":"prefers dark mode"}]}}`,
	})
	app := &App{DataDir: dir}
	res, rpcErr := app.handleLearningLog(contracts.LearningLogRequest{Limit: 10})
	if rpcErr != nil {
		t.Fatalf("handleLearningLog: %v", rpcErr)
	}
	result, ok := res.(contracts.LearningLogResult)
	if !ok {
		t.Fatalf("result type = %T", res)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(result.Entries))
	}
	entry := result.Entries[0]
	if entry.LLMConversationID != "conv_llm_3" {
		t.Errorf("llm conversation = %q, want conv_llm_3", entry.LLMConversationID)
	}
	if entry.Status != "done" {
		t.Errorf("status = %q, want done", entry.Status)
	}
	if len(entry.Mutations) != 1 {
		t.Fatalf("mutations = %+v, want 1", entry.Mutations)
	}
	if entry.ConversationID != "" {
		t.Errorf("conversation_id = %q, want empty (no source conversation on a job event)", entry.ConversationID)
	}
	if _, ok := entry.Detail["llm_conversation_id"]; ok {
		t.Error("llm_conversation_id must not leak into raw detail")
	}
	if _, ok := entry.Detail["job_id"]; !ok {
		t.Error("job_id should remain available as raw detail")
	}
}

// Learning jobs persist background transcripts; every other headless run is
// an automation transcript. Both stay out of the Agent room list, and the
// learning ones are the ones the Learning log opens.
func TestHeadlessConversationTypeByAgentKind(t *testing.T) {
	for _, kind := range []AgentKind{AgentMemoryConsolidator, AgentSkillEvolver, AgentSkillEvaluator} {
		if got := headlessConversationType(kind); got != domain.ConversationTypeBackground {
			t.Errorf("headlessConversationType(%q) = %q, want %q", kind, got, domain.ConversationTypeBackground)
		}
	}
	for _, kind := range []AgentKind{AgentAutomation, AgentDelegate} {
		if got := headlessConversationType(kind); got != domain.ConversationTypeAutomation {
			t.Errorf("headlessConversationType(%q) = %q, want %q", kind, got, domain.ConversationTypeAutomation)
		}
	}
	if title := headlessTurnTitle(AgentMemoryConsolidator, "anything"); !strings.Contains(title, "learning") {
		t.Errorf("learning turn title = %q, want a readable learning label", title)
	}
}
