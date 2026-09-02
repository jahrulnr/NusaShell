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

// docDTO converts a PrimaryEntry from a memory document (user.md or
// soul.md) to a MemoryEntryDTO for the UI.
func docDTO(e *domain.PrimaryEntry, tier string) contracts.MemoryEntryDTO {
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

// primaryDTO converts a PrimaryEntry to a MemoryEntryDTO for the UI,
// tagged with the legacy "primary" tier label (the user tier).
func primaryDTO(e *domain.PrimaryEntry) contracts.MemoryEntryDTO {
	return docDTO(e, domain.MemoryTierPrimary)
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
	// Primary (user) memory entries (always-injected working set).
	if a.Primary != nil {
		mem := a.Primary.Load()
		for i := range mem.Entries {
			out = append(out, primaryDTO(&mem.Entries[i]))
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

// handleMemoryPrimaryUpdate replaces the always-injected primary memory
// document from the Learning/About You editor. Primary memory is one
// free-form document, so this endpoint deliberately does not expose fragment
// metadata or per-entry delete semantics.
func (a *App) handleMemoryPrimaryUpdate(req contracts.MemoryPrimaryUpdateRequest) (any, *contracts.RPCError) {
	if a.Primary == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "primary memory store not configured"}
	}
	content := strings.TrimSpace(req.Content)
	if len(content) > domain.PrimaryCharCap {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "primary memory cannot exceed 4000 characters"}
	}
	if err := a.Primary.Update([]domain.PrimaryEntry{{Content: content, Source: "user"}}); err != nil {
		return nil, rpcInternal(err)
	}
	mem := a.Primary.Load()
	var entry domain.PrimaryEntry
	if mem != nil && len(mem.Entries) > 0 {
		entry = mem.Entries[0]
	}
	a.emitMemoryUpdated()
	a.publishAnnouncementToAll(newAnnouncement(
		"memory_changed",
		domain.AnnouncementMemoryChangedArgs("primary", "update"),
		domain.AnnouncementMemoryChangedMessage(),
	), "")
	return contracts.MemoryPrimaryUpdateResult{Entry: primaryDTO(&entry)}, nil
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
	if err := a.Agent.Update([]domain.PrimaryEntry{{Content: content, Source: "user"}}); err != nil {
		return nil, rpcInternal(err)
	}
	mem := a.Agent.Load()
	var entry domain.PrimaryEntry
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
	if a.Primary != nil {
		mem := a.Primary.Load()
		q := strings.ToLower(query)
		for i := range mem.Entries {
			if q == "" || strings.Contains(strings.ToLower(mem.Entries[i].Content), q) {
				out = append(out, primaryDTO(&mem.Entries[i]))
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
