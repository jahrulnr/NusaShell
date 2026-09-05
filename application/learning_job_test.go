package application

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

type panicDocStore struct {
	t *testing.T
}

func (p *panicDocStore) Load() *domain.MemoryDocument { return &domain.MemoryDocument{} }
func (p *panicDocStore) Update(entries []domain.DocumentEntry) error {
	p.t.Fatal("consolidator must not write user.md or soul.md")
	return nil
}
func (p *panicDocStore) Replace(oldText, content string) error {
	p.t.Fatal("consolidator must not write user.md or soul.md")
	return nil
}
func (p *panicDocStore) Path() string { return "" }

func TestConsolidateJobDoesNotWriteProfileDocs(t *testing.T) {
	exp := &domain.Experience{
		ID:      "exp_1",
		Goal:    "remember I prefer dark mode",
		Signals: domain.ExperienceSignals{ExplicitTeaching: true},
	}
	app := &App{
		Experiences:   &fakeExperienceStore{items: []*domain.Experience{exp}},
		MemoryRecords: &fakeMemoryRecordStore{},
		User:          &panicDocStore{t: t},
		Agent:         &panicDocStore{t: t},
	}
	if _, _, err := app.consolidateJob(&domain.LearningJob{ID: "job_1", ExperienceID: "exp_1", Kind: domain.LearningJobConsolidate}); err != nil {
		t.Fatal(err)
	}
	if len(app.MemoryRecords.List()) != 1 {
		t.Fatalf("records=%d", len(app.MemoryRecords.List()))
	}
}

func TestConsolidateJobSetARecall(t *testing.T) {
	exp := &domain.Experience{
		ID:      "exp_a",
		Goal:    "I prefer Go for backend work",
		Signals: domain.ExperienceSignals{ExplicitTeaching: true},
	}
	records := &fakeMemoryRecordStore{}
	app := &App{
		Experiences:   &fakeExperienceStore{items: []*domain.Experience{exp}},
		MemoryRecords: records,
	}
	if _, _, err := app.consolidateJob(&domain.LearningJob{ID: "job_a", ExperienceID: "exp_a"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rec := range records.List() {
		if rec.Retrievable() && strings.Contains(rec.Body, "Go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Set A: preference not retrievable: %+v", records.List())
	}
}

func TestConsolidateJobSetDOneOffPackageManagerStaysEpisode(t *testing.T) {
	exp := &domain.Experience{
		ID:      "exp_d",
		Goal:    "install frontend deps",
		Actions: []domain.ExperienceAction{{Name: "exec", Digest: "pnpm install"}},
		Outcome: domain.ExperienceOutcome{Status: "success"},
	}
	records := &fakeMemoryRecordStore{}
	app := &App{
		Experiences:   &fakeExperienceStore{items: []*domain.Experience{exp}},
		MemoryRecords: records,
	}
	if _, _, err := app.consolidateJob(&domain.LearningJob{ID: "job_d", ExperienceID: "exp_d"}); err != nil {
		t.Fatal(err)
	}
	for _, rec := range records.List() {
		if rec.Type == domain.MemoryTypePreference {
			t.Fatalf("Set D: one-off pnpm became preference: %+v", rec)
		}
	}
	if len(records.List()) != 0 {
		t.Fatalf("Set D: expected no durable records, got %+v", records.List())
	}
}

func TestConsolidateJobSetCStrengthensDuplicate(t *testing.T) {
	exp1 := &domain.Experience{
		ID:          "exp_c1",
		Goal:        "use pnpm in this repo",
		Corrections: []domain.UserCorrection{{Type: "preference", Desired: "use pnpm in this repo", Explicit: true}},
		Signals:     domain.ExperienceSignals{UserCorrections: 1},
		Scope:       domain.ExperienceScope{Project: "app"},
	}
	exp2 := &domain.Experience{
		ID:          "exp_c2",
		Goal:        "use pnpm in this repo",
		Corrections: []domain.UserCorrection{{Type: "preference", Desired: "use pnpm in this repo", Explicit: true}},
		Signals:     domain.ExperienceSignals{UserCorrections: 1},
		Scope:       domain.ExperienceScope{Project: "app"},
	}
	records := &fakeMemoryRecordStore{}
	app := &App{
		Experiences:   &fakeExperienceStore{items: []*domain.Experience{exp1, exp2}},
		MemoryRecords: records,
	}
	if _, _, err := app.consolidateJob(&domain.LearningJob{ID: "job_c1", ExperienceID: "exp_c1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.consolidateJob(&domain.LearningJob{ID: "job_c2", ExperienceID: "exp_c2"}); err != nil {
		t.Fatal(err)
	}
	live := 0
	var kept *domain.MemoryRecord
	for _, rec := range records.List() {
		if rec.Retrievable() {
			live++
			kept = rec
		}
	}
	if live != 1 {
		t.Fatalf("Set C: want 1 record, got %d %+v", live, records.List())
	}
	if kept.EvidenceCount < 2 {
		t.Fatalf("Set C: evidence_count=%d, want >=2", kept.EvidenceCount)
	}
}

func TestEvaluateSkillJobNeverPromotes(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"learned-flow": {
			ID: "learned-flow", Origin: domain.SkillOriginLearned,
			Status: domain.SkillStatusExperimental, Version: 3, ActiveVersion: 3,
		},
	}}
	app := &App{Skills: skills}
	if err := app.evaluateSkillJob(&domain.LearningJob{SkillID: "learned-flow"}); err != nil {
		t.Fatal(err)
	}
	got, _ := skills.Get("learned-flow", string(domain.SkillOriginLearned))
	if got.Status == domain.SkillStatusTrusted {
		t.Fatal("evaluator must not set trusted")
	}
	if got.Status != domain.SkillStatusExperimental {
		t.Fatalf("no verifier must leave experimental, got %s", got.Status)
	}
}

func TestEvolveSkillJobStaysExperimentalAndRespectsRevisionCap(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{}}
	exp := &domain.Experience{
		ID:   "exp_e",
		Goal: "debug nginx config",
		Actions: []domain.ExperienceAction{
			{Name: "file_read"}, {Name: "grep"}, {Name: "exec"},
		},
	}
	app := &App{
		Skills:      skills,
		Experiences: &fakeExperienceStore{items: []*domain.Experience{exp}},
	}
	job := &domain.LearningJob{ExperienceID: "exp_e", Kind: domain.LearningJobEvolveSkill}
	for i := 0; i < domain.MaxSkillRevisions+3; i++ {
		if _, err := app.evolveSkillJob(job); err != nil {
			t.Fatalf("evolve %d: %v", i, err)
		}
	}
	var got *domain.Skill
	for _, s := range skills.List() {
		got = s
	}
	if got == nil {
		t.Fatal("expected a learned skill")
	}
	if got.Status != domain.SkillStatusExperimental {
		t.Fatalf("status=%s, want experimental", got.Status)
	}
	if got.Version > domain.MaxSkillRevisions {
		t.Fatalf("version=%d exceeds cap %d", got.Version, domain.MaxSkillRevisions)
	}
}

func TestEvolveSkillJobEmitsEvolveOp(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{}}
	exp := &domain.Experience{
		ID:   "exp_e2",
		Goal: "reload nginx",
		Actions: []domain.ExperienceAction{
			{Name: "file_read"}, {Name: "file_patch"}, {Name: "exec"},
		},
	}
	dir := t.TempDir()
	bus := NewBus()
	_, ch, unsub := bus.Subscribe()
	t.Cleanup(unsub)
	app := &App{
		Skills:      skills,
		Experiences: &fakeExperienceStore{items: []*domain.Experience{exp}},
		Bus:         bus,
		Trajectory:  NewTrajectoryRecorder(dir),
	}
	t.Cleanup(func() { _ = app.Trajectory.Close() })
	if _, err := app.evolveSkillJob(&domain.LearningJob{ExperienceID: "exp_e2", Kind: domain.LearningJobEvolveSkill}); err != nil {
		t.Fatal(err)
	}
	ev := waitBusEvent(t, ch, contracts.EventSkillUpdated)
	payload := skillEventPayload(t, ev)
	if payload["op"] != "evolve" {
		t.Fatalf("evolve event op=%v payload=%v", payload["op"], payload)
	}
	if payload["status"] != string(domain.SkillStatusExperimental) {
		t.Fatalf("status=%v", payload["status"])
	}
	raw := mustReadTrajectory(t, dir)
	if !strings.Contains(raw, `"type":"skill_evolve"`) {
		t.Fatalf("trajectory missing skill_evolve: %s", raw)
	}
	if strings.Contains(raw, `"type":"skill_promote"`) {
		t.Fatal("evolve must not record skill_promote")
	}
}

func TestHandleSkillsPromoteEmitsPromoteOp(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"learned-flow": {
			ID: "learned-flow", Origin: domain.SkillOriginLearned,
			Status: domain.SkillStatusExperimental, Version: 1, ActiveVersion: 1,
		},
	}}
	dir := t.TempDir()
	bus := NewBus()
	_, ch, unsub := bus.Subscribe()
	t.Cleanup(unsub)
	app := &App{Skills: skills, Bus: bus, Trajectory: NewTrajectoryRecorder(dir)}
	t.Cleanup(func() { _ = app.Trajectory.Close() })
	if _, err := app.handleSkillsPromote(contracts.SkillPromoteRequest{ID: "learned-flow"}); err != nil {
		t.Fatal(err)
	}
	ev := waitBusEvent(t, ch, contracts.EventSkillUpdated)
	payload := skillEventPayload(t, ev)
	if payload["op"] != "promote" {
		t.Fatalf("promote event op=%v payload=%v", payload["op"], payload)
	}
	if payload["status"] != string(domain.SkillStatusTrusted) {
		t.Fatalf("status=%v", payload["status"])
	}
	raw := mustReadTrajectory(t, dir)
	if !strings.Contains(raw, `"type":"skill_promote"`) {
		t.Fatalf("trajectory missing skill_promote: %s", raw)
	}
}

func skillEventPayload(t *testing.T, ev contracts.Event) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return payload
}

func waitBusEvent(t *testing.T, ch <-chan contracts.Event, typ string) contracts.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == typ {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", typ)
		}
	}
}

func mustReadTrajectory(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "learning", "trajectory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExtractExperienceOneOffPnpmDoesNotEnqueue(t *testing.T) {
	conv := &domain.Conversation{
		ID: "conv_pnpm",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "install frontend deps"},
			{Role: domain.RoleAssistant, Content: "running pnpm", ToolCalls: []domain.ToolCall{
				{Name: "exec", Status: domain.ToolOK, Args: `{"command":"pnpm install"}`},
			}},
		},
	}
	exp := ExtractExperience(conv, false)
	if domain.DecideLearningTrigger(exp, nil).Enqueue {
		t.Fatalf("one-off pnpm enqueued: %+v", exp)
	}
}

func TestRunLearningJobAdvancesCursorAfterSuccessfulConsolidation(t *testing.T) {
	source := &domain.Conversation{
		ID:                   "conv_cursor_success",
		LastReviewedMsgCount: 1,
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "first"},
			{ID: "m2", Role: domain.RoleAssistant, Content: "second"},
			{ID: "m3", Role: domain.RoleUser, Content: "third"},
			{ID: "m4", Role: domain.RoleAssistant, Content: "fourth"},
		},
	}
	conversations := &cloningConvStore{conv: source}
	jobs := &fakeLearningJobStore{items: map[string]*domain.LearningJob{
		"job_cursor_success": {
			ID:           "job_cursor_success",
			Kind:         domain.LearningJobConsolidate,
			ExperienceID: "exp_cursor_success",
		},
	}}
	var prompt string
	app := &App{
		Conversations: conversations,
		LearningJobs:  jobs,
		Experiences: &fakeExperienceStore{items: []*domain.Experience{{
			ID:             "exp_cursor_success",
			ConversationID: source.ID,
		}}},
		MemoryRecords: &fakeMemoryRecordStore{},
		learningTurn: func(_ context.Context, _ AgentKind, _, gotPrompt string) (string, string, error) {
			prompt = gotPrompt
			return "[]", "conv_learning_success", nil
		},
	}

	app.runLearningJob("job_cursor_success")

	got, err := conversations.Get(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastReviewedMsgCount != len(source.Messages) {
		t.Fatalf("cursor = %d, want %d", got.LastReviewedMsgCount, len(source.Messages))
	}
	if !strings.Contains(prompt, "message_range: [1,4)") {
		t.Fatalf("prompt = %q, want captured range [1,4)", prompt)
	}
	if len(got.Messages) != len(source.Messages) {
		t.Fatalf("messages = %d, want %d", len(got.Messages), len(source.Messages))
	}
	for i, message := range got.Messages {
		if message.ID != source.Messages[i].ID {
			t.Fatalf("message %d id = %q, want %q", i, message.ID, source.Messages[i].ID)
		}
	}
	if job := jobs.items["job_cursor_success"]; job.Status != domain.LearningJobDone {
		t.Fatalf("job status = %q, want done", job.Status)
	}
}

func TestAdvanceLearningCursorClampsInvalidPersistedMarker(t *testing.T) {
	for _, marker := range []int{-1, 9} {
		t.Run(fmt.Sprintf("marker_%d", marker), func(t *testing.T) {
			source := &domain.Conversation{
				ID:                   "conv_cursor_clamp",
				LastReviewedMsgCount: marker,
				Messages:             []domain.Message{{ID: "m1"}, {ID: "m2"}, {ID: "m3"}},
			}
			conversations := &cloningConvStore{conv: source}
			app := &App{Conversations: conversations}
			if err := app.advanceLearningCursor(&learningSource{
				conversationID:   source.ID,
				messageEnd:       3,
				boundaryCaptured: true,
			}); err != nil {
				t.Fatal(err)
			}
			got, err := conversations.Get(source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.LastReviewedMsgCount != 3 {
				t.Fatalf("cursor = %d, want 3", got.LastReviewedMsgCount)
			}
		})
	}
}

func TestRunLearningJobDoesNotAdvanceCursorOnLearningFailure(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		turnErr    error
		wantStatus string
	}{
		{name: "provider failure", response: "", turnErr: errStubLearningTurn, wantStatus: domain.LearningJobDone},
		{name: "parse failure", response: "not json", wantStatus: domain.LearningJobDone},
		{
			name:       "applied operation failure",
			response:   `[{"kind":"memory.upsert","payload":{}}]`,
			wantStatus: domain.LearningJobError,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := &domain.Conversation{
				ID:                   "conv_cursor_failure",
				LastReviewedMsgCount: 1,
				Messages: []domain.Message{
					{ID: "m1", Role: domain.RoleUser},
					{ID: "m2", Role: domain.RoleAssistant},
					{ID: "m3", Role: domain.RoleUser},
				},
			}
			conversations := &cloningConvStore{conv: source}
			jobs := &fakeLearningJobStore{items: map[string]*domain.LearningJob{
				"job_cursor_failure": {
					ID:           "job_cursor_failure",
					Kind:         domain.LearningJobConsolidate,
					ExperienceID: "exp_cursor_failure",
				},
			}}
			app := &App{
				Conversations: conversations,
				LearningJobs:  jobs,
				Experiences: &fakeExperienceStore{items: []*domain.Experience{{
					ID:             "exp_cursor_failure",
					ConversationID: source.ID,
					Goal:           "remember this preference",
					Signals:        domain.ExperienceSignals{ExplicitTeaching: true},
				}}},
				MemoryRecords: &fakeMemoryRecordStore{},
				learningTurn: func(_ context.Context, _ AgentKind, _, _ string) (string, string, error) {
					return tc.response, "conv_learning_failure", tc.turnErr
				},
			}

			app.runLearningJob("job_cursor_failure")

			got, err := conversations.Get(source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.LastReviewedMsgCount != source.LastReviewedMsgCount {
				t.Fatalf("cursor = %d, want unchanged %d", got.LastReviewedMsgCount, source.LastReviewedMsgCount)
			}
			if job := jobs.items["job_cursor_failure"]; job.Status != tc.wantStatus {
				t.Fatalf("job status = %q, want %q", job.Status, tc.wantStatus)
			}
		})
	}
}

func TestRunLearningJobPreservesMessagesArrivingAfterCapture(t *testing.T) {
	source := &domain.Conversation{
		ID: "conv_cursor_newer",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser},
			{ID: "m2", Role: domain.RoleAssistant},
		},
	}
	conversations := &cloningConvStore{conv: source}
	jobs := &fakeLearningJobStore{items: map[string]*domain.LearningJob{
		"job_cursor_newer": {
			ID:           "job_cursor_newer",
			Kind:         domain.LearningJobConsolidate,
			ExperienceID: "exp_cursor_newer",
		},
	}}
	started := make(chan struct{})
	release := make(chan struct{})
	app := &App{
		Conversations: conversations,
		LearningJobs:  jobs,
		Experiences: &fakeExperienceStore{items: []*domain.Experience{{
			ID:             "exp_cursor_newer",
			ConversationID: source.ID,
		}}},
		MemoryRecords: &fakeMemoryRecordStore{},
		learningTurn: func(_ context.Context, _ AgentKind, _, prompt string) (string, string, error) {
			if !strings.Contains(prompt, "message_range: [0,2)") {
				t.Errorf("prompt = %q, want captured range [0,2)", prompt)
			}
			close(started)
			<-release
			return "[]", "conv_learning_newer", nil
		},
	}
	done := make(chan struct{})
	go func() {
		app.runLearningJob("job_cursor_newer")
		close(done)
	}()
	<-started

	conversations.mu.Lock()
	conversations.conv.Messages = append(conversations.conv.Messages, domain.Message{
		ID: "m3", Role: domain.RoleUser, Content: "arrived while learning",
	})
	conversations.mu.Unlock()
	close(release)
	<-done

	got, err := conversations.Get(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastReviewedMsgCount != 2 {
		t.Fatalf("cursor = %d, want captured boundary 2", got.LastReviewedMsgCount)
	}
	if len(got.Messages) != 3 || got.Messages[2].ID != "m3" {
		t.Fatalf("messages = %+v, want newer message preserved", got.Messages)
	}
}

func TestAdvanceLearningCursorIsMonotonicForConcurrentCompletion(t *testing.T) {
	source := &domain.Conversation{
		ID: "conv_cursor_monotonic",
		Messages: []domain.Message{
			{ID: "m1"}, {ID: "m2"}, {ID: "m3"}, {ID: "m4"},
			{ID: "m5"}, {ID: "m6"}, {ID: "m7"}, {ID: "m8"},
		},
	}
	conversations := &cloningConvStore{conv: source}
	app := &App{Conversations: conversations}

	var wg sync.WaitGroup
	for _, end := range []int{3, 8} {
		wg.Add(1)
		go func(end int) {
			defer wg.Done()
			app.advanceLearningCursor(&learningSource{
				conversationID:   source.ID,
				messageEnd:       end,
				boundaryCaptured: true,
			})
		}(end)
	}
	wg.Wait()

	got, err := conversations.Get(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastReviewedMsgCount != 8 {
		t.Fatalf("cursor = %d, want 8 after stale completion", got.LastReviewedMsgCount)
	}
}

func TestRunLearningJobAdvancesCursorAfterSuccessfulEvolution(t *testing.T) {
	source := &domain.Conversation{
		ID: "conv_cursor_evolve",
		Messages: []domain.Message{
			{ID: "m1"}, {ID: "m2"}, {ID: "m3"},
		},
	}
	conversations := &cloningConvStore{conv: source}
	jobs := &fakeLearningJobStore{items: map[string]*domain.LearningJob{
		"job_cursor_evolve": {
			ID:           "job_cursor_evolve",
			Kind:         domain.LearningJobEvolveSkill,
			ExperienceID: "exp_cursor_evolve",
		},
	}}
	app := &App{
		Conversations: conversations,
		LearningJobs:  jobs,
		Experiences: &fakeExperienceStore{items: []*domain.Experience{{
			ID:             "exp_cursor_evolve",
			ConversationID: source.ID,
			Goal:           "debug nginx",
			Actions: []domain.ExperienceAction{
				{Name: "file_read"}, {Name: "grep"}, {Name: "exec"},
			},
		}}},
		Skills:       &fakeSkillStore{items: map[string]*domain.Skill{}},
		learningTurn: learningTurnStub(t, AgentLearner, `{"kind":"skill.create","name":"debug-nginx","purpose":"debug nginx","trigger":"nginx fails","steps":"1. inspect logs"}`, "conv_learning_evolve"),
	}

	app.runLearningJob("job_cursor_evolve")

	got, err := conversations.Get(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastReviewedMsgCount != len(source.Messages) {
		t.Fatalf("cursor = %d, want %d", got.LastReviewedMsgCount, len(source.Messages))
	}
	if job := jobs.items["job_cursor_evolve"]; job.Status != domain.LearningJobDone {
		t.Fatalf("job status = %q, want done", job.Status)
	}
}
