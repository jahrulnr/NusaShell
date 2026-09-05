package application

import (
	"encoding/base64"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

func skillDTO(s *domain.Skill) contracts.SkillDTO {
	s.EnsureStatusDefault()
	dto := contracts.SkillDTO{
		ID:            s.ID,
		Name:          s.Name,
		Description:   s.Description,
		Category:      s.Category,
		Status:        string(s.Status),
		Version:       s.Version,
		ActiveVersion: s.ActiveVersion,
		Origin:        string(s.Origin),
		OwnedBy:       s.EffectiveOwnedBy(),
		UsageCount:    s.UsageCount,
		UpdatedAt:     clock.NewTime(s.UpdatedAt).Format(timeRFC3339),
	}
	if !s.LastUsedAt.IsZero() {
		dto.LastUsedAt = clock.NewTime(s.LastUsedAt).Format(timeRFC3339)
	}
	if s.Origin == "" {
		dto.Origin = string(domain.SkillOriginUser)
	}
	return dto
}

// skillDTOsWithShadow marks skills that are shadowed by a higher-priority
// skill with the same ID. Priority: user > builtin > plugin.
func skillDTOsWithShadow(skills []*domain.Skill) []contracts.SkillDTO {
	// Group by ID to detect collisions.
	byID := make(map[string][]*domain.Skill)
	for _, s := range skills {
		byID[s.ID] = append(byID[s.ID], s)
	}
	out := make([]contracts.SkillDTO, 0, len(skills))
	for _, s := range skills {
		dto := skillDTO(s)
		// If there are multiple skills with this ID, mark lower-priority
		// ones as shadowed.
		if candidates := byID[s.ID]; len(candidates) > 1 {
			// Find the highest priority owner.
			bestPriority := domain.SkillOwnerPriority(s.EffectiveOwnedBy())
			for _, c := range candidates {
				if p := domain.SkillOwnerPriority(c.EffectiveOwnedBy()); p < bestPriority {
					bestPriority = p
				}
			}
			if domain.SkillOwnerPriority(s.EffectiveOwnedBy()) > bestPriority {
				dto.Shadowed = true
			}
		}
		out = append(out, dto)
	}
	return out
}

func (a *App) handleSkillsList() (any, *contracts.RPCError) {
	list := a.Skills.List()
	out := skillDTOsWithShadow(list)
	return contracts.SkillsListResult{Skills: out}, nil
}

func (a *App) handleSkillsRead(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	s, err := a.Skills.Get(req.ID, req.OwnedBy)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	full := contracts.SkillFull{SkillDTO: skillDTO(s), Content: s.Content}
	if files, ferr := a.Skills.Files(req.ID, req.OwnedBy); ferr == nil {
		for _, f := range files {
			full.Files = append(full.Files, contracts.SkillFileDTO{
				Path: f.Path, Type: f.Type, SizeBytes: f.SizeBytes, Editable: f.Editable,
			})
		}
	}
	return contracts.SkillReadResult{Skill: full}, nil
}

func (a *App) handleSkillsFileRead(req contracts.SkillFileReadRequest) (any, *contracts.RPCError) {
	if req.ID == "" || req.Path == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "id and path are required"}
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 200_000
	}
	f, err := a.Skills.ReadFile(req.ID, req.OwnedBy, req.Path, req.Offset, maxChars)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return contracts.SkillFileReadResult{
		Content:    f.Content,
		SizeBytes:  f.SizeBytes,
		Truncated:  f.Truncated,
		NextOffset: f.NextOffset,
	}, nil
}

func (a *App) handleSkillsInstall(req contracts.SkillInstallRequest) (any, *contracts.RPCError) {
	if req.Data == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "data is required"}
	}
	zipData, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "invalid base64 data"}
	}
	id, err := a.Skills.Install(zipData)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	skill, _ := a.Skills.Get(id, "")
	name := id
	if skill != nil {
		name = skill.Name
	}
	a.log("info", "skills", "skill installed: %s", id)
	a.publishAnnouncementToAll(newAnnouncement(
		"skills_changed",
		domain.AnnouncementSkillsChangedArgs("install"),
		domain.AnnouncementSkillsChangedMessage(),
	), "")
	return contracts.SkillInstallResult{ID: id, Name: name}, nil
}

func (a *App) handleSkillsSave(req contracts.SkillSaveRequest) (any, *contracts.RPCError) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill name is required"}
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill content is required"}
	}
	// When path is set, write a support file inside an existing skill.
	if path := strings.TrimSpace(req.Path); path != "" {
		if err := a.Skills.WriteFile(name, "", path, req.Content); err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		a.log("info", "skills", "skill file saved: %s/%s", name, path)
		a.publishAnnouncementToAll(newAnnouncement(
			"skills_changed",
			domain.AnnouncementSkillsChangedArgs("save"),
			domain.AnnouncementSkillsChangedMessage(),
		), "")
		return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: contracts.SkillDTO{ID: name, Name: name}}}, nil
	}
	var s *domain.Skill
	if req.ID != "" {
		existing, err := a.Skills.Get(req.ID, "")
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		s = existing
	} else {
		s = &domain.Skill{
			ID:     domain.SkillSlug(name),
			Status: domain.SkillStatusTrusted,
			Origin: domain.SkillOriginUser,
		}
	}
	s.Name = name
	s.Description = strings.TrimSpace(req.Description)
	s.Content = req.Content
	s.UpdatedAt = clock.NewTime().Time()
	if err := a.Skills.Save(s); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "skills", "skill saved: %s", s.Name)
	a.publishAnnouncementToAll(newAnnouncement(
		"skills_changed",
		domain.AnnouncementSkillsChangedArgs("save"),
		domain.AnnouncementSkillsChangedMessage(),
	), "")
	return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: skillDTO(s), Content: s.Content}}, nil
}

func (a *App) handleSkillsDelete(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	if _, err := a.Skills.Get(req.ID, req.OwnedBy); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if err := a.Skills.Delete(req.ID, req.OwnedBy); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "skills", "skill deleted: %s", req.ID)
	a.publishAnnouncementToAll(newAnnouncement(
		"skills_changed",
		domain.AnnouncementSkillsChangedArgs("delete"),
		domain.AnnouncementSkillsChangedMessage(),
	), "")
	return map[string]bool{"ok": true}, nil
}

func (a *App) handleSkillsPromote(req contracts.SkillPromoteRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill id is required"}
	}
	s, err := a.Skills.Promote(req.ID, req.OwnedBy)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	// Promotion is a human decision, not a model run: no transcript to link.
	a.emitSkillLifecycle("promote", s.ID, string(s.Status), "")
	return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: skillDTO(s), Content: s.Content}}, nil
}

func (a *App) handleSkillsRollback(req contracts.SkillRollbackRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" || req.Version < 1 {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill id and version are required"}
	}
	s, err := a.Skills.Rollback(req.ID, req.OwnedBy, req.Version)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventSkillUpdated, map[string]any{"id": s.ID, "status": s.Status, "active_version": s.ActiveVersion})
	}
	return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: skillDTO(s), Content: s.Content}}, nil
}
