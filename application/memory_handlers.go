package application

import (
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// fragmentDTO converts a MemoryFragment to a MemoryEntryDTO for the UI.
func fragmentDTO(f *domain.MemoryFragment) contracts.MemoryEntryDTO {
	dto := contracts.MemoryEntryDTO{
		ID:        f.ID,
		Content:   f.Content,
		Tags:      f.Tags,
		Source:    f.Source,
		CreatedAt: clock.NewTime(f.CreatedAt).Format(timeRFC3339),
		Category:  f.Category,
		Project:   f.Project,
		Task:      f.Task,
		Tier:      "fragment",
	}
	if dto.Source == "" {
		dto.Source = "user"
	}
	return dto
}

// docDTO converts a DocumentEntry from a memory document (user.md or
// soul.md) to a MemoryEntryDTO for the UI.
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

// emitMemoryUpdated publishes a memory.updated event so the Learning UI
// can refresh its memory list, search results, and graph without polling.
func (a *App) emitMemoryUpdated() {
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{"source": "rpc"})
	}
}

func (a *App) handleMemoryList() (any, *contracts.RPCError) {
	out := make([]contracts.MemoryEntryDTO, 0)
	// User memory entries (always-injected working set).
	if a.User != nil {
		mem := a.User.Load()
		for i := range mem.Entries {
			out = append(out, docDTO(&mem.Entries[i], domain.MemoryTierUser))
		}
	}
	// Agent memory entries (always-injected agent working knowledge).
	if a.Agent != nil {
		mem := a.Agent.Load()
		for i := range mem.Entries {
			out = append(out, docDTO(&mem.Entries[i], domain.MemoryTierAgent))
		}
	}
	// Fragments (searchable archive).
	if a.Fragments != nil {
		for _, f := range a.Fragments.List(domain.FragmentSearchFilter{Limit: 500}) {
			out = append(out, fragmentDTO(f))
		}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

// handleMemoryUserUpdate replaces the always-injected user memory
// document from the Learning/About You editor. User memory is one
// free-form document, so this endpoint deliberately does not expose fragment
// metadata or per-entry delete semantics.
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

// handleMemoryAgentUpdate replaces the always-injected agent memory
// document from the Learning/Soul.md editor. Soul memory is one free-form
// document, so this endpoint deliberately does not expose fragment metadata
// or per-entry delete semantics.
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
		domain.AnnouncementMemoryChangedArgs("agent", "update"),
		domain.AnnouncementMemoryChangedMessage(),
	), "")
	return contracts.MemoryAgentUpdateResult{Entry: docDTO(&entry, domain.MemoryTierAgent)}, nil
}

func (a *App) handleMemorySave(req contracts.MemorySaveRequest) (any, *contracts.RPCError) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "memory content is required"}
	}
	if a.Fragments == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "fragment store not configured"}
	}
	category := strings.TrimSpace(req.Category)
	if category == "" {
		category = domain.FragmentCategoryGeneral
	}
	frag := &domain.MemoryFragment{
		Category: category,
		Project:  strings.TrimSpace(req.Project),
		Task:     strings.TrimSpace(req.Task),
		Tags:     req.Tags,
		Content:  content,
		Source:   "user",
	}
	if err := a.Fragments.Save(frag); err != nil {
		return nil, rpcInternal(err)
	}
	a.emitMemoryUpdated()
	a.publishAnnouncementToAll(newAnnouncement(
		"memory_changed",
		domain.AnnouncementMemoryChangedArgs("fragment", "save"),
		domain.AnnouncementMemoryChangedMessage(),
	), "")
	return contracts.MemoryListResult{Entries: []contracts.MemoryEntryDTO{fragmentDTO(frag)}}, nil
}

func (a *App) handleMemorySearch(req contracts.MemorySearchRequest) (any, *contracts.RPCError) {
	query := strings.TrimSpace(req.Query)
	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var out []contracts.MemoryEntryDTO
	// Search fragments via BM25 + metadata filters.
	if a.Fragments != nil {
		hits := a.Fragments.Search(domain.FragmentSearchFilter{
			Query:    query,
			Category: strings.TrimSpace(req.Category),
			Project:  strings.TrimSpace(req.Project),
			Task:     strings.TrimSpace(req.Task),
			Tags:     req.Tags,
			Limit:    limit,
		})
		for _, h := range hits {
			out = append(out, fragmentDTO(h.Fragment))
		}
	}
	// Also include user-tier and agent-tier document entries that match
	// the query (substring).
	if a.User != nil {
		mem := a.User.Load()
		q := strings.ToLower(query)
		for i := range mem.Entries {
			if q == "" || strings.Contains(strings.ToLower(mem.Entries[i].Content), q) {
				out = append(out, docDTO(&mem.Entries[i], domain.MemoryTierUser))
			}
		}
	}
	if a.Agent != nil {
		mem := a.Agent.Load()
		q := strings.ToLower(query)
		for i := range mem.Entries {
			if q == "" || strings.Contains(strings.ToLower(mem.Entries[i].Content), q) {
				out = append(out, docDTO(&mem.Entries[i], domain.MemoryTierAgent))
			}
		}
	}
	if out == nil {
		out = []contracts.MemoryEntryDTO{}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (a *App) handleMemoryDelete(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	if a.Fragments == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "fragment store not configured"}
	}
	if err := a.Fragments.Delete(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	a.emitMemoryUpdated()
	a.publishAnnouncementToAll(newAnnouncement(
		"memory_changed",
		domain.AnnouncementMemoryChangedArgs("fragment", "delete"),
		domain.AnnouncementMemoryChangedMessage(),
	), "")
	return map[string]bool{"ok": true}, nil
}
