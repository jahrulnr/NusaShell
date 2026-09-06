package memory

import (
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/rpcdispatch"
	clock "nusashell/pkg/time"
)

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

func docDTO(e *domain.DocumentEntry, tier string) contracts.MemoryEntryDTO {
	dto := contracts.MemoryEntryDTO{
		ID:        e.ID,
		Content:   e.Content,
		Source:    e.Source,
		CreatedAt: clock.NewTime(e.UpdatedAt).Format(timeRFC3339),
		Tier:      tier,
	}
	if dto.Source == "" {
		dto.Source = "user"
	}
	return dto
}

func recordDTO(m *domain.MemoryRecord) contracts.MemoryEntryDTO {
	d := contracts.MemoryRecordDTOFromDomain(m)
	return contracts.MemoryEntryDTO{
		ID:        d.ID,
		Content:   d.Body,
		Body:      d.Body,
		Type:      d.Type,
		Status:    d.Status,
		Scope:     d.Scope,
		Source:    d.Source,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
		Project:   d.Project,
		Tier:      contracts.MemoryTierRecord,
	}
}

func (s *Service) emitUpdated() {
	if s.deps.Bus != nil {
		s.deps.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{"source": "rpc"})
	}
}

func (s *Service) changed(tier, op string) {
	if s.deps.OnChanged != nil {
		s.deps.OnChanged(tier, op)
	}
}

func (s *Service) HandleList() (any, *contracts.RPCError) {
	out := make([]contracts.MemoryEntryDTO, 0)
	if s.deps.User != nil {
		mem := s.deps.User.Load()
		for i := range mem.Entries {
			out = append(out, docDTO(&mem.Entries[i], domain.MemoryTierUser))
		}
	}
	if s.deps.Agent != nil {
		mem := s.deps.Agent.Load()
		for i := range mem.Entries {
			out = append(out, docDTO(&mem.Entries[i], domain.MemoryTierAgent))
		}
	}
	if s.deps.Records != nil {
		for _, m := range s.deps.Records.List() {
			if m == nil {
				continue
			}
			out = append(out, recordDTO(m))
		}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (s *Service) HandleUserUpdate(req contracts.MemoryUserUpdateRequest) (any, *contracts.RPCError) {
	if s.deps.User == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "user memory store not configured"}
	}
	content := strings.TrimSpace(req.Content)
	if len(content) > domain.UserCharCap {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "user memory cannot exceed 4000 characters"}
	}
	if err := s.deps.User.Update([]domain.DocumentEntry{{Content: content, Source: "user"}}); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	mem := s.deps.User.Load()
	var entry domain.DocumentEntry
	if mem != nil && len(mem.Entries) > 0 {
		entry = mem.Entries[0]
	}
	s.emitUpdated()
	s.changed(domain.MemoryTierUser, "update")
	return contracts.MemoryUserUpdateResult{Entry: docDTO(&entry, domain.MemoryTierUser)}, nil
}

func (s *Service) HandleAgentUpdate(req contracts.MemoryAgentUpdateRequest) (any, *contracts.RPCError) {
	if s.deps.Agent == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "soul memory store not configured"}
	}
	content := strings.TrimSpace(req.Content)
	if len(content) > domain.AgentCharCap {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "soul memory cannot exceed 4000 characters"}
	}
	if err := s.deps.Agent.Update([]domain.DocumentEntry{{Content: content, Source: "user"}}); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	mem := s.deps.Agent.Load()
	var entry domain.DocumentEntry
	if mem != nil && len(mem.Entries) > 0 {
		entry = mem.Entries[0]
	}
	s.emitUpdated()
	s.changed(domain.MemoryTierAgent, "update")
	return contracts.MemoryAgentUpdateResult{Entry: docDTO(&entry, domain.MemoryTierAgent)}, nil
}

func (s *Service) HandleSearch(req contracts.MemorySearchRequest) (any, *contracts.RPCError) {
	if s.deps.Records == nil {
		return contracts.MemoryListResult{Entries: []contracts.MemoryEntryDTO{}}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	q := strings.ToLower(strings.TrimSpace(req.Query))
	out := make([]contracts.MemoryEntryDTO, 0)
	filter := domain.MemorySearchFilter{
		Query:   q,
		Type:    req.Type,
		Status:  req.Status,
		Scope:   req.Scope,
		Project: req.Project,
		Limit:   limit,
	}
	for _, m := range s.deps.Records.List() {
		if !m.Matches(filter) {
			continue
		}
		out = append(out, recordDTO(m))
		if len(out) >= limit {
			break
		}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (s *Service) HandleGet(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "memory id is required"}
	}
	if s.deps.Records == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "memory not found"}
	}
	m, err := s.deps.Records.Get(req.ID)
	if err != nil || m == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "memory not found"}
	}
	return recordDTO(m), nil
}

// HandleDelete deletes a memory record for good: the record row,
// its graph edges, and its retrieval-visible presence. Retire was the
// only human action before, but users want irrelevant entries gone, not
// just hidden; the lifecycle still retires internally for audit.
func (s *Service) HandleDelete(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "memory id is required"}
	}
	if s.deps.Records == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "memory record store not configured"}
	}
	m, err := s.deps.Records.Get(req.ID)
	if err != nil || m == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "memory not found"}
	}
	if err := s.deps.Records.Delete(req.ID); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	if s.deps.OnRecordDeleted != nil {
		s.deps.OnRecordDeleted(req.ID)
	}
	s.emitUpdated()
	return recordDTO(m), nil
}

func (s *Service) HandleRetire(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "memory id is required"}
	}
	if s.deps.Records == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "memory record store not configured"}
	}
	m, err := s.deps.Records.Get(req.ID)
	if err != nil || m == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "memory not found"}
	}
	m.Retire(clock.NewTime().Time())
	if err := s.deps.Records.Save(m); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	s.emitUpdated()
	return recordDTO(m), nil
}
