package application

import (
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func expDTO(id string, at time.Time) *domain.Experience {
	return &domain.Experience{ID: id, Timestamp: at, Goal: "goal " + id}
}

func TestExperienceListPaginatesNewestFirst(t *testing.T) {
	base := time.Now()
	store := &fakeExperienceStore{items: []*domain.Experience{
		expDTO("exp_old", base.Add(-3*time.Hour)),
		expDTO("exp_mid", base.Add(-2*time.Hour)),
		expDTO("exp_new", base.Add(-time.Hour)),
	}}
	app := &App{Experiences: store}

	res, rpcErr := app.handleExperienceList(contracts.ExperienceListRequest{Limit: 2})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	result := res.(contracts.ExperienceListResult)
	if result.Total != 3 {
		t.Fatalf("total = %d, want 3", result.Total)
	}
	if len(result.Experiences) != 2 {
		t.Fatalf("page size = %d, want 2", len(result.Experiences))
	}
	if result.Experiences[0].ID != "exp_new" || result.Experiences[1].ID != "exp_mid" {
		t.Fatalf("first page order = %s, %s; want exp_new, exp_mid", result.Experiences[0].ID, result.Experiences[1].ID)
	}

	res2, _ := app.handleExperienceList(contracts.ExperienceListRequest{Offset: 2, Limit: 2})
	page2 := res2.(contracts.ExperienceListResult)
	if len(page2.Experiences) != 1 || page2.Experiences[0].ID != "exp_old" {
		t.Fatalf("second page = %+v, want [exp_old]", page2.Experiences)
	}
	if page2.Total != 3 {
		t.Fatalf("second page total = %d, want 3", page2.Total)
	}
}

func TestExperienceDeleteRemovesEpisodeAndJobs(t *testing.T) {
	store := &fakeExperienceStore{items: []*domain.Experience{expDTO("exp_x", time.Now())}}
	jobs := &fakeLearningJobStore{items: map[string]*domain.LearningJob{
		"job_for_x": {ID: "job_for_x", ExperienceID: "exp_x", Status: domain.LearningJobQueued},
		"job_other": {ID: "job_other", ExperienceID: "exp_other", Status: domain.LearningJobQueued},
	}}
	app := &App{
		Experiences:  store,
		LearningJobs: jobs,
		Trajectory:   newTestTrajectory(t),
	}
	app.Trajectory.Record("consolidate", map[string]interface{}{"job_id": "job_for_x"})

	if _, rpcErr := app.handleExperienceDelete(contracts.ExperienceIDRequest{ID: "exp_x"}); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if len(store.items) != 0 {
		t.Fatalf("experience still present: %+v", store.items)
	}
	if _, ok := jobs.items["job_for_x"]; ok {
		t.Fatal("job referencing the deleted experience must be removed")
	}
	if _, ok := jobs.items["job_other"]; !ok {
		t.Fatal("unrelated job must survive")
	}
	if _, rpcErr := app.handleExperienceDelete(contracts.ExperienceIDRequest{ID: "exp_missing"}); rpcErr == nil {
		t.Fatal("missing experience must not delete silently")
	}
}

func TestMemoryDeleteRemovesRecordAndEdges(t *testing.T) {
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "mem_a", Body: "a", Status: domain.MemoryStatusLearned},
		{ID: "mem_b", Body: "b", Status: domain.MemoryStatusLearned},
	}}
	graph := &fakeLearningEdgeStore{items: []*domain.LearningEdge{
		{ID: "e1", SourceID: "mem_a", TargetID: "mem_b"},
		{ID: "e2", SourceID: "mem_b", TargetID: "mem_a"},
		{ID: "e3", SourceID: "skill_x", TargetID: "mem_a"},
	}}
	app := &App{
		MemoryRecords: records,
		edgeBuilder:   nil,
		graphService:  NewLearningGraphService(graph),
	}
	if _, rpcErr := app.handleMemoryDelete(contracts.MemoryIDRequest{ID: "mem_a"}); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if len(records.items) != 1 || records.items[0].ID != "mem_b" {
		t.Fatalf("records after delete = %+v", records.items)
	}
	if len(graph.items) != 1 || graph.items[0].ID != "e2" {
		t.Fatalf("edges after delete = %+v, want only e2 (mem_b only)", graph.items)
	}
	if _, rpcErr := app.handleMemoryDelete(contracts.MemoryIDRequest{ID: "mem_missing"}); rpcErr == nil {
		t.Fatal("missing memory must not delete silently")
	}
}

func TestLearningLogDeleteRemovesJobTrajectoryAndTranscript(t *testing.T) {
	dataDir := t.TempDir()
	rec := NewTrajectoryRecorder(dataDir)
	defer rec.Close()
	rec.Record("consolidate", map[string]interface{}{"job_id": "job_a", "llm_conversation_id": "conv_learn_a"})
	rec.Record("consolidate", map[string]interface{}{"job_id": "job_b"})

	convs := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_learn_a": {ID: "conv_learn_a", Type: domain.ConversationTypeBackground},
	}}
	jobs := &fakeLearningJobStore{items: map[string]*domain.LearningJob{
		"job_a": {ID: "job_a", Status: domain.LearningJobDone},
		"job_b": {ID: "job_b", Status: domain.LearningJobDone},
	}}
	app := &App{LearningJobs: jobs, Conversations: convs, Trajectory: rec, DataDir: dataDir}

	if _, rpcErr := app.handleLearningLogDelete(contracts.LearningLogDeleteRequest{JobID: "job_a"}); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if _, ok := jobs.items["job_a"]; ok {
		t.Fatal("deleted job must leave the jobs store")
	}
	if _, ok := jobs.items["job_b"]; !ok {
		t.Fatal("unrelated job must survive")
	}
	if _, err := convs.Get("conv_learn_a"); err == nil {
		t.Fatal("LLM transcript conversation must be deleted with its job")
	}
	events := ReadTrajectory(dataDir, 100)
	for _, ev := range events {
		if ev.Type == "consolidate" && detailString(ev.Detail, "job_id") == "job_a" {
			t.Fatalf("job_a trajectory event still present: %+v", ev)
		}
	}
	if _, rpcErr := app.handleLearningLogDelete(contracts.LearningLogDeleteRequest{JobID: "job_ghost"}); rpcErr == nil {
		t.Fatal("unknown job must be rejected")
	}
}

// fakeLearningEdgeStore is a minimal edge store for cleanup tests.
type fakeLearningEdgeStore struct {
	items []*domain.LearningEdge
}

func (f *fakeLearningEdgeStore) List() []*domain.LearningEdge { return f.items }
func (f *fakeLearningEdgeStore) Save(e *domain.LearningEdge) error {
	for i, existing := range f.items {
		if existing.ID == e.ID {
			f.items[i] = e
			return nil
		}
	}
	f.items = append(f.items, e)
	return nil
}
func (f *fakeLearningEdgeStore) Delete(id string) error {
	for i, e := range f.items {
		if e.ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return nil
}

func newTestTrajectory(t *testing.T) *TrajectoryRecorder {
	t.Helper()
	rec := NewTrajectoryRecorder(t.TempDir())
	t.Cleanup(func() { rec.Close() })
	return rec
}
