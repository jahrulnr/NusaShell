package skillfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestRuntimeCatalogResolvesBuiltinWorkspaceGlobalPriority(t *testing.T) {
	managed, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := managed.Save(&domain.Skill{
		ID:          "shared-skill",
		Name:        "shared-skill",
		Description: "builtin description",
		Content:     "# builtin\n",
		Origin:      domain.SkillOriginBuiltin,
		Status:      domain.SkillStatusTrusted,
	}); err != nil {
		t.Fatalf("Save builtin: %v", err)
	}

	workspace := t.TempDir()
	workspaceSkills := filepath.Join(workspace, "skills")
	globalSkills := filepath.Join(t.TempDir(), "skills")
	writeRuntimeSkill(t, workspaceSkills, "shared-skill", "workspace description", "# workspace\n")
	writeRuntimeSkill(t, globalSkills, "shared-skill", "global description", "# global\n")
	writeRuntimeSkill(t, workspaceSkills, "workspace-wins", "workspace description", "# workspace\n")
	writeRuntimeSkill(t, globalSkills, "workspace-wins", "global description", "# global\n")
	writeRuntimeSkill(t, globalSkills, "global-only", "global description", "# global-only\n")

	catalog := NewRuntimeCatalog(managed, globalSkills)
	got := catalog.List(workspace)
	byID := make(map[string]*domain.Skill, len(got))
	for _, skill := range got {
		if _, exists := byID[skill.ID]; exists {
			t.Fatalf("duplicate runtime skill id %q in list", skill.ID)
		}
		byID[skill.ID] = skill
	}

	if skill := byID["shared-skill"]; skill == nil || skill.EffectiveOwnedBy() != "builtin" || !strings.Contains(skill.Content, "builtin") {
		t.Fatalf("builtin winner = %+v, want builtin content", skill)
	}
	if skill := byID["workspace-wins"]; skill == nil || skill.EffectiveOwnedBy() != "workspace" || !strings.Contains(skill.Content, "workspace") {
		t.Fatalf("workspace winner = %+v, want workspace content", skill)
	}
	if skill := byID["global-only"]; skill == nil || skill.EffectiveOwnedBy() != "global" {
		t.Fatalf("global-only winner = %+v, want global owner", skill)
	}

	workspaceSkill, err := catalog.Get(workspace, "shared-skill", "workspace")
	if err != nil {
		t.Fatalf("Get exact workspace owner: %v", err)
	}
	if workspaceSkill.EffectiveOwnedBy() != "workspace" || !strings.Contains(workspaceSkill.Content, "workspace") {
		t.Fatalf("exact workspace skill = %+v", workspaceSkill)
	}
	globalSkill, err := catalog.Get(workspace, "shared-skill", "global")
	if err != nil {
		t.Fatalf("Get exact global owner: %v", err)
	}
	if globalSkill.EffectiveOwnedBy() != "global" || !strings.Contains(globalSkill.Content, "global") {
		t.Fatalf("exact global skill = %+v", globalSkill)
	}

	if _, err := os.Stat(filepath.Join(workspaceSkills, "shared-skill", "meta.json")); !os.IsNotExist(err) {
		t.Fatalf("workspace source must remain read-only; meta.json stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(globalSkills, "shared-skill", "meta.json")); !os.IsNotExist(err) {
		t.Fatalf("global source must remain read-only; meta.json stat error = %v", err)
	}
}

func TestRuntimeCatalogSkipsInvalidAndUnsafePackages(t *testing.T) {
	managed, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	workspace := t.TempDir()
	root := filepath.Join(workspace, "skills")
	writeRuntimeSkill(t, root, "valid-skill", "valid", "# valid\n")
	writeRuntimeSkill(t, root, "versioned-2.3", "versioned", "# versioned\n")
	writeRuntimeSkill(t, root, "BadSkill", "invalid", "# invalid\n")
	if err := os.MkdirAll(filepath.Join(root, "broken-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "symlink-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "symlink-skill", "SKILL.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := NewRuntimeCatalog(managed, "").List(workspace)
	if len(got) != 2 {
		t.Fatalf("runtime skills = %+v, want valid-skill and versioned-2.3", got)
	}
	for _, skill := range got {
		if skill.ID != "valid-skill" && skill.ID != "versioned-2.3" {
			t.Fatalf("unexpected runtime skill = %+v", skill)
		}
	}
}

func TestRuntimeCatalogManagedSkillsRemainFallbackCandidates(t *testing.T) {
	managed, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := managed.Save(&domain.Skill{
		ID:      "fallback-skill",
		Name:    "fallback-skill",
		Content: "# managed\n",
		Origin:  domain.SkillOriginUser,
	}); err != nil {
		t.Fatalf("Save managed: %v", err)
	}
	workspace := t.TempDir()
	writeRuntimeSkill(t, filepath.Join(workspace, "skills"), "fallback-skill", "workspace", "# workspace\n")

	got, err := NewRuntimeCatalog(managed, "").Get(workspace, "fallback-skill", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EffectiveOwnedBy() != "workspace" {
		t.Fatalf("winner owner = %q, want workspace", got.EffectiveOwnedBy())
	}
}

func writeRuntimeSkill(t *testing.T, root, id, description, body string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", id, description, body)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}
