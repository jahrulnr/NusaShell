package learn

import (
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// staleLearningJobTTL is the age beyond which a job that never finished is
// considered orphaned. Learning jobs normally complete in seconds to a few
// minutes (LLM turn plus apply); anything still "running" or still "queued"
// after this window almost certainly belongs to a previous app instance
// that restarted mid-run.
const staleLearningJobTTL = 10 * time.Minute

// RecoverStaleLearningJobs reconciles the persisted job log on startup.
// Jobs abandoned by a restart keep their row for audit but stop pretending
// to be active:
//
//   - running jobs older than the TTL are marked error with an
//     "interrupted" reason. They are deliberately not requeued: re-running
//     the headless LLM turn could double-apply mutations. The source
//     conversation cursor never advanced for them, so the periodic nudge
//     will review the same content later anyway.
//   - queued jobs that never started within the TTL are also marked error;
//     they were spawned synchronously by the last turn of a previous
//     instance and can never start again.
//
// Fresh queued jobs are left alone: in-flight spawns from the current
// instance may still pick them up.
func (s *Service) RecoverStaleLearningJobs() {
	if s == nil || s.deps.Jobs == nil {
		return
	}
	now := clock.NewTime().Time()
	for _, job := range s.deps.Jobs.List() {
		if job == nil {
			continue
		}
		switch job.Status {
		case domain.LearningJobRunning:
			if job.StartedAt == nil || now.Sub(*job.StartedAt) <= staleLearningJobTTL {
				continue
			}
			job.Status = domain.LearningJobError
			job.Error = "interrupted: stale running job recovered on startup"
			job.FinishedAt = &now
			if err := s.deps.Jobs.Save(job); err == nil {
				s.log("warn", "learning", "recovered stale running job: id=%s started=%s", job.ID, job.StartedAt.Format(time.RFC3339))
			}
		case domain.LearningJobQueued:
			if now.Sub(job.CreatedAt) <= staleLearningJobTTL {
				continue
			}
			job.Status = domain.LearningJobError
			job.Error = "expired: queued job abandoned by restart"
			job.FinishedAt = &now
			if err := s.deps.Jobs.Save(job); err == nil {
				s.log("warn", "learning", "expired abandoned queued job: id=%s created=%s", job.ID, job.CreatedAt.Format(time.RFC3339))
			}
		}
	}
}
