package learn

import (
	"fmt"
	"testing"
	"time"

	"nusashell/domain"
)

type retentionExperiences struct{ items map[string]*domain.Experience }

func (s *retentionExperiences) List() []*domain.Experience {
	out := make([]*domain.Experience, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out
}
func (s *retentionExperiences) Get(id string) (*domain.Experience, error) {
	v := s.items[id]
	if v == nil {
		return nil, fmt.Errorf("missing")
	}
	return v, nil
}
func (s *retentionExperiences) Save(v *domain.Experience) error { s.items[v.ID] = v; return nil }
func (s *retentionExperiences) ListByConversation(id string) []*domain.Experience {
	var out []*domain.Experience
	for _, v := range s.items {
		if v.ConversationID == id {
			out = append(out, v)
		}
	}
	return out
}
func (s *retentionExperiences) Delete(id string) error { delete(s.items, id); return nil }

type retentionJobs struct {
	items map[string]*domain.LearningJob
}

func (s *retentionJobs) List() []*domain.LearningJob {
	out := make([]*domain.LearningJob, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out
}
func (s *retentionJobs) Get(id string) (*domain.LearningJob, error) { return s.items[id], nil }
func (s *retentionJobs) Save(v *domain.LearningJob) error           { s.items[v.ID] = v; return nil }
func (s *retentionJobs) Delete(id string) error                     { delete(s.items, id); return nil }

type retentionOps struct {
	items map[string]*domain.LearningOperation
}

func (s *retentionOps) List() []*domain.LearningOperation {
	out := make([]*domain.LearningOperation, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out
}
func (s *retentionOps) Delete(id string) error { delete(s.items, id); return nil }

type retentionConversations struct{ deleted map[string]bool }

func (s *retentionConversations) List() []*domain.Conversation             { return nil }
func (s *retentionConversations) Get(string) (*domain.Conversation, error) { return nil, nil }
func (s *retentionConversations) Delete(id string) error                   { s.deleted[id] = true; return nil }

// TestActiveLearningJobIsCoalescedPerConversation pins the in-memory
// tracking that replaced the hot-path Jobs.List + Experiences.Get scan:
// only jobs this process queued block a new trigger, the mark clears when
// the run exits, and stale persisted rows can never wedge a conversation.
func TestActiveLearningJobIsCoalescedPerConversation(t *testing.T) {
	experiences := &retentionExperiences{items: map[string]*domain.Experience{}}
	jobs := &retentionJobs{items: map[string]*domain.LearningJob{
		// Leftover from a previous process: it can never run again (startup
		// recovery expires it past the TTL), so it must not block.
		"stale_a": {ID: "stale_a", Kind: domain.LearningJobLearner, Status: domain.LearningJobRunning, ExperienceID: "exp_gone"},
	}}
	var run func()
	s := New(Deps{
		Experiences: experiences,
		Jobs:        jobs,
		Go:          func(_ string, fn func()) { run = fn },
	})
	if s.hasActiveLearningJob("conv_a") {
		t.Fatal("a persisted job row from a previous process must not coalesce a new trigger")
	}

	conv := &domain.Conversation{ID: "conv_a"}
	for i := 0; i < domain.DefaultLearnerNudgeInterval; i++ {
		conv.Messages = append(conv.Messages, domain.Message{Role: domain.RoleUser, Content: fmt.Sprintf("turn %d", i)})
	}
	s.RecordExperience(conv, false)
	if run == nil {
		t.Fatal("learner trigger should have queued a job")
	}
	if len(jobs.items) != 2 {
		t.Fatalf("queued jobs = %d, want 2 (stale row + new job)", len(jobs.items))
	}
	if !s.hasActiveLearningJob("conv_a") {
		t.Fatal("a queued learner must mark its conversation active")
	}
	if s.hasActiveLearningJob("conv_b") {
		t.Fatal("an unrelated conversation must not be blocked")
	}

	// A second turn end while the first job is still queued coalesces —
	// without touching the job store.
	s.RecordExperience(conv, false)
	if len(jobs.items) != 2 {
		t.Fatalf("coalesced trigger queued a second job: %+v", jobs.items)
	}

	// The entry clears when the run exits — here the job fails (no records
	// store), and the conversation must still unblock.
	run()
	if s.hasActiveLearningJob("conv_a") {
		t.Fatal("an exited learner must unblock its conversation")
	}
}

// TestActiveLearningJobClearsWhenJobVanishes covers a queued job that was
// deleted before its goroutine ran: the tracked entry must still clear.
func TestActiveLearningJobClearsWhenJobVanishes(t *testing.T) {
	jobs := &retentionJobs{items: map[string]*domain.LearningJob{}}
	s := New(Deps{Jobs: jobs})
	s.recordMu.Lock()
	s.activeJobs = map[string]string{"job_gone": "conv_a"}
	s.recordMu.Unlock()

	if !s.hasActiveLearningJob("conv_a") {
		t.Fatal("tracked job must mark its conversation active")
	}
	s.RunLearningJob("job_gone") // not in the store -> exits immediately
	if s.hasActiveLearningJob("conv_a") {
		t.Fatal("a job that vanished before running must still unblock its conversation")
	}
}

func TestPruneGrowthOnceAppliesBalancedRetentionAndProtectsActiveWork(t *testing.T) {
	now := time.Now()
	old := now.Add(-200 * 24 * time.Hour)
	recent := now.Add(-10 * 24 * time.Hour)
	experiences := &retentionExperiences{items: map[string]*domain.Experience{
		"exp_old":    {ID: "exp_old", Timestamp: old},
		"exp_active": {ID: "exp_active", Timestamp: old},
		"exp_recent": {ID: "exp_recent", Timestamp: recent},
	}}
	jobs := &retentionJobs{items: map[string]*domain.LearningJob{
		"job_old":    {ID: "job_old", Status: domain.LearningJobDone, CreatedAt: old, FinishedAt: &old, LLMConversationID: "conv_learning_old"},
		"job_active": {ID: "job_active", Status: domain.LearningJobRunning, CreatedAt: old, ExperienceID: "exp_active"},
		"job_recent": {ID: "job_recent", Status: domain.LearningJobDone, CreatedAt: recent, FinishedAt: &recent},
	}}
	operations := &retentionOps{items: map[string]*domain.LearningOperation{
		"op_old":      {ID: "op_old", Status: domain.LearningOpAccepted, CreatedAt: old},
		"op_proposed": {ID: "op_proposed", Status: domain.LearningOpProposed, CreatedAt: old},
		"op_recent":   {ID: "op_recent", Status: domain.LearningOpAccepted, CreatedAt: recent},
	}}
	conversations := &retentionConversations{deleted: map[string]bool{}}
	s := New(Deps{Experiences: experiences, Jobs: jobs, Operations: operations, Conversations: conversations})
	s.PruneGrowthOnce()

	if jobs.items["job_old"] != nil || jobs.items["job_active"] == nil || jobs.items["job_recent"] == nil {
		t.Fatalf("jobs after retention: %+v", jobs.items)
	}
	if experiences.items["exp_old"] != nil || experiences.items["exp_active"] == nil || experiences.items["exp_recent"] == nil {
		t.Fatalf("experiences after retention: %+v", experiences.items)
	}
	if operations.items["op_old"] != nil || operations.items["op_proposed"] == nil || operations.items["op_recent"] == nil {
		t.Fatalf("operations after retention: %+v", operations.items)
	}
	if !conversations.deleted["conv_learning_old"] {
		t.Fatal("expired learning transcript was not removed")
	}
}
