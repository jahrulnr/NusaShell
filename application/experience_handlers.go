package application

import (
	"encoding/json"
	"sort"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
)

func (a *App) dispatchExperience(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodExperienceList:   decodeReq(a.handleExperienceList),
		contracts.MethodExperienceGet:    decodeReq(a.handleExperienceGet),
		contracts.MethodExperienceDelete: decodeReq(a.handleExperienceDelete),
	}, "experience")(method, payload)
}

// handleExperienceList pages the experience catalog newest first. The list
// is cheap (experiences are compact episodes) but unbounded in count, so
// the UI must not load it all at once.
func (a *App) handleExperienceList(req contracts.ExperienceListRequest) (any, *contracts.RPCError) {
	out := make([]contracts.ExperienceDTO, 0)
	if a.Experiences == nil {
		return contracts.ExperienceListResult{Experiences: out}, nil
	}
	all := a.Experiences.List()
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

func (a *App) handleExperienceGet(req contracts.ExperienceIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "experience id is required"}
	}
	if a.Experiences == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	e, err := a.Experiences.Get(req.ID)
	if err != nil || e == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	return contracts.ExperienceGetResult{Experience: contracts.ExperienceDTOFromDomain(e)}, nil
}

// handleExperienceDelete removes an experience (hard delete) plus any
// learner jobs that were queued from it. The user owns the catalog: a
// wrong or noisy episode must be removable, not just hidden.
func (a *App) handleExperienceDelete(req contracts.ExperienceIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "experience id is required"}
	}
	if a.Experiences == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	e, err := a.Experiences.Get(req.ID)
	if err != nil || e == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	if err := a.Experiences.Delete(req.ID); err != nil {
		return nil, rpcInternal(err)
	}
	// Jobs derived from this episode can no longer run (their source is
	// gone); remove them so the jobs list and log stay truthful.
	a.deleteJobsForExperience(req.ID)
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventExperienceDeleted, map[string]any{"id": req.ID})
	}
	return contracts.ExperienceGetResult{Experience: contracts.ExperienceDTOFromDomain(e)}, nil
}

// deleteJobsForExperience removes learner jobs that reference an
// experience id, together with their trajectory events and transcripts.
func (a *App) deleteJobsForExperience(experienceID string) {
	if a == nil || a.LearningJobs == nil {
		return
	}
	for _, job := range a.LearningJobs.List() {
		if job == nil || job.ExperienceID != experienceID {
			continue
		}
		a.deleteLearningJob(job.ID)
	}
}
