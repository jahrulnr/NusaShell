package application

import (
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

func (a *App) recordExperience(conv *domain.Conversation, headless bool) {
	if a == nil || a.Experiences == nil || conv == nil {
		return
	}
	exp := ExtractExperience(conv, headless)
	if err := a.Experiences.Save(&exp); err != nil {
		a.log("warn", "learning", "experience save failed: %v", err)
		return
	}
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventExperienceRecorded, contracts.ExperienceDTOFromDomain(&exp))
	}
	if headless || a.LearningJobs == nil {
		return
	}
	var history []domain.Experience
	for _, h := range a.Experiences.ListByConversation(conv.ID) {
		if h.ID == exp.ID {
			continue
		}
		history = append(history, *h)
	}
	trig := domain.DecideLearningTrigger(exp, history)
	if !trig.Enqueue {
		return
	}
	now := clock.NewTime().Time()
	job := &domain.LearningJob{
		ID:           domain.NewULID(domain.IDPrefixLearnJob),
		Kind:         domain.LearningJobConsolidate,
		ExperienceID: exp.ID,
		Reason:       trig.Reason,
		Priority:     trig.Priority,
		Status:       domain.LearningJobQueued,
		CreatedAt:    now,
	}
	if strings.Contains(trig.Reason, "procedure") {
		job.Kind = domain.LearningJobEvolveSkill
	}
	if err := a.LearningJobs.Save(job); err != nil {
		a.log("warn", "learning", "learning job save failed: %v", err)
		return
	}
	a.log("info", "learning", "job queued: id=%s kind=%s reason=%s conv=%s", job.ID, job.Kind, job.Reason, conv.ID)
	jobID := job.ID
	a.goSafe("learning", func() { a.runLearningJob(jobID) })
}
