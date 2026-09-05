package application

import (
	"fmt"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

func (a *App) runLearningJob(id string) {
	if a == nil || a.LearningJobs == nil {
		return
	}
	job, err := a.LearningJobs.Get(id)
	if err != nil || job == nil {
		return
	}
	now := clock.NewTime().Time()
	job.Status = domain.LearningJobRunning
	job.StartedAt = &now
	_ = a.LearningJobs.Save(job)
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventLearningJobStarted, contracts.LearningJobEvent{
			JobID:  job.ID,
			Kind:   job.Kind,
			Status: job.Status,
		})
	}
	var runErr error
	switch job.Kind {
	case domain.LearningJobConsolidate:
		runErr = a.consolidateJob(job)
	case domain.LearningJobEvolveSkill:
		runErr = a.evolveSkillJob(job)
	case domain.LearningJobEvaluate:
		runErr = a.evaluateSkillJob(job)
	case domain.LearningJobRetire:
		if a.lifecycle != nil {
			a.lifecycle.PruneOnce()
		}
	default:
		runErr = fmt.Errorf("unknown job kind %s", job.Kind)
	}
	done := clock.NewTime().Time()
	job.FinishedAt = &done
	if runErr != nil {
		job.Status = domain.LearningJobError
		job.Error = runErr.Error()
		a.log("warn", "learning", "job error: id=%s kind=%s err=%v", job.ID, job.Kind, runErr)
		if a.Bus != nil {
			a.Bus.Emit(contracts.EventLearningJobError, contracts.LearningJobEvent{
				JobID:  job.ID,
				Kind:   job.Kind,
				Status: job.Status,
				Error:  "learning job failed",
			})
		}
	} else {
		job.Status = domain.LearningJobDone
		if a.Bus != nil {
			a.Bus.Emit(contracts.EventLearningJobDone, contracts.LearningJobEvent{
				JobID:  job.ID,
				Kind:   job.Kind,
				Status: job.Status,
			})
		}
	}
	_ = a.LearningJobs.Save(job)
	a.emitMemoryUpdated()
}

func (a *App) consolidateJob(job *domain.LearningJob) error {
	if a.Experiences == nil || a.MemoryRecords == nil {
		return fmt.Errorf("growth stores not configured")
	}
	exp, err := a.Experiences.Get(job.ExperienceID)
	if err != nil {
		return err
	}
	svc := NewMemoryService(a.MemoryRecords, a.LearningOps)
	ops := teachingOps(exp, job.ID)
	if len(ops) == 0 {
		return nil
	}
	for i := range ops {
		if ops[i].Kind == domain.OpMemoryUpsert {
			body := payloadString(ops[i].Payload, "body")
			typ := payloadString(ops[i].Payload, "type")
			project := payloadString(ops[i].Payload, "project")
			if id := matchingRecordID(a.MemoryRecords, body, typ, project); id != "" {
				ops[i].Kind = domain.OpMemoryStrengthen
				if ops[i].Payload == nil {
					ops[i].Payload = map[string]any{}
				}
				ops[i].Payload["id"] = id
			}
		}
		if err := svc.Apply(&ops[i]); err != nil {
			return err
		}
	}
	return nil
}

func matchingRecordID(store MemoryRecordStore, body, typ, project string) string {
	if store == nil {
		return ""
	}
	want := domain.NormalizeMemoryContent(body)
	if want == "" {
		return ""
	}
	for _, rec := range store.List() {
		if rec == nil || !rec.Retrievable() {
			continue
		}
		if typ != "" && rec.Type != typ {
			continue
		}
		if project != "" && !strings.EqualFold(rec.Scope.Project, project) {
			continue
		}
		if domain.NormalizeMemoryContent(rec.Body) == want {
			return rec.ID
		}
	}
	return ""
}

func teachingOps(exp *domain.Experience, jobID string) []domain.LearningOperation {
	if exp == nil {
		return nil
	}
	var ops []domain.LearningOperation
	now := clock.NewTime().Time()
	add := func(body, typ string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		ops = append(ops, domain.LearningOperation{
			ID:       domain.NewULID(domain.IDPrefixLearnOp),
			Kind:     domain.OpMemoryUpsert,
			Status:   domain.LearningOpProposed,
			Actor:    domain.ActorConsolidator,
			JobID:    jobID,
			Evidence: []string{exp.ID},
			Payload: map[string]any{
				"body":    body,
				"type":    typ,
				"scope":   domain.MemoryScopeUser,
				"project": exp.Scope.Project,
			},
			CreatedAt: now,
		})
	}
	if exp.Signals.ExplicitTeaching {
		add(exp.Goal, domain.MemoryTypePreference)
	}
	for _, c := range exp.Corrections {
		text := strings.TrimSpace(c.Desired)
		if text == "" {
			text = strings.TrimSpace(c.UserSaid)
		}
		kind := domain.MemoryTypePreference
		if c.Type == "fact" {
			kind = domain.MemoryTypeFact
		}
		if c.Type == "procedure" {
			kind = domain.MemoryTypeConstraint
		}
		add(text, kind)
	}
	return ops
}

func (a *App) evolveSkillJob(job *domain.LearningJob) error {
	if a.Skills == nil || a.Experiences == nil {
		return fmt.Errorf("skill store not configured")
	}
	exp, err := a.Experiences.Get(job.ExperienceID)
	if err != nil {
		return err
	}
	if len(exp.Actions) < 3 {
		return nil
	}
	name := "learned-" + domain.SkillSlug(strings.TrimSpace(exp.Goal))
	if name == "learned-" || len(name) < 12 {
		name = "learned-workflow"
	}
	if len(name) > 48 {
		name = name[:48]
	}
	var b strings.Builder
	b.WriteString("Repeat this verified workflow.\n\n")
	for i, act := range exp.Actions {
		fmt.Fprintf(&b, "%d. `%s`", i+1, act.Name)
		if act.Digest != "" {
			fmt.Fprintf(&b, " — %s", act.Digest)
		}
		b.WriteByte('\n')
	}
	skill := &domain.Skill{
		ID:            name,
		Name:          name,
		Description:   clip(exp.Goal, 200),
		Content:       b.String(),
		Origin:        domain.SkillOriginLearned,
		Status:        domain.SkillStatusExperimental,
		Version:       1,
		ActiveVersion: 1,
		OwnedBy:       string(domain.SkillOriginLearned),
	}
	if existing, err := a.Skills.Get(name, string(domain.SkillOriginLearned)); err == nil && existing != nil {
		if existing.Version >= domain.MaxSkillRevisions {
			return nil
		}
		skill.ID = existing.ID
		skill.Name = existing.Name
		skill.Status = domain.SkillStatusExperimental
	}
	skill.EnsureStatusDefault()
	if domain.CreatorMayPromote(domain.ActorSkillEvolver) {
		return fmt.Errorf("evolver must not promote")
	}
	if err := a.Skills.Save(skill); err != nil {
		return err
	}
	a.emitSkillLifecycle("evolve", skill.ID, string(skill.Status))
	// Evaluate in the same job goroutine. Nested goSafe races the skill
	// store with the still-finishing turn.
	return a.evaluateSkillJob(&domain.LearningJob{
		Kind:    domain.LearningJobEvaluate,
		SkillID: skill.ID,
		Reason:  "post_evolve",
	})
}

func (a *App) evaluateSkillJob(job *domain.LearningJob) error {
	if a.Skills == nil || job.SkillID == "" {
		return nil
	}
	if domain.CreatorMayPromote(domain.ActorSkillEval) || domain.CreatorMayPromote(domain.ActorSkillEvolver) {
		return fmt.Errorf("evaluator must not be the creator")
	}
	s, err := a.Skills.Get(job.SkillID, string(domain.SkillOriginLearned))
	if err != nil {
		return err
	}
	if s.Status == domain.SkillStatusTrusted {
		return nil
	}
	if s.Version > domain.MaxSkillRevisions {
		s.Status = domain.SkillStatusDeprecated
		s.Touch(clock.NewTime().Time())
		return a.Skills.Save(s)
	}
	// No deterministic verifier in v1: stay experimental. Trusted is human-only.
	return nil
}

func (a *App) emitSkillLifecycle(op, id, status string) {
	if a == nil {
		return
	}
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventSkillUpdated, map[string]any{
			"id":     id,
			"status": status,
			"op":     op,
		})
	}
	if a.Trajectory != nil {
		a.Trajectory.Record("skill_"+op, map[string]interface{}{
			"id":     id,
			"status": status,
		})
	}
}
