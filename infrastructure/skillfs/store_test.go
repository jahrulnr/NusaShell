package skillfs

import (
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
		State:       domain.SkillStateActive,
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
		State:       domain.SkillStateActive,
		Origin:      domain.SkillOriginAgent,
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
func TestStore_GetWithAgentOwner(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Save(&domain.Skill{
		Name:        "mcp-call-json-escaping",
		Description: "created by the background review agent",
		Content:     "# Agent skill\n",
		State:       domain.SkillStateActive,
		Origin:      domain.SkillOriginAgent,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Exact owner lookup — this is what the Skills UI sends on click.
	got, err := s.Get("mcp-call-json-escaping", string(domain.SkillOriginAgent))
	if err != nil {
		t.Fatalf("Get(agent owner): %v", err)
	}
	if got.Name != "mcp-call-json-escaping" {
		t.Fatalf("Name = %q", got.Name)
	}

	// Files listing for the same owner must resolve too (same resolver path).
	files, err := s.Files("mcp-call-json-escaping", string(domain.SkillOriginAgent))
	if err != nil {
		t.Fatalf("Files(agent owner): %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("Files returned no entries")
	}

	// Priority resolution without an explicit owner still works.
	if _, err := s.Get("mcp-call-json-escaping", ""); err != nil {
		t.Fatalf("Get(priority): %v", err)
	}

	// Metadata written under the "agent:<id>" composite key must be read
	// back after a fresh store load (pinned/usage/last_used survive restart).
	reread, err := New(s.root)
	if err != nil {
		t.Fatalf("New(reload): %v", err)
	}
	got, err = reread.Get("mcp-call-json-escaping", string(domain.SkillOriginAgent))
	if err != nil {
		t.Fatalf("Get(reload): %v", err)
	}
	if got.Origin != domain.SkillOriginAgent {
		t.Fatalf("Origin = %q, want agent (metadata under agent:<id> key was not read back)", got.Origin)
	}

	// And deletion via the UI path must succeed for agent-owned skills.
	if err := s.Delete("mcp-call-json-escaping", string(domain.SkillOriginAgent)); err != nil {
		t.Fatalf("Delete(agent owner): %v", err)
	}
}
