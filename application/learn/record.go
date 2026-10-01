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
	turns, iters := domain.CountUnreviewedLearningProgress(conv.Messages, conv.LastReviewedMsgCount)
	trig := domain.DecideLearningTriggerWith(exp, nil, domain.LearningReviewProgress{
		UnreviewedUserTurns: turns,
		UnreviewedToolIters: iters,
		Interval:            s.LearnerNudgeInterval(),
	})
	if !trig.Enqueue {
		return
	}
	jobID := s.enqueueLearnerJob(conv, exp, trig)
	if jobID == "" {
		return
	}
	// The job leaves the active set when RunLearningJob exits, so launching
	// outside recordMu keeps a synchronous Go func (tests) from deadlocking.
	s.goSafe("learning", func() { s.RunLearningJob(jobID) })
}

// enqueueLearnerJob persists a queued learner job and marks the source
// conversation active, or returns "" when a job for it is already queued or
// running. The check, the persist, and the active mark share one recordMu
// critical section so two simultaneous turn endings cannot both enqueue.
func (s *Service) enqueueLearnerJob(conv *domain.Conversation, exp domain.Experience, trig domain.LearningTrigger) string {
	s.recordMu.Lock()
	defer s.recordMu.Unlock()
	if s.hasActiveLearningJobLocked(conv.ID) {
		s.log("debug", "learning", "learner trigger coalesced: reason=%s conv=%s", trig.Reason, conv.ID)
		return ""
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
		return ""
	}
	if s.activeJobs == nil {
		s.activeJobs = map[string]string{}
	}
	s.activeJobs[job.ID] = conv.ID
	s.log("info", "learning", "job queued: id=%s kind=%s reason=%s conv=%s", job.ID, job.Kind, job.Reason, conv.ID)
	return job.ID
}

// hasActiveLearningJob reports whether this process has a learner job queued
// or running for the conversation. The answer comes from the in-memory
// activeJobs set — populated at enqueue and cleared when RunLearningJob
// exits — so the turn-end hot path never reads the job or experience store.
// Persisted queued/running rows from a previous process are deliberately
// ignored: those jobs can never start again, and RecoverStaleLearningJobs
// expires them past their TTL on startup.
func (s *Service) hasActiveLearningJob(conversationID string) bool {
	if s == nil {
		return false
	}
	s.recordMu.Lock()
	defer s.recordMu.Unlock()
	return s.hasActiveLearningJobLocked(conversationID)
}

// hasActiveLearningJobLocked is hasActiveLearningJob for callers already
// holding recordMu; the check-then-enqueue critical section must stay atomic.
func (s *Service) hasActiveLearningJobLocked(conversationID string) bool {
	for _, convID := range s.activeJobs {
		if convID == conversationID {
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
