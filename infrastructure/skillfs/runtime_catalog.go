package skillfs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"nusashell/application"
	"nusashell/domain"
)

// External agent skill directories in the wild may use a dotted version
// segment (for example, ltx-2.3-prompt-builder). Managed skills keep the
// stricter store slug validator; the read-only runtime catalog accepts the
// broader safe filename form without permitting path separators.
var runtimeSkillIDRe = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// RuntimeCatalog is the read-only view exposed to agent tool discovery. The
// managed store remains the source of truth for user, learned, builtin, and
// plugin skills; workspace and global packages are loaded directly from
// their source directories and are never hydrated into that store.
type RuntimeCatalog struct {
	managed   application.SkillStore
	globalDir string
}

// NewRuntimeCatalog builds a catalog with the host-wide agent skills root.
// globalDir is expected to be the resolved ~/.agents/skills path.
func NewRuntimeCatalog(managed application.SkillStore, globalDir string) *RuntimeCatalog {
	return &RuntimeCatalog{
		managed:   managed,
		globalDir: cleanRuntimeRoot(globalDir),
	}
}

// List returns one winning, routable-independent row per skill ID. Filtering
// by status is deliberately left to the toolbox so callers can still request
// candidate/experimental rows explicitly.
func (c *RuntimeCatalog) List(workspace string) []*domain.Skill {
	if c == nil {
		return nil
	}
	candidates := c.candidates(workspace)
	winners := make(map[string]*runtimeSkillCandidate, len(candidates))
	for i := range candidates {
		candidate := &candidates[i]
		current, ok := winners[candidate.skill.ID]
		if !ok || runtimeSkillLess(candidate, current) {
			winners[candidate.skill.ID] = candidate
		}
	}

	out := make([]*domain.Skill, 0, len(winners))
	for _, candidate := range winners {
		out = append(out, candidate.skill)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].EffectiveOwnedBy() < out[j].EffectiveOwnedBy()
	})
	return out
}

// Get resolves an exact owner when ownedBy is set; otherwise it returns the
// winner for the active workspace. External owners are read-only by design;
// callers must use the returned absolute Path with file_read to inspect them.
func (c *RuntimeCatalog) Get(workspace, id, ownedBy string) (*domain.Skill, error) {
	if c == nil || !runtimeSkillIDRe.MatchString(id) {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	candidates := c.candidates(workspace)
	if ownedBy != "" {
		for i := range candidates {
			candidate := &candidates[i]
			if candidate.skill.ID == id && candidate.skill.EffectiveOwnedBy() == ownedBy {
				return candidate.skill, nil
			}
		}
		return nil, fmt.Errorf("skill %q not found (owner %s)", id, ownedBy)
	}
	var winner *runtimeSkillCandidate
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.skill.ID != id {
			continue
		}
		if winner == nil || runtimeSkillLess(candidate, winner) {
			winner = candidate
		}
	}
	if winner == nil {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	return winner.skill, nil
}

type runtimeSkillCandidate struct {
	skill *domain.Skill
	owner string
	root  string
}

func (c *RuntimeCatalog) candidates(workspace string) []runtimeSkillCandidate {
	var out []runtimeSkillCandidate
	if c.managed != nil {
		for _, skill := range c.managed.List() {
			if skill == nil || !runtimeSkillIDRe.MatchString(skill.ID) {
				continue
			}
			out = append(out, runtimeSkillCandidate{
				skill: skill,
				owner: skill.EffectiveOwnedBy(),
			})
		}
	}

	workspaceRoot := runtimeWorkspaceSkillsRoot(workspace)
	out = append(out, scanRuntimeRoot(workspaceRoot, domain.SkillOriginWorkspace, string(domain.SkillOriginWorkspace))...)
	out = append(out, scanRuntimeRoot(c.globalDir, domain.SkillOriginGlobal, string(domain.SkillOriginGlobal))...)
	return out
}

// runtimeSkillLess defines the requested source precedence. Managed entries
// not covered by the three external tiers remain available after them, with
// their existing owner ordering as a deterministic fallback.
func runtimeSkillLess(a, b *runtimeSkillCandidate) bool {
	ra, rb := runtimeSkillPriority(a), runtimeSkillPriority(b)
	if ra != rb {
		return ra < rb
	}
	if a.owner != b.owner {
		return a.owner < b.owner
	}
	if a.root != b.root {
		return a.root < b.root
	}
	return a.skill.Path < b.skill.Path
}

func runtimeSkillPriority(candidate *runtimeSkillCandidate) int {
	switch candidate.owner {
	case string(domain.SkillOriginBuiltin):
		return 0
	case string(domain.SkillOriginWorkspace):
		return 1
	case string(domain.SkillOriginGlobal):
		return 2
	}
	// Keep non-source-root managed skills discoverable without letting them
	// override the explicit builtin/workspace/global precedence.
	return 3 + domain.SkillOwnerPriority(candidate.owner)
}

func scanRuntimeRoot(root string, origin domain.SkillOrigin, owner string) []runtimeSkillCandidate {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]runtimeSkillCandidate, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() || !runtimeSkillIDRe.MatchString(entry.Name()) {
			continue
		}
		skill, err := loadRuntimeSkill(root, entry.Name(), origin, owner)
		if err != nil {
			continue
		}
		out = append(out, runtimeSkillCandidate{
			skill: skill,
			owner: owner,
			root:  root,
		})
	}
	return out
}

func loadRuntimeSkill(root, id string, origin domain.SkillOrigin, owner string) (*domain.Skill, error) {
	skillDir := filepath.Join(root, id)
	dirInfo, err := os.Lstat(skillDir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("skillfs: invalid skill directory %s", skillDir)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	info, err := os.Lstat(skillPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("skillfs: invalid skill file %s", skillPath)
	}
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, err
	}
	name, description, content := parseSkillMarkdown(string(data))
	if strings.TrimSpace(name) == "" {
		name = id
	}
	skill := &domain.Skill{
		ID:            id,
		Name:          name,
		Description:   description,
		Content:       content,
		Status:        domain.SkillStatusTrusted,
		Version:       1,
		ActiveVersion: 1,
		Origin:        origin,
		OwnedBy:       owner,
		Path:          skillDir,
		Bundled:       hasSupportFiles(skillDir),
		UpdatedAt:     dirInfo.ModTime(),
	}
	return skill, nil
}

func runtimeWorkspaceSkillsRoot(workspace string) string {
	workspace = cleanRuntimeRoot(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, "skills")
}

func cleanRuntimeRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

var _ application.RuntimeSkillCatalog = (*RuntimeCatalog)(nil)
