package learn

import (
	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

func (s *Service) RecordExperience(conv *domain.Conversation, headless bool) {
	if s == nil || s.deps.Experiences == nil || conv == nil {
		return
	}
	exp := domain.ExtractExperience(conv, headless)
	if err := s.deps.Experiences.Save(&exp); err != nil {
		s.log("warn", "learning", "experience save failed: %v", err)
		return
	}
	if s.deps.Bus != nil {
		s.deps.Bus.Emit(contracts.EventExperienceRecorded, contracts.ExperienceDTOFromDomain(&exp))
	}
	if headless || s.deps.Jobs == nil {
		return
	}
	var history []domain.Experience
	for _, h := range s.deps.Experiences.ListByConversation(conv.ID) {
		if h.ID == exp.ID {
			continue
		}
		history = append(history, *h)
	}
	turns, iters := domain.CountUnreviewedLearningProgress(conv.Messages, conv.LastReviewedMsgCount)
	trig := domain.DecideLearningTriggerWith(exp, history, domain.LearningReviewProgress{
		UnreviewedUserTurns: turns,
		UnreviewedToolIters: iters,
		Interval:            s.LearnerNudgeInterval(),
	})
	if !trig.Enqueue {
		return
	}
	s.recordMu.Lock()
	defer s.recordMu.Unlock()
	if s.hasActiveLearningJob(conv.ID) {
		s.log("debug", "learning", "learner trigger coalesced: reason=%s conv=%s", trig.Reason, conv.ID)
		return
	}
	now := clock.NewTime().Time()
	job := &domain.LearningJob{
		ID:           domain.NewULID(domain.IDPrefixLearnJob),
		Kind:         domain.LearningJobLearner,
		ExperienceID: exp.ID,
		Reason:       trig.Reason,
		Priority:     trig.Priority,
		Status:       domain.LearningJobQueued,
		CreatedAt:    now,
	}
	if err := s.deps.Jobs.Save(job); err != nil {
		s.log("warn", "learning", "learning job save failed: %v", err)
		return
	}
	s.log("info", "learning", "job queued: id=%s kind=%s reason=%s conv=%s", job.ID, job.Kind, job.Reason, conv.ID)
	jobID := job.ID
	s.goSafe("learning", func() { s.RunLearningJob(jobID) })
}

func (s *Service) hasActiveLearningJob(conversationID string) bool {
	if s == nil || s.deps.Jobs == nil || s.deps.Experiences == nil {
		return false
	}
	for _, job := range s.deps.Jobs.List() {
		if job == nil || (job.Status != domain.LearningJobQueued && job.Status != domain.LearningJobRunning) {
			continue
		}
		experience, err := s.deps.Experiences.Get(job.ExperienceID)
		if err == nil && experience != nil && experience.ConversationID == conversationID {
			return true
		}
	}
	return false
}

func (s *Service) LearnerNudgeInterval() int {
	if s == nil || s.deps.Settings == nil {
		return domain.DefaultLearnerNudgeInterval
	}
	return domain.EffectiveLearnerNudgeInterval(s.deps.Settings.Get().LearnerNudgeInterval)
}
