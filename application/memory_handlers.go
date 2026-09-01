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

// primaryDTO converts a PrimaryEntry to a MemoryEntryDTO for the UI.
func primaryDTO(e *domain.PrimaryEntry) contracts.MemoryEntryDTO {
	dto := contracts.MemoryEntryDTO{
		ID:        e.ID,
		Content:   e.Content,
		Source:    e.Source,
		CreatedAt: clock.NewTime(e.UpdatedAt).Format(timeRFC3339),
		Tier:      "primary",
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
	// Primary memory entries (always-injected working set).
	if a.Primary != nil {
		mem := a.Primary.Load()
		for i := range mem.Entries {
			out = append(out, primaryDTO(&mem.Entries[i]))
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
	// Also include primary memory entries that match the query (substring).
	if a.Primary != nil {
		mem := a.Primary.Load()
		q := strings.ToLower(query)
		for i := range mem.Entries {
			if q == "" || strings.Contains(strings.ToLower(mem.Entries[i].Content), q) {
				out = append(out, primaryDTO(&mem.Entries[i]))
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
