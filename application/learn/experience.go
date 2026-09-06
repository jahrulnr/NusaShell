package learn

import (
	"sort"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
)

// HandleExperienceList pages the experience catalog newest first. The list
// is cheap (experiences are compact episodes) but unbounded in count, so
// the UI must not load it all at once.
func (s *Service) HandleExperienceList(req contracts.ExperienceListRequest) (any, *contracts.RPCError) {
	out := make([]contracts.ExperienceDTO, 0)
	if s.deps.Experiences == nil {
		return contracts.ExperienceListResult{Experiences: out}, nil
	}
	all := s.deps.Experiences.List()
	total := len(all)
	sorted := make([]*domain.Experience, 0, total)
	for _, e := range all {
		if e != nil {
			sorted = append(sorted, e)
		}
	}
	// Newest first: experiences are timestamped with event time, which is
	// monotonic per session (older transcripts append earlier).
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Timestamp.After(sorted[j].Timestamp) })

	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset >= total {
		return contracts.ExperienceListResult{Experiences: out, Total: total}, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	for _, e := range sorted[offset:end] {
		out = append(out, contracts.ExperienceDTOFromDomain(e))
	}
	return contracts.ExperienceListResult{Experiences: out, Total: total}, nil
}

func (s *Service) HandleExperienceGet(req contracts.ExperienceIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "experience id is required"}
	}
	if s.deps.Experiences == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	e, err := s.deps.Experiences.Get(req.ID)
	if err != nil || e == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	return contracts.ExperienceGetResult{Experience: contracts.ExperienceDTOFromDomain(e)}, nil
}

// HandleExperienceDelete removes an experience (hard delete) plus any
// learner jobs that were queued from it. The user owns the catalog: a
// wrong or noisy episode must be removable, not just hidden.
func (s *Service) HandleExperienceDelete(req contracts.ExperienceIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "experience id is required"}
	}
	if s.deps.Experiences == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	e, err := s.deps.Experiences.Get(req.ID)
	if err != nil || e == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	if err := s.deps.Experiences.Delete(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: err.Error()}
	}
	// Jobs derived from this episode can no longer run (their source is
	// gone); remove them so the jobs list and log stay truthful.
	s.deleteJobsForExperience(req.ID)
	if s.deps.Bus != nil {
		s.deps.Bus.Emit(contracts.EventExperienceDeleted, map[string]any{"id": req.ID})
	}
	return contracts.ExperienceGetResult{Experience: contracts.ExperienceDTOFromDomain(e)}, nil
}

// deleteJobsForExperience removes learner jobs that reference an
// experience id, together with their trajectory events and transcripts.
func (s *Service) deleteJobsForExperience(experienceID string) {
	if s == nil || s.deps.Jobs == nil {
		return
	}
	for _, job := range s.deps.Jobs.List() {
		if job == nil || job.ExperienceID != experienceID {
			continue
		}
		s.DeleteLearningJob(job.ID)
	}
}
