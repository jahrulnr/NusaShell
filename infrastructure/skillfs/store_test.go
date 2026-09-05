package skillfs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

// newTestStore creates a skillfs Store in a temp dir and seeds one skill
// named "test-skill" with a SKILL.md. Returns the store and its root.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Seed a skill so WriteFile has a target.
	if err := s.Save(&domain.Skill{
		ID:          "test-skill",
		Name:        "test-skill",
		Description: "test",
		Content:     "# Test\n",
		Status:      domain.SkillStatusTrusted,
		Origin:      domain.SkillOriginUser,
	}); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return s
}

func TestStore_SaveNewSkillDerivesIDFromName(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	skill := &domain.Skill{
		Name:        "new-skill",
		Description: "created by the review agent",
		Content:     "# New skill\n",
		Status:      domain.SkillStatusExperimental,
		Origin:      domain.SkillOriginLearned,
	}
	if err := s.Save(skill); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if skill.ID != "new-skill" {
		t.Fatalf("derived ID = %q, want new-skill", skill.ID)
	}
	if _, err := os.Stat(filepath.Join(s.root, "new-skill", "SKILL.md")); err != nil {
		t.Fatalf("new skill folder was not created: %v", err)
	}
}

func TestSeedBuiltinSkillsIncludesAutomationAuthoringTemplates(t *testing.T) {
	root := t.TempDir()
	if err := SeedBuiltinSkills(root); err != nil {
		t.Fatalf("SeedBuiltinSkills: %v", err)
	}
	for _, rel := range []string{
		"SKILL.md",
		"templates/telegram-auto-reply.yaml",
		"templates/alarm-once.yaml",
		"templates/reminder-every.yaml",
		"references/yaml-contract.md",
		"references/event-variables.md",
		"references/tool-discovery.md",
	} {
		path := filepath.Join(root, "automation-authoring", filepath.FromSlash(rel))
		if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
			t.Fatalf("seeded file %s: err=%v bytes=%d", rel, err, len(data))
		}
	}
}

func TestStore_WriteFile_supportFile(t *testing.T) {
	s := newTestStore(t)
	err := s.WriteFile("test-skill", "", "references/errors.md", "# Error recipes\n")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// File must exist on disk.
	data, err := os.ReadFile(filepath.Join(s.root, "test-skill", "references", "errors.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "# Error recipes\n" {
		t.Fatalf("content = %q, want %q", data, "# Error recipes\n")
	}
	// ReadFile must return the same content.
	sf, err := s.ReadFile("test-skill", "", "references/errors.md", 0, 0)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if sf.Content != "# Error recipes\n" {
		t.Fatalf("ReadFile content = %q", sf.Content)
	}
}

func TestStore_WriteFile_defaultSKILLMD(t *testing.T) {
	s := newTestStore(t)
	err := s.WriteFile("test-skill", "", "", "# Updated\n")
	if err != nil {
		t.Fatalf("WriteFile default path: %v", err)
	}
	sf, err := s.ReadFile("test-skill", "", "", 0, 0)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(sf.Content, "# Updated") {
		t.Fatalf("SKILL.md not updated, got %q", sf.Content)
	}
}

func TestStore_WriteFile_pathTraversal_rejected(t *testing.T) {
	s := newTestStore(t)
	err := s.WriteFile("test-skill", "", "../../etc/evil", "pwned")
	if err == nil {
		t.Fatal("expected path traversal rejection, got nil")
	}
}

func TestStore_WriteFile_nonexistentSkill_rejected(t *testing.T) {
	s := newTestStore(t)
	err := s.WriteFile("no-such-skill", "", "references/x.md", "x")
	if err == nil {
		t.Fatal("expected error for nonexistent skill, got nil")
	}
}

func TestStore_WriteFile_overwritesExisting(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteFile("test-skill", "", "references/note.md", "v1"); err != nil {
		t.Fatalf("first WriteFile: %v", err)
	}
	if err := s.WriteFile("test-skill", "", "references/note.md", "v2"); err != nil {
		t.Fatalf("second WriteFile: %v", err)
	}
	sf, err := s.ReadFile("test-skill", "", "references/note.md", 0, 0)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if sf.Content != "v2" {
		t.Fatalf("content = %q, want %q", sf.Content, "v2")
	}
}

// TestStore_GetWithAgentOwner resolves the click-to-read bug: skills created
// by the agent (skill op=save with Origin=agent) live in the root dir, but an
// exact-owner lookup used to fail with "owner agent not mounted" because
// getWithOwner only knew user/builtin/plugin owners.
func TestStore_GetWithLearnedOwner(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Save(&domain.Skill{
		Name:        "mcp-call-json-escaping",
		Description: "created by the background review agent",
		Content:     "# Agent skill\n",
		Status:      domain.SkillStatusExperimental,
		Origin:      domain.SkillOriginLearned,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get("mcp-call-json-escaping", string(domain.SkillOriginLearned))
	if err != nil {
		t.Fatalf("Get(learned owner): %v", err)
	}
	if got.Name != "mcp-call-json-escaping" {
		t.Fatalf("Name = %q", got.Name)
	}

	files, err := s.Files("mcp-call-json-escaping", string(domain.SkillOriginLearned))
	if err != nil {
		t.Fatalf("Files(learned owner): %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("Files returned no entries")
	}

	if _, err := s.Get("mcp-call-json-escaping", ""); err != nil {
		t.Fatalf("Get(priority): %v", err)
	}

	reread, err := New(s.root)
	if err != nil {
		t.Fatalf("New(reload): %v", err)
	}
	got, err = reread.Get("mcp-call-json-escaping", string(domain.SkillOriginLearned))
	if err != nil {
		t.Fatalf("Get(reload): %v", err)
	}
	if got.Origin != domain.SkillOriginLearned {
		t.Fatalf("Origin = %q, want learned (meta.json was not read back)", got.Origin)
	}
	if got.Status != domain.SkillStatusExperimental {
		t.Fatalf("Status = %q, want experimental", got.Status)
	}

	if err := s.Delete("mcp-call-json-escaping", string(domain.SkillOriginLearned)); err != nil {
		t.Fatalf("Delete(learned owner): %v", err)
	}
}

// TestStore_ListSetsAbsolutePath verifies that every skill returned by List
// carries the absolute path to its directory on disk. The model uses this
// path from skill_search/skill_list results to read the SKILL.md and
// support files without needing a separate tool call to resolve the path.
func TestStore_ListSetsAbsolutePath(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Save(&domain.Skill{
		Name:        "path-test",
		Description: "verifies Path is set",
		Content:     "# Path test\n",
		Status:      domain.SkillStatusTrusted,
		Origin:      domain.SkillOriginUser,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, sk := range s.List() {
		if sk.ID != "path-test" {
			continue
		}
		want := filepath.Join(s.root, "path-test")
		if sk.Path != want {
			t.Fatalf("Path = %q, want %q", sk.Path, want)
		}
		// Get must also return the path.
		got, err := s.Get("path-test", "")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Path != want {
			t.Fatalf("Get Path = %q, want %q", got.Path, want)
		}
		return
	}
	t.Fatal("path-test skill not found in List")
}

// TestStore_ListSetsBundledFlag verifies that the Bundled flag is set
// correctly: true when a skill directory has support files beyond
// SKILL.md, false when it only contains SKILL.md. The model uses this
// flag from skill_search/skill_list results to decide whether to
// file_list the skill directory for subfiles (references/, templates/,
// scripts/, examples/) or skip straight to reading SKILL.md.
func TestStore_ListSetsBundledFlag(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Skill with only SKILL.md → Bundled=false
	if err := s.Save(&domain.Skill{
		Name:        "bare-skill",
		Description: "no subfiles",
		Content:     "# Bare\n",
		Status:      domain.SkillStatusTrusted,
		Origin:      domain.SkillOriginUser,
	}); err != nil {
		t.Fatalf("Save bare: %v", err)
	}
	// Skill with a support file → Bundled=true
	if err := s.Save(&domain.Skill{
		Name:        "bundled-skill",
		Description: "has subfiles",
		Content:     "# Bundled\n",
		Status:      domain.SkillStatusTrusted,
		Origin:      domain.SkillOriginUser,
	}); err != nil {
		t.Fatalf("Save bundled: %v", err)
	}
	if err := s.WriteFile("bundled-skill", "", "references/guide.md", "# Guide\n"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, sk := range s.List() {
		switch sk.ID {
		case "bare-skill":
			if sk.Bundled {
				t.Errorf("bare-skill: Bundled=true, want false (only SKILL.md)")
			}
		case "bundled-skill":
			if !sk.Bundled {
				t.Errorf("bundled-skill: Bundled=false, want true (has references/guide.md)")
			}
		}
	}
}

func TestStore_UserSaveSnapshotsAndIncrementsVersion(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	skill := &domain.Skill{
		Name:        "user-skill",
		Description: "curated",
		Content:     "# v1\n",
		Origin:      domain.SkillOriginUser,
	}
	if err := s.Save(skill); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	if skill.Status != domain.SkillStatusTrusted {
		t.Fatalf("Status = %q, want trusted", skill.Status)
	}
	if skill.Version != 1 || skill.ActiveVersion != 1 {
		t.Fatalf("version=%d active=%d", skill.Version, skill.ActiveVersion)
	}
	snap := filepath.Join(s.root, "user-skill", "versions", "1", "SKILL.md")
	if _, err := os.Stat(snap); err != nil {
		t.Fatalf("missing v1 snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.root, "user-skill", "meta.json")); err != nil {
		t.Fatalf("missing meta.json: %v", err)
	}

	skill.Content = "# v2\n"
	if err := s.Save(skill); err != nil {
		t.Fatalf("Save v2: %v", err)
	}
	if skill.Version != 2 || skill.ActiveVersion != 2 {
		t.Fatalf("after update version=%d active=%d", skill.Version, skill.ActiveVersion)
	}
	if _, err := os.Stat(filepath.Join(s.root, "user-skill", "versions", "2", "SKILL.md")); err != nil {
		t.Fatalf("missing v2 snapshot: %v", err)
	}
}

func TestStore_PromoteAndRollback(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Save(&domain.Skill{
		Name:    "learned-flow",
		Content: "# experimental\n",
		Origin:  domain.SkillOriginLearned,
		Status:  domain.SkillStatusExperimental,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(&domain.Skill{
		ID:      "learned-flow",
		Name:    "learned-flow",
		Content: "# experimental v2\n",
		Origin:  domain.SkillOriginLearned,
		Status:  domain.SkillStatusExperimental,
	}); err != nil {
		t.Fatalf("Save v2: %v", err)
	}

	promoted, err := s.Promote("learned-flow", string(domain.SkillOriginLearned))
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if promoted.Status != domain.SkillStatusTrusted {
		t.Fatalf("Promote status = %q", promoted.Status)
	}
	if _, err := s.Promote("learned-flow", string(domain.SkillOriginLearned)); err == nil {
		t.Fatal("second Promote of trusted skill must fail")
	}

	rolled, err := s.Rollback("learned-flow", string(domain.SkillOriginLearned), 1)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rolled.ActiveVersion != 1 {
		t.Fatalf("ActiveVersion = %d, want 1", rolled.ActiveVersion)
	}
	if !strings.Contains(rolled.Content, "experimental") || strings.Contains(rolled.Content, "v2") {
		t.Fatalf("rollback content = %q", rolled.Content)
	}
}

func TestStore_LearnedSaveDoesNotOverwriteTrusted(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Save(&domain.Skill{
		Name:    "git-helper",
		Content: "# curated\n",
		Origin:  domain.SkillOriginUser,
	}); err != nil {
		t.Fatalf("Save user: %v", err)
	}
	learned := &domain.Skill{
		Name:    "git-helper",
		Content: "# learned\n",
		Origin:  domain.SkillOriginLearned,
		Status:  domain.SkillStatusExperimental,
	}
	if err := s.Save(learned); err != nil {
		t.Fatalf("Save learned: %v", err)
	}
	if learned.ID != "learned-git-helper" {
		t.Fatalf("learned ID = %q, want learned-git-helper", learned.ID)
	}
	got, err := s.Get("git-helper", "")
	if err != nil {
		t.Fatalf("Get curated: %v", err)
	}
	if got.Origin != domain.SkillOriginUser {
		t.Fatalf("curated origin = %q", got.Origin)
	}
	if !strings.Contains(got.Content, "curated") {
		t.Fatalf("curated content overwritten: %q", got.Content)
	}
}

func TestStore_OneShotMapsRetiredAgentOrigin(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "old-agent-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: old-agent-skill\n---\n\n# body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := map[string]map[string]any{
		"agent:old-agent-skill": {
			"origin":   "agent",
			"state":    "active",
			"pinned":   true,
			"owned_by": "agent",
		},
	}
	raw, _ := json.Marshal(cache)
	if err := os.WriteFile(filepath.Join(root, "skills.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := s.Get("old-agent-skill", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Origin != domain.SkillOriginLearned {
		t.Fatalf("Origin = %q, want learned", got.Origin)
	}
	if got.Status != domain.SkillStatusExperimental {
		t.Fatalf("Status = %q, want experimental", got.Status)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		t.Fatalf("one-shot must persist meta.json: %v", err)
	}
	metaRaw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metaRaw), `"agent"`) {
		t.Fatalf("meta.json must not keep origin agent: %s", metaRaw)
	}

	reread, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reread.Get("old-agent-skill", string(domain.SkillOriginLearned))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again.Origin != domain.SkillOriginLearned || again.Status != domain.SkillStatusExperimental {
		t.Fatalf("reload origin=%q status=%q", again.Origin, again.Status)
	}
}

func TestSeedBuiltinSkillsWritesTrustedMeta(t *testing.T) {
	root := t.TempDir()
	if err := SeedBuiltinSkills(root); err != nil {
		t.Fatalf("SeedBuiltinSkills: %v", err)
	}
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("automation-authoring", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Origin != domain.SkillOriginBuiltin {
		t.Fatalf("Origin = %q", got.Origin)
	}
	if got.Status != domain.SkillStatusTrusted {
		t.Fatalf("Status = %q", got.Status)
	}
	if !got.Routable() {
		t.Fatal("builtin seed must be routable")
	}
}
