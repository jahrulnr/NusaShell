package memorystore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestAgentAutoCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SoulFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("soul.md should not exist yet")
	}
	a, err := NewAgent(dir)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("soul.md not auto-created: %v", err)
	}
	mem := a.Load()
	if len(mem.Entries) != 1 {
		t.Fatalf("new agent document should have 1 entry, got %+v", mem)
	}
	if !strings.Contains(mem.Entries[0].Content, "# About Agent") {
		t.Errorf("new agent document should be seeded from template, got %+v", mem)
	}
	if !strings.HasPrefix(a.Path(), path) {
		t.Errorf("Path = %q, want under %q", a.Path(), path)
	}
}

func TestAgentUpdateEnforcesCap(t *testing.T) {
	dir := t.TempDir()
	a, _ := NewAgent(dir)
	big := strings.Repeat("x", domain.AgentCharCap+1)
	if err := a.Update([]domain.DocumentEntry{{Content: big}}); err == nil {
		t.Error("agent Update should reject content over the agent cap")
	}
}

func TestAgentReplaceAndPersist(t *testing.T) {
	dir := t.TempDir()
	a, _ := NewAgent(dir)
	_ = a.Update([]domain.DocumentEntry{{Content: "convention: gates must be stdlib-only", Source: "agent"}})
	if err := a.Replace("stdlib-only", "stdlib-only or venv"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	a2, err := NewAgent(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded := a2.Load()
	if len(loaded.Entries) != 1 || !strings.Contains(loaded.Entries[0].Content, "venv") {
		t.Errorf("agent doc after replace+reload = %+v", loaded)
	}
	if !strings.HasPrefix(loaded.Entries[0].ID, "agent_") {
		t.Errorf("agent document ID should be agent_ prefixed, got %q", loaded.Entries[0].ID)
	}
}

func TestUserAutoCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, UserFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("user.md should not exist yet")
	}
	p, err := NewUser(dir)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("user.md not auto-created: %v", err)
	}
	mem := p.Load()
	if len(mem.Entries) != 1 {
		t.Fatalf("new user document should have 1 entry, got %+v", mem)
	}
	if !strings.Contains(mem.Entries[0].Content, "# Overview") {
		t.Errorf("new user document should be seeded from template, got %+v", mem)
	}
}

func TestUserUpdateAndLoad(t *testing.T) {
	dir := t.TempDir()
	p, err := NewUser(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := "You are a backend developer living in Jakarta.\n\nYou prefer pragmatic solutions."
	if err := p.Update([]domain.DocumentEntry{{Content: body, Source: "agent"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	loaded := p.Load()
	if len(loaded.Entries) != 1 {
		t.Fatalf("loaded %d entries, want 1 (single document)", len(loaded.Entries))
	}
	if loaded.Entries[0].Content != body {
		t.Errorf("body mismatch:\nwant: %q\ngot:  %q", body, loaded.Entries[0].Content)
	}
}

func TestUserUpdateMergesEntries(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewUser(dir)
	entries := []domain.DocumentEntry{
		{Content: "First paragraph.", Source: "agent"},
		{Content: "Second paragraph.", Source: "agent"},
	}
	if err := p.Update(entries); err != nil {
		t.Fatalf("Update: %v", err)
	}
	loaded := p.Load()
	if len(loaded.Entries) != 1 {
		t.Fatalf("loaded %d entries, want 1", len(loaded.Entries))
	}
	if !strings.Contains(loaded.Entries[0].Content, "First paragraph.") ||
		!strings.Contains(loaded.Entries[0].Content, "Second paragraph.") {
		t.Errorf("merged body missing content: %q", loaded.Entries[0].Content)
	}
	if !strings.Contains(loaded.Entries[0].Content, "\n\n") {
		t.Errorf("merged body should have blank line separator: %q", loaded.Entries[0].Content)
	}
}

func TestUserUpdateEnforcesCap(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewUser(dir)
	big := strings.Repeat("x", domain.UserCharCap+1)
	if err := p.Update([]domain.DocumentEntry{{Content: big}}); err == nil {
		t.Error("Update should reject content over the cap")
	}
}

func TestUserReplace(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewUser(dir)
	_ = p.Update([]domain.DocumentEntry{{Content: "user prefers English", Source: "agent"}})
	if err := p.Replace("English", "user prefers Indonesian"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	loaded := p.Load()
	if !strings.Contains(loaded.Entries[0].Content, "Indonesian") {
		t.Errorf("after replace: %q", loaded.Entries[0].Content)
	}
}

func TestUserReplaceNoMatch(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewUser(dir)
	_ = p.Update([]domain.DocumentEntry{{Content: "hello"}})
	if err := p.Replace("nonexistent", "new"); err == nil {
		t.Error("Replace should fail when no text matches")
	}
}

func TestUserPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	p1, _ := NewUser(dir)
	_ = p1.Update([]domain.DocumentEntry{{Content: "persisted document body"}})

	p2, err := NewUser(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded := p2.Load()
	if len(loaded.Entries) != 1 || loaded.Entries[0].Content != "persisted document body" {
		t.Errorf("reload lost document: %+v", loaded)
	}
}

func TestUserAutoCreateHasYAMLFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p, err := NewUser(dir)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, UserFile))
	if err != nil {
		t.Fatalf("read user.md: %v", err)
	}
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatal("user.md should start with YAML frontmatter delimiter '---'")
	}
	if !strings.Contains(s, "last_updated:") {
		t.Error("frontmatter missing last_updated field")
	}
	if !strings.Contains(s, "version:") {
		t.Error("frontmatter missing version field")
	}
	if !strings.Contains(s, fmt.Sprintf("version: %d", DocVersion)) {
		t.Errorf("frontmatter version should be %d", DocVersion)
	}
	mem := p.Load()
	if len(mem.Entries) != 1 {
		t.Fatalf("auto-created user document should have 1 entry, got %+v", mem)
	}
	if !strings.Contains(mem.Entries[0].Content, "# Overview") {
		t.Errorf("auto-created user document should keep template body, got %+v", mem)
	}
}

func TestSeedProfileDocsDoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, UserFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const existing = "already written by the user\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SeedProfileDocs(dir); err != nil {
		t.Fatalf("SeedProfileDocs: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != existing {
		t.Errorf("existing user.md must not be overwritten, got %q", raw)
	}
}

func TestSeedProfileDocsCopiesMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := SeedProfileDocs(dir); err != nil {
		t.Fatalf("SeedProfileDocs: %v", err)
	}
	userRaw, err := os.ReadFile(filepath.Join(dir, UserFile))
	if err != nil {
		t.Fatalf("user.md missing after seed: %v", err)
	}
	soulRaw, err := os.ReadFile(filepath.Join(dir, SoulFile))
	if err != nil {
		t.Fatalf("soul.md missing after seed: %v", err)
	}
	if !strings.Contains(string(userRaw), "# Overview") {
		t.Errorf("seeded user.md missing template body, got:\n%s", userRaw)
	}
	if !strings.Contains(string(soulRaw), "# About Agent") {
		t.Errorf("seeded soul.md missing template body, got:\n%s", soulRaw)
	}
}

func TestUserUpdateWritesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewUser(dir)
	_ = p.Update([]domain.DocumentEntry{{Content: "test document body"}})
	raw, _ := os.ReadFile(filepath.Join(dir, UserFile))
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatal("updated user.md should start with YAML frontmatter")
	}
	if !strings.Contains(s, "last_updated:") {
		t.Error("updated frontmatter missing last_updated")
	}
	if !strings.Contains(s, "version:") {
		t.Error("updated frontmatter missing version")
	}
	if !strings.Contains(s, "test document body") {
		t.Errorf("updated body missing content, got:\n%s", s)
	}
	if strings.Contains(s, "- [") {
		t.Errorf("format should not contain bullet ID prefix, got:\n%s", s)
	}
}

func TestUserMultiParagraphDocument(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewUser(dir)
	body := "You are a backend developer.\n\nYou prefer Go and clean architecture.\n\nYou work on NusaShell."
	if err := p.Update([]domain.DocumentEntry{{Content: body, Source: "agent"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	loaded := p.Load()
	if len(loaded.Entries) != 1 {
		t.Fatalf("loaded %d entries, want 1 (entire body = 1 entry)", len(loaded.Entries))
	}
	if loaded.Entries[0].Content != body {
		t.Errorf("body should be preserved as-is:\nwant: %q\ngot:  %q", body, loaded.Entries[0].Content)
	}
	if !strings.HasPrefix(loaded.Entries[0].ID, "user_") {
		t.Errorf("ID should be user_ prefixed, got %q", loaded.Entries[0].ID)
	}
}
