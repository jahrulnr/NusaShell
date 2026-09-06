package skills

import (
	"encoding/base64"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/rpcdispatch"
	clock "nusashell/pkg/time"
)

func toDTO(s *domain.Skill) contracts.SkillDTO {
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
		UpdatedAt:     clock.NewTime(s.UpdatedAt).RFC3339(),
	}
	if !s.LastUsedAt.IsZero() {
		dto.LastUsedAt = clock.NewTime(s.LastUsedAt).RFC3339()
	}
	if s.Origin == "" {
		dto.Origin = string(domain.SkillOriginUser)
	}
	return dto
}

func dtosWithShadow(list []*domain.Skill) []contracts.SkillDTO {
	byID := make(map[string][]*domain.Skill)
	for _, s := range list {
		byID[s.ID] = append(byID[s.ID], s)
	}
	out := make([]contracts.SkillDTO, 0, len(list))
	for _, s := range list {
		dto := toDTO(s)
		if candidates := byID[s.ID]; len(candidates) > 1 {
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

func (svc *Service) HandleList() (any, *contracts.RPCError) {
	if svc.store == nil {
		return contracts.SkillsListResult{Skills: []contracts.SkillDTO{}}, nil
	}
	return contracts.SkillsListResult{Skills: dtosWithShadow(svc.store.List())}, nil
}

func (svc *Service) HandleRead(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	s, err := svc.store.Get(req.ID, req.OwnedBy)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	full := contracts.SkillFull{SkillDTO: toDTO(s), Content: s.Content}
	if files, ferr := svc.store.Files(req.ID, req.OwnedBy); ferr == nil {
		for _, f := range files {
			full.Files = append(full.Files, contracts.SkillFileDTO{
				Path: f.Path, Type: f.Type, SizeBytes: f.SizeBytes, Editable: f.Editable,
			})
		}
	}
	return contracts.SkillReadResult{Skill: full}, nil
}

func (svc *Service) HandleFileRead(req contracts.SkillFileReadRequest) (any, *contracts.RPCError) {
	if req.ID == "" || req.Path == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "id and path are required"}
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 200_000
	}
	f, err := svc.store.ReadFile(req.ID, req.OwnedBy, req.Path, req.Offset, maxChars)
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

func (svc *Service) HandleInstall(req contracts.SkillInstallRequest) (any, *contracts.RPCError) {
	if req.Data == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "data is required"}
	}
	zipData, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "invalid base64 data"}
	}
	id, err := svc.store.Install(zipData)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	skill, _ := svc.store.Get(id, "")
	name := id
	if skill != nil {
		name = skill.Name
	}
	svc.write("skill installed: %s", id)
	svc.changed("install")
	return contracts.SkillInstallResult{ID: id, Name: name}, nil
}

func (svc *Service) HandleSave(req contracts.SkillSaveRequest) (any, *contracts.RPCError) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill name is required"}
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill content is required"}
	}
	rel, support, err := domain.SkillSaveSupportPath(req.Path)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if support {
		lookup := name
		if id := strings.TrimSpace(req.ID); id != "" {
			lookup = id
		}
		if err := svc.store.WriteFile(lookup, "", rel, req.Content); err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		svc.write("skill file saved: %s/%s", lookup, rel)
		svc.changed("save")
		return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: contracts.SkillDTO{ID: lookup, Name: name}}}, nil
	}
	var s *domain.Skill
	if req.ID != "" {
		existing, err := svc.store.Get(req.ID, "")
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
	if err := svc.store.Save(s); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	svc.write("skill saved: %s", s.Name)
	svc.changed("save")
	return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: toDTO(s), Content: s.Content}}, nil
}

func (svc *Service) HandleDelete(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	if _, err := svc.store.Get(req.ID, req.OwnedBy); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if err := svc.store.Delete(req.ID, req.OwnedBy); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	svc.write("skill deleted: %s", req.ID)
	svc.changed("delete")
	return map[string]bool{"ok": true}, nil
}

func (svc *Service) HandlePromote(req contracts.SkillPromoteRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill id is required"}
	}
	s, err := svc.store.Promote(req.ID, req.OwnedBy)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if svc.onLife != nil {
		svc.onLife("promote", s.ID, string(s.Status))
	}
	return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: toDTO(s), Content: s.Content}}, nil
}

func (svc *Service) HandleRollback(req contracts.SkillRollbackRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" || req.Version < 1 {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill id and version are required"}
	}
	s, err := svc.store.Rollback(req.ID, req.OwnedBy, req.Version)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if svc.bus != nil {
		svc.bus.Emit(contracts.EventSkillUpdated, map[string]any{"id": s.ID, "status": s.Status, "active_version": s.ActiveVersion})
	}
	return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: toDTO(s), Content: s.Content}}, nil
}
