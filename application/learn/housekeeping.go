package learn

import (
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

const (
	trajectoryRetention = 90 * 24 * time.Hour
	jobRetention        = 90 * 24 * time.Hour
	experienceRetention = 180 * 24 * time.Hour
	operationRetention  = 90 * 24 * time.Hour
)

type deleteManyStore interface {
	DeleteMany(ids []string) error
}

// PruneGrowthOnce applies the balanced retention policy to auxiliary learning
// history. Active jobs and every record needed by one are preserved. Durable,
// retrievable memories are managed separately by LifecycleManager and are
// never deleted here.
func (s *Service) PruneGrowthOnce() {
	if s == nil {
		return
	}
	now := clock.NewTime().Time()
	activeExperienceIDs := map[string]struct{}{}
	var expiredJobIDs, transcriptIDs []string
	if s.deps.Jobs != nil {
		for _, job := range s.deps.Jobs.List() {
			if job == nil {
				continue
			}
			if job.Status == domain.LearningJobQueued || job.Status == domain.LearningJobRunning {
				if job.ExperienceID != "" {
					activeExperienceIDs[job.ExperienceID] = struct{}{}
				}
				continue
			}
			anchor := job.CreatedAt
			if job.FinishedAt != nil {
				anchor = *job.FinishedAt
			}
			if !anchor.IsZero() && anchor.Before(now.Add(-jobRetention)) {
				expiredJobIDs = append(expiredJobIDs, job.ID)
				if safeLearningConversationID(job.LLMConversationID) {
					transcriptIDs = append(transcriptIDs, job.LLMConversationID)
				}
			}
		}
	}

	var expiredExperienceIDs []string
	if s.deps.Experiences != nil {
		cutoff := now.Add(-experienceRetention)
		for _, experience := range s.deps.Experiences.List() {
			if experience == nil || experience.Timestamp.IsZero() || !experience.Timestamp.Before(cutoff) {
				continue
			}
			if _, active := activeExperienceIDs[experience.ID]; !active {
				expiredExperienceIDs = append(expiredExperienceIDs, experience.ID)
			}
		}
	}

	var expiredOperationIDs []string
	if s.deps.Operations != nil {
		cutoff := now.Add(-operationRetention)
		for _, operation := range s.deps.Operations.List() {
			if operation == nil || operation.Status == domain.LearningOpProposed || operation.CreatedAt.IsZero() {
				continue
			}
			if operation.CreatedAt.Before(cutoff) {
				expiredOperationIDs = append(expiredOperationIDs, operation.ID)
			}
		}
	}

	jobsErr := deleteStoreIDs(s.deps.Jobs, expiredJobIDs)
	if err := deleteStoreIDs(s.deps.Experiences, expiredExperienceIDs); err != nil {
		s.log("warn", "learning", "experience housekeeping failed: %v", err)
	}
	if err := deleteStoreIDs(s.deps.Operations, expiredOperationIDs); err != nil {
		s.log("warn", "learning", "operation housekeeping failed: %v", err)
	}
	if jobsErr != nil {
		s.log("warn", "learning", "job housekeeping failed: %v", jobsErr)
	}
	if jobsErr == nil && s.deps.Conversations != nil {
		for _, id := range uniqueStrings(transcriptIDs) {
			_ = s.deps.Conversations.Delete(id)
		}
	}
	if s.deps.Trajectory != nil {
		cutoff := now.Add(-trajectoryRetention)
		s.deps.Trajectory.DeleteEvents(func(event TrajectoryEvent) bool {
			return !event.Timestamp.IsZero() && event.Timestamp.Before(cutoff)
		})
	}
	if n := len(expiredJobIDs) + len(expiredExperienceIDs) + len(expiredOperationIDs); n > 0 {
		s.log("info", "learning", "growth housekeeping deleted jobs=%d experiences=%d operations=%d transcripts=%d", len(expiredJobIDs), len(expiredExperienceIDs), len(expiredOperationIDs), len(uniqueStrings(transcriptIDs)))
	}
}

func deleteStoreIDs(store interface{ Delete(string) error }, ids []string) error {
	if store == nil || len(ids) == 0 {
		return nil
	}
	if bulk, ok := any(store).(deleteManyStore); ok {
		return bulk.DeleteMany(ids)
	}
	var firstErr error
	for _, id := range ids {
		if err := store.Delete(id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
