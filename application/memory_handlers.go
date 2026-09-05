package application

import (
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

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

func (a *App) emitMemoryUpdated() {
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{"source": "rpc"})
	}
}

func (a *App) handleMemoryList() (any, *contracts.RPCError) {
	out := make([]contracts.MemoryEntryDTO, 0)
	if a.User != nil {
		mem := a.User.Load()
		for i := range mem.Entries {
			out = append(out, docDTO(&mem.Entries[i], domain.MemoryTierUser))
		}
	}
	if a.Agent != nil {
		mem := a.Agent.Load()
		for i := range mem.Entries {
			out = append(out, docDTO(&mem.Entries[i], domain.MemoryTierAgent))
		}
	}
	if a.MemoryRecords != nil {
		for _, m := range a.MemoryRecords.List() {
			if m == nil {
				continue
			}
			out = append(out, recordDTO(m))
		}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (a *App) handleMemoryUserUpdate(req contracts.MemoryUserUpdateRequest) (any, *contracts.RPCError) {
	if a.User == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "user memory store not configured"}
	}
	content := strings.TrimSpace(req.Content)
	if len(content) > domain.UserCharCap {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "user memory cannot exceed 4000 characters"}
	}
	if err := a.User.Update([]domain.DocumentEntry{{Content: content, Source: "user"}}); err != nil {
		return nil, rpcInternal(err)
	}
	mem := a.User.Load()
	var entry domain.DocumentEntry
	if mem != nil && len(mem.Entries) > 0 {
		entry = mem.Entries[0]
	}
	a.emitMemoryUpdated()
	a.publishAnnouncementToAll(newAnnouncement(
		"memory_changed",
		domain.AnnouncementMemoryChangedArgs(domain.MemoryTierUser, "update"),
		domain.AnnouncementMemoryChangedMessage(),
	), "")
	return contracts.MemoryUserUpdateResult{Entry: docDTO(&entry, domain.MemoryTierUser)}, nil
}

func (a *App) handleMemoryAgentUpdate(req contracts.MemoryAgentUpdateRequest) (any, *contracts.RPCError) {
	if a.Agent == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "soul memory store not configured"}
	}
	content := strings.TrimSpace(req.Content)
	if len(content) > domain.AgentCharCap {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "soul memory cannot exceed 4000 characters"}
	}
	if err := a.Agent.Update([]domain.DocumentEntry{{Content: content, Source: "user"}}); err != nil {
		return nil, rpcInternal(err)
	}
	mem := a.Agent.Load()
	var entry domain.DocumentEntry
	if mem != nil && len(mem.Entries) > 0 {
		entry = mem.Entries[0]
	}
	a.emitMemoryUpdated()
	a.publishAnnouncementToAll(newAnnouncement(
		"memory_changed",
		domain.AnnouncementMemoryChangedArgs(domain.MemoryTierAgent, "update"),
		domain.AnnouncementMemoryChangedMessage(),
	), "")
	return contracts.MemoryAgentUpdateResult{Entry: docDTO(&entry, domain.MemoryTierAgent)}, nil
}

func (a *App) handleMemorySearch(req contracts.MemorySearchRequest) (any, *contracts.RPCError) {
	if a.MemoryRecords == nil {
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
	for _, m := range a.MemoryRecords.List() {
		if m == nil || !m.Retrievable() {
			continue
		}
		if req.Type != "" && m.Type != req.Type {
			continue
		}
		if req.Status != "" && m.Status != req.Status {
			continue
		}
		if req.Scope != "" && m.Scope.Level != req.Scope {
			continue
		}
		if req.Project != "" && !strings.EqualFold(m.Scope.Project, req.Project) {
			continue
		}
		if q != "" {
			blob := strings.ToLower(strings.Join([]string{m.Body, m.Subject, m.Predicate, m.Object, m.Type}, " "))
			if !strings.Contains(blob, q) {
				continue
			}
		}
		out = append(out, recordDTO(m))
		if len(out) >= limit {
			break
		}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (a *App) handleMemoryGet(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "memory id is required"}
	}
	if a.MemoryRecords == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "memory not found"}
	}
	m, err := a.MemoryRecords.Get(req.ID)
	if err != nil || m == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "memory not found"}
	}
	return recordDTO(m), nil
}

func (a *App) handleMemoryRetire(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "memory id is required"}
	}
	if a.MemoryRecords == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "memory record store not configured"}
	}
	m, err := a.MemoryRecords.Get(req.ID)
	if err != nil || m == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "memory not found"}
	}
	m.Retire(clock.NewTime().Time())
	if err := a.MemoryRecords.Save(m); err != nil {
		return nil, rpcInternal(err)
	}
	a.emitMemoryUpdated()
	return recordDTO(m), nil
}
