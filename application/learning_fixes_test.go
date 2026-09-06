package application

import (
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func TestPrepareConsolidationOpNearDuplicateMerges(t *testing.T) {
	created := time.Now().Add(-24 * time.Hour)
	base := &domain.MemoryRecord{
		ID:                    "mem_base",
		Type:                  domain.MemoryTypePreference,
		Body:                  "Untuk integrasi Cursor di 9router, yang dibutuhkan adalah versi IDE (state.vscdb), bukan CLI",
		Scope:                 domain.MemoryScope{Level: domain.MemoryScopeUser},
		Status:                domain.MemoryStatusLearned,
		EvidenceCount:         2,
		SupportingExperiences: []string{"exp_old_1", "exp_old_2"},
		CreatedAt:             created,
	}
	store := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{base}}
	svc := NewMemoryService(store, nil)
	app := &App{MemoryRecords: store}

	op := &domain.LearningOperation{
		Kind:     domain.OpMemoryUpsert,
		Actor:    domain.ActorLearner,
		JobID:    "job_near",
		Evidence: []string{"exp_new_1", "exp_new_2"},
		Payload: map[string]any{
			"body":  "Untuk integrasi Cursor di 9router: butuh versi IDE (state.vscdb) bukan CLI, dan auto-import digate local only",
			"type":  domain.MemoryTypePreference,
			"scope": domain.MemoryScopeUser,
		},
	}
	app.prepareConsolidationOp(op)
	if op.Kind != domain.OpMemoryUpsert {
		t.Fatalf("near-duplicate should stay upsert (merge by id), got kind=%s", op.Kind)
	}
	if op.Payload["id"] != "mem_base" {
		t.Fatalf("near-duplicate must target the existing record, got id=%v", op.Payload["id"])
	}
	if err := svc.Apply(op); err != nil {
		t.Fatal(err)
	}
	list := store.List()
	if len(list) != 1 {
		t.Fatalf("records = %d, want 1 (merged, not duplicated)", len(list))
	}
	rec := list[0]
	if rec.EvidenceCount != 4 {
		t.Fatalf("evidence_count = %d, want 4 (2 old + 2 new)", rec.EvidenceCount)
	}
	if !rec.CreatedAt.Equal(created) {
		t.Fatalf("created_at must survive the merge, got %v", rec.CreatedAt)
	}
	if !strings.Contains(rec.Body, "local only") {
		t.Fatalf("new information must be appended, body=%q", rec.Body)
	}
}

func TestPrepareConsolidationOpExactDuplicateStrengthens(t *testing.T) {
	base := &domain.MemoryRecord{
		ID:            "mem_exact",
		Type:          domain.MemoryTypePreference,
		Body:          "User prefers Go for backend work",
		Scope:         domain.MemoryScope{Level: domain.MemoryScopeUser},
		Status:        domain.MemoryStatusLearned,
		EvidenceCount: 1,
	}
	store := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{base}}
	app := &App{MemoryRecords: store}
	op := &domain.LearningOperation{
		Kind:     domain.OpMemoryUpsert,
		Actor:    domain.ActorLearner,
		Evidence: []string{"exp_again"},
		Payload:  map[string]any{"body": "User prefers Go for backend work", "type": domain.MemoryTypePreference},
	}
	app.prepareConsolidationOp(op)
	if op.Kind != domain.OpMemoryStrengthen {
		t.Fatalf("identical body should strengthen, got kind=%s", op.Kind)
	}
	if op.Payload["id"] != "mem_exact" {
		t.Fatalf("target id = %v", op.Payload["id"])
	}
}

func TestTeachingOpsNeverEmitsRawUserText(t *testing.T) {
	exp := &domain.Experience{
		ID:   "exp_raw",
		Goal: "sebelum push, perbaiki ini: di tab Experience itemnya ascending",
		Corrections: []domain.UserCorrection{{
			Type: "approach", UserSaid: "kalo tambah 1 hydration tool lagi? udah ada file_list kan ?", Explicit: true,
		}},
		Signals: domain.ExperienceSignals{ExplicitTeaching: true, UserCorrections: 1},
	}
	if ops := teachingOps(exp, "job_raw"); len(ops) != 0 {
		t.Fatalf("deterministic fallback emitted %d ops from raw user text: %+v", len(ops), ops)
	}
	// The Desired path stays available once extraction populates it.
	exp2 := &domain.Experience{
		ID:   "exp_desired",
		Goal: "change the memory tab",
		Corrections: []domain.UserCorrection{{
			Type: "preference", Desired: "User prefers newest-first ordering in the Experience tab", Explicit: true,
		}},
	}
	if ops := teachingOps(exp2, "job_desired"); len(ops) != 1 {
		t.Fatalf("distilled Desired correction should produce one op, got %d", len(ops))
	}
}

func TestApplyConsolidationOpsRejectsNonDurable(t *testing.T) {
	store := &fakeMemoryRecordStore{}
	svc := NewMemoryService(store, nil)
	exp := &domain.Experience{ID: "exp_gate", Goal: "sebelum push, perbaiki tab Experience ascending"}
	op := domain.LearningOperation{
		Kind:     domain.OpMemoryUpsert,
		Actor:    domain.ActorLearner,
		JobID:    "job_gate",
		Evidence: []string{"exp_gate"},
		Payload:  map[string]any{"body": "sebelum push, perbaiki tab Experience ascending", "type": domain.MemoryTypePreference},
	}
	app := &App{MemoryRecords: store}
	ops, _, err, _ := app.applyConsolidationOps([]domain.LearningOperation{op}, "", true, svc, exp)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Status != domain.LearningOpRejected {
		t.Fatalf("raw echo op must be rejected, got %+v", ops)
	}
	if len(store.List()) != 0 {
		t.Fatalf("rejected op must not write a record: %+v", store.List())
	}
}

func TestApplyConsolidationOpsSupersedeThenUpsert(t *testing.T) {
	old := &domain.MemoryRecord{
		ID:     "mem_phantom",
		Type:   domain.MemoryTypeFact,
		Body:   "file_patch can roll back a phantom hunk",
		Status: domain.MemoryStatusLearned,
		Scope:  domain.MemoryScope{Level: domain.MemoryScopeUser},
	}
	store := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{old}}
	svc := NewMemoryService(store, nil)
	app := &App{MemoryRecords: store}
	ops := []domain.LearningOperation{
		{
			Kind:     domain.OpMemoryContradict,
			Actor:    domain.ActorLearner,
			JobID:    "job_sup",
			TargetID: "mem_phantom",
			Reason:   "learner supersede",
		},
		{
			Kind:     domain.OpMemoryUpsert,
			Actor:    domain.ActorLearner,
			JobID:    "job_sup",
			Evidence: []string{"exp_sup"},
			Payload: map[string]any{
				"body": "file_patch cannot roll back a phantom hunk; recover from git or rewrite",
				"type": domain.MemoryTypeFact,
			},
		},
	}
	applied, _, err, _ := app.applyConsolidationOps(ops, "conv_learn", true, svc, &domain.Experience{ID: "exp_sup"})
	if err != nil {
		t.Fatalf("healthy upsert after supersede must not abort the batch: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("ops=%d, want 2", len(applied))
	}
	if applied[0].Status != domain.LearningOpAccepted {
		t.Fatalf("contradict status=%s reason=%s", applied[0].Status, applied[0].Reason)
	}
	if applied[1].Status != domain.LearningOpAccepted {
		t.Fatalf("upsert status=%s", applied[1].Status)
	}
	gotOld, err := store.Get("mem_phantom")
	if err != nil {
		t.Fatal(err)
	}
	if gotOld.Status != domain.MemoryStatusSuperseded {
		t.Fatalf("old record status=%s, want superseded", gotOld.Status)
	}
	var correction *domain.MemoryRecord
	for _, rec := range store.List() {
		if rec != nil && rec.ID != "mem_phantom" && rec.Retrievable() {
			correction = rec
		}
	}
	if correction == nil || !strings.Contains(correction.Body, "cannot roll back") {
		t.Fatalf("correction upsert missing: %+v", store.List())
	}
}

func TestApplyConsolidationOpsContinuesAfterFailedOp(t *testing.T) {
	store := &fakeMemoryRecordStore{}
	svc := NewMemoryService(store, nil)
	app := &App{MemoryRecords: store}
	ops := []domain.LearningOperation{
		{
			Kind:     domain.OpMemoryContradict,
			Actor:    domain.ActorLearner,
			TargetID: "mem_missing",
			Reason:   "learner supersede",
		},
		{
			Kind:     domain.OpMemoryUpsert,
			Actor:    domain.ActorLearner,
			Evidence: []string{"exp_ok"},
			Payload: map[string]any{
				"body": "User prefers newest-first ordering in the Experience tab",
				"type": domain.MemoryTypePreference,
			},
		},
	}
	applied, _, err, reviewed := app.applyConsolidationOps(ops, "conv_learn", true, svc, &domain.Experience{ID: "exp_ok"})
	if err != nil {
		t.Fatalf("one bad op must not fail the batch: %v", err)
	}
	if !reviewed {
		t.Fatal("source must stay reviewed so a bad op cannot force a retry of healthy work")
	}
	if applied[0].Status != domain.LearningOpRejected {
		t.Fatalf("missing target must be rejected, got %s", applied[0].Status)
	}
	if applied[1].Status != domain.LearningOpAccepted {
		t.Fatalf("healthy upsert status=%s", applied[1].Status)
	}
	if len(store.List()) != 1 {
		t.Fatalf("records=%d, want the surviving upsert", len(store.List()))
	}
}

func TestLearnedSkillNameDedupesPrefixes(t *testing.T) {
	tests := []struct{ in, want string }{
		{"learned-tool-mapping", "learned-tool-mapping"},
		{"learned-learned-cross-layer-tool-mapping", "learned-cross-layer-tool-mapping"},
		{"tool-mapping-workflow", "learned-tool-mapping-workflow"},
		{"skill-git-workflow", "learned-git-workflow"},
		{"update changelog ya", "learned-update-changelog-ya"},
		{"", "learned-workflow"},
		{"  ", "learned-workflow"},
	}
	for _, tt := range tests {
		if got := learnedSkillName(tt.in); got != tt.want {
			t.Errorf("learnedSkillName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFindCanonicalLearnedSkill(t *testing.T) {
	store := &fakeSkillStore{items: map[string]*domain.Skill{
		"learned-tool-mapping": {ID: "learned-tool-mapping", Origin: domain.SkillOriginLearned},
		"learned-hatch-pet":    {ID: "learned-hatch-pet", Origin: domain.SkillOriginLearned},
	}}
	if got := findCanonicalLearnedSkill(store, "tool-mapping-workflow"); got == nil || got.ID != "learned-tool-mapping" {
		t.Fatalf("expected canonical learned-tool-mapping, got %+v", got)
	}
	if got := findCanonicalLearnedSkill(store, "learned-tool-mapping"); got == nil || got.ID != "learned-tool-mapping" {
		t.Fatalf("exact id must resolve, got %+v", got)
	}
	if got := findCanonicalLearnedSkill(store, "pet-pipeline"); got != nil {
		t.Fatalf("unrelated topic must not resolve to a canonical skill, got %s", got.ID)
	}
	if got := findCanonicalLearnedSkill(nil, "anything"); got != nil {
		t.Fatal("nil store must return nil")
	}
}

func TestApplyLearnedSkillRevisionAdoptsCanonical(t *testing.T) {
	store := &fakeSkillStore{items: map[string]*domain.Skill{
		"learned-tool-mapping": {
			ID: "learned-tool-mapping", Name: "learned-tool-mapping",
			Origin: domain.SkillOriginLearned,
			Status: domain.SkillStatusExperimental, Version: 1, ActiveVersion: 1,
		},
	}}
	app := &App{Skills: store}
	proposed := newLearnedSkill("learned-tool-mapping-workflow", "desc", "# x\n\n## Purpose\ny\n\n## Trigger\nz\n\n## Steps\n1")
	if !app.applyLearnedSkillRevision(proposed, "learned-tool-mapping-workflow") {
		t.Fatal("revision should be accepted")
	}
	if proposed.ID != "learned-tool-mapping" || proposed.Name != "learned-tool-mapping" {
		t.Fatalf("evolution must adopt the canonical id, got id=%s name=%s", proposed.ID, proposed.Name)
	}
}

func TestRecoverStaleLearningJobs(t *testing.T) {
	now := time.Now()
	past := now.Add(-30 * time.Minute)
	fresh := now.Add(-time.Minute)
	jobs := &fakeLearningJobStore{items: map[string]*domain.LearningJob{
		"stale_running": {ID: "stale_running", Kind: "learner", Status: domain.LearningJobRunning, StartedAt: &past},
		"fresh_running": {ID: "fresh_running", Kind: "learner", Status: domain.LearningJobRunning, StartedAt: &fresh},
		"stale_queued":  {ID: "stale_queued", Kind: "learner", Status: domain.LearningJobQueued, CreatedAt: past},
		"fresh_queued":  {ID: "fresh_queued", Kind: "learner", Status: domain.LearningJobQueued, CreatedAt: fresh},
	}}
	app := &App{LearningJobs: jobs}
	app.RecoverStaleLearningJobs()

	if j := jobs.items["stale_running"]; j.Status != domain.LearningJobError || j.FinishedAt == nil || !strings.Contains(j.Error, "interrupted") {
		t.Fatalf("stale running job = %+v", j)
	}
	if j := jobs.items["fresh_running"]; j.Status != domain.LearningJobRunning || j.FinishedAt != nil {
		t.Fatalf("fresh running job must be untouched: %+v", j)
	}
	if j := jobs.items["stale_queued"]; j.Status != domain.LearningJobError || j.FinishedAt == nil {
		t.Fatalf("stale queued job = %+v", j)
	}
	if j := jobs.items["fresh_queued"]; j.Status != domain.LearningJobQueued || j.FinishedAt != nil {
		t.Fatalf("fresh queued job must be untouched: %+v", j)
	}
}
