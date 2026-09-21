package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// executeSkillFamily runs the skill root's ops. It is reached only through
// executeFamily, which resolves and validates the op via
// application.DispatchOp before delegating here.
func (t *Toolbox) executeSkillFamily(ctx context.Context, op string, argsJSON []byte) (string, error) {
	switch op {
	case "list":
		var args struct {
			Limit  int    `json:"limit"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 100
		}
		skills := filterSkills(t.skillsForContext(ctx), args.Status)
		if limit < len(skills) {
			skills = skills[:limit]
		}
		items := make([]any, 0, len(skills))
		for _, s := range skills {
			items = append(items, formatSkillJSON(s))
		}
		return yamlJSONL(map[string]any{"count": len(skills)}, items), nil

	case "search":
		var args struct {
			Query  string `json:"query"`
			Limit  int    `json:"limit"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		if t.SkillSearcher != nil {
			return t.searchSkillsRanked(ctx, args.Query, limit, args.Status)
		}
		q := strings.ToLower(args.Query)
		var items []any
		for _, s := range filterSkills(t.skillsForContext(ctx), args.Status) {
			if !strings.Contains(strings.ToLower(s.Name+" "+s.Description+" "+s.Content), q) {
				continue
			}
			items = append(items, formatSkillJSON(s))
			if len(items) >= limit {
				break
			}
		}
		return yamlJSONL(map[string]any{"count": len(items)}, items), nil

	case "save":
		var args struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
			Content     string `json:"content"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return "", fmt.Errorf("skill name is required")
		}
		if strings.TrimSpace(args.Content) == "" {
			return "", fmt.Errorf("skill content is required")
		}
		rel, support, err := domain.SkillSaveSupportPath(args.Path)
		if err != nil {
			return "", fmt.Errorf("skill save: %w", err)
		}
		if support {
			lookup := name
			if id := strings.TrimSpace(args.ID); id != "" {
				lookup = id
			}
			existing, err := t.skillForContext(ctx, lookup, "")
			if err != nil {
				return "", fmt.Errorf("skill save: skill %q not found; omit path to create SKILL.md, or pass the existing skill id to write a support file", lookup)
			}
			if !existing.CanAgentMutate() {
				return "", skillMutationError(existing, lookup)
			}
			if t.Skills == nil {
				return "", depMissing("skill store not configured")
			}
			if err := t.Skills.WriteFile(lookup, "", rel, args.Content); err != nil {
				return "", fmt.Errorf("skill save: %w", err)
			}
			return yamlBlock(map[string]any{"status": "saved"}), nil
		}
		var s *domain.Skill
		if args.ID != "" {
			existing, err := t.skillForContext(ctx, args.ID, "")
			if err != nil {
				return "", fmt.Errorf("skill %q not found: %w", args.ID, err)
			}
			if !existing.CanAgentMutate() {
				return "", skillMutationError(existing, args.ID)
			}
			s = existing
		} else if existing, err := t.skillForContext(ctx, name, ""); err == nil {
			if !existing.CanAgentMutate() {
				return "", skillMutationError(existing, name)
			}
			s = existing
		} else {
			s = &domain.Skill{
				Origin:        domain.SkillOriginLearned,
				Status:        domain.SkillStatusExperimental,
				OwnedBy:       string(domain.SkillOriginLearned),
				Version:       1,
				ActiveVersion: 1,
			}
		}
		s.Name = name
		s.Description = strings.TrimSpace(args.Description)
		s.Content = args.Content
		s.UpdatedAt = clock.NewTime().Time()
		if t.Skills == nil {
			return "", depMissing("skill store not configured")
		}
		if err := t.Skills.Save(s); err != nil {
			return "", err
		}
		return yamlBlock(map[string]any{"status": "saved", "id": s.ID}), nil

	case "delete":
		var args struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return "", fmt.Errorf("skill id is required")
		}
		skill, err := t.skillForContext(ctx, id, args.OwnedBy)
		if err != nil {
			return "", err
		}
		if isExternalSkill(skill) {
			return "", skillMutationError(skill, id)
		}
		if skill.Origin != domain.SkillOriginLearned || (skill.Status != domain.SkillStatusCandidate && skill.Status != domain.SkillStatusExperimental) {
			return "", fmt.Errorf("only learned candidate/experimental skills can be deleted")
		}
		if t.Skills == nil {
			return "", depMissing("skill store not configured")
		}
		if err := t.Skills.Delete(id, args.OwnedBy); err != nil {
			return "", err
		}
		return yamlBlock(map[string]any{"status": "deleted", "id": id}), nil

	default:
		return "", fmt.Errorf("unknown %s op %q", "skill_"+op, op)
	}
}

func (t *Toolbox) skillsForContext(ctx context.Context) []*domain.Skill {
	if t.RuntimeSkills != nil {
		return t.RuntimeSkills.List(application.WorkspaceFromContext(ctx))
	}
	if t.Skills == nil {
		return nil
	}
	return t.Skills.List()
}

func (t *Toolbox) skillForContext(ctx context.Context, id, ownedBy string) (*domain.Skill, error) {
	if t.RuntimeSkills != nil {
		return t.RuntimeSkills.Get(application.WorkspaceFromContext(ctx), id, ownedBy)
	}
	if t.Skills == nil {
		return nil, depMissing("skill store not configured")
	}
	return t.Skills.Get(id, ownedBy)
}

func isExternalSkill(skill *domain.Skill) bool {
	if skill == nil {
		return false
	}
	switch skill.EffectiveOwnedBy() {
	case string(domain.SkillOriginWorkspace), string(domain.SkillOriginGlobal):
		return true
	default:
		return false
	}
}

func skillMutationError(skill *domain.Skill, id string) error {
	if isExternalSkill(skill) {
		return fmt.Errorf("%s skill %q is read-only; edit its source SKILL.md", skill.EffectiveOwnedBy(), id)
	}
	return fmt.Errorf("cannot mutate trusted curated skill %q", id)
}

// searchSkillsRanked runs the ranked skill search (BM25 + graph + recency,
// no embedding) and appends substring matches the ranker missed (plural or
// inflected forms) so recall never regresses below the plain matcher.
func (t *Toolbox) searchSkillsRanked(ctx context.Context, query string, limit int, status string) (string, error) {
	results, err := t.SkillSearcher.SearchSkills(ctx, query, limit)
	if err != nil {
		return "", fmt.Errorf("skill search: %w", err)
	}
	skills := filterSkills(t.skillsForContext(ctx), status)
	byKey := make(map[string]*domain.Skill, len(skills))
	for _, sk := range skills {
		byKey[sk.ID] = sk
		if _, ok := byKey[sk.Name]; !ok {
			byKey[sk.Name] = sk
		}
	}
	seen := make(map[string]bool, len(results))
	items := make([]any, 0, limit)
	for _, r := range results {
		sk := byKey[r.ID]
		if sk == nil || seen[sk.ID] {
			continue
		}
		seen[sk.ID] = true
		items = append(items, formatSkillJSON(sk))
		if len(items) >= limit {
			break
		}
	}
	if len(items) < limit {
		q := strings.ToLower(query)
		for _, sk := range skills {
			if len(items) >= limit {
				break
			}
			if seen[sk.ID] {
				continue
			}
			if strings.Contains(strings.ToLower(sk.Name+" "+sk.Description+" "+sk.Content), q) {
				seen[sk.ID] = true
				items = append(items, formatSkillJSON(sk))
			}
		}
	}
	return yamlJSONL(map[string]any{"count": len(items)}, items), nil
}

// formatSkillJSON renders a skill as discovery metadata for list/search.
func formatSkillJSON(s *domain.Skill) map[string]any {
	return map[string]any{
		"id":          s.ID,
		"name":        s.Name,
		"description": s.Description,
		"owned_by":    s.EffectiveOwnedBy(),
		"status":      string(s.Status),
		"path":        s.Path,
		"bundled":     s.Bundled,
	}
}

func filterSkills(skills []*domain.Skill, status string) []*domain.Skill {
	status = strings.TrimSpace(status)
	out := make([]*domain.Skill, 0, len(skills))
	for _, s := range skills {
		if s == nil {
			continue
		}
		if status != "" {
			if string(s.Status) == status {
				out = append(out, s)
			}
			continue
		}
		if s.Routable() {
			out = append(out, s)
		}
	}
	return out
}
