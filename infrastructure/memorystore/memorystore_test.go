package memorystore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func TestPrimaryAutoCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PrimaryFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("MEMORY.md should not exist yet")
	}
	p, err := NewPrimary(dir)
	if err != nil {
		t.Fatalf("NewPrimary: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("MEMORY.md not auto-created: %v", err)
	}
	if mem := p.Load(); len(mem.Entries) != 0 {
		t.Errorf("new primary should be empty, got %d entries", len(mem.Entries))
	}
}

func TestPrimaryUpdateAndLoad(t *testing.T) {
	dir := t.TempDir()
	p, err := NewPrimary(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := []domain.PrimaryEntry{
		{ID: "frag_1", Content: "user prefers Indonesian", Source: "agent"},
		{ID: "frag_2", Content: "repo uses Go + Clean Architecture", Source: "agent"},
	}
	if err := p.Update(entries); err != nil {
		t.Fatalf("Update: %v", err)
	}
	loaded := p.Load()
	if len(loaded.Entries) != 2 {
		t.Fatalf("loaded %d entries, want 2", len(loaded.Entries))
	}
	if loaded.Entries[0].Content != "user prefers Indonesian" {
		t.Errorf("entry 0 = %q", loaded.Entries[0].Content)
	}
}

func TestPrimaryUpdateEnforcesCap(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewPrimary(dir)
	big := strings.Repeat("x", domain.PrimaryCharCap+1)
	if err := p.Update([]domain.PrimaryEntry{{ID: "x", Content: big}}); err == nil {
		t.Error("Update should reject content over the cap")
	}
}

func TestPrimaryReplace(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewPrimary(dir)
	_ = p.Update([]domain.PrimaryEntry{
		{ID: "frag_1", Content: "user prefers English", Source: "agent"},
	})
	if err := p.Replace("English", "user prefers Indonesian"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	loaded := p.Load()
	if !strings.Contains(loaded.Entries[0].Content, "Indonesian") {
		t.Errorf("after replace: %q", loaded.Entries[0].Content)
	}
}

func TestPrimaryReplaceNoMatch(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewPrimary(dir)
	_ = p.Update([]domain.PrimaryEntry{{ID: "x", Content: "hello"}})
	if err := p.Replace("nonexistent", "new"); err == nil {
		t.Error("Replace should fail when no entry matches")
	}
}

func TestPrimaryPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	p1, _ := NewPrimary(dir)
	_ = p1.Update([]domain.PrimaryEntry{{ID: "frag_1", Content: "persisted fact"}})

	p2, err := NewPrimary(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded := p2.Load()
	if len(loaded.Entries) != 1 || loaded.Entries[0].Content != "persisted fact" {
		t.Errorf("reload lost entries: %+v", loaded.Entries)
	}
}

func TestPrimaryAutoCreateHasYAMLFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p, err := NewPrimary(dir)
	if err != nil {
		t.Fatalf("NewPrimary: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, PrimaryFile))
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatal("MEMORY.md should start with YAML frontmatter delimiter '---'")
	}
	if !strings.Contains(s, "last_updated:") {
		t.Error("frontmatter missing last_updated field")
	}
	if !strings.Contains(s, "version:") {
		t.Error("frontmatter missing version field")
	}
	if !strings.Contains(s, fmt.Sprintf("version: %d", PrimaryVersion)) {
		t.Errorf("frontmatter version should be %d", PrimaryVersion)
	}
	if len(p.Load().Entries) != 0 {
		t.Errorf("auto-created primary should be empty, got %d entries", len(p.Load().Entries))
	}
}

func TestPrimaryUpdateWritesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewPrimary(dir)
	_ = p.Update([]domain.PrimaryEntry{{ID: "frag_1", Content: "test fact"}})
	raw, _ := os.ReadFile(filepath.Join(dir, PrimaryFile))
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatal("updated MEMORY.md should start with YAML frontmatter")
	}
	if !strings.Contains(s, "last_updated:") {
		t.Error("updated frontmatter missing last_updated")
	}
	if !strings.Contains(s, "version:") {
		t.Error("updated frontmatter missing version")
	}
	if !strings.Contains(s, "- [frag_1] test fact") {
		t.Errorf("updated body missing entry, got:\n%s", s)
	}
}

func TestFragmentsAutoCreatesDir(t *testing.T) {
	dir := t.TempDir()
	fragDir := filepath.Join(dir, FragmentsDir)
	if _, err := os.Stat(fragDir); !os.IsNotExist(err) {
		t.Fatal("fragments dir should not exist yet")
	}
	f, err := NewFragments(dir)
	if err != nil {
		t.Fatalf("NewFragments: %v", err)
	}
	if _, err := os.Stat(fragDir); err != nil {
		t.Fatalf("fragments dir not auto-created: %v", err)
	}
	if frags := f.List(domain.FragmentSearchFilter{}); len(frags) != 0 {
		t.Errorf("new fragments store should be empty, got %d", len(frags))
	}
}

func TestFragmentsSaveAndGet(t *testing.T) {
	dir := t.TempDir()
	f, _ := NewFragments(dir)
	frag := &domain.MemoryFragment{
		Category: domain.FragmentCategoryProject,
		Project:  "nusashell",
		Tags:     []string{"go", "arch"},
		Content:  "Repo uses Clean Architecture with strict layer deps.",
		Source:   "agent",
	}
	if err := f.Save(frag); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if frag.ID == "" {
		t.Fatal("Save did not assign an ID")
	}
	loaded := f.Get(frag.ID)
	if loaded == nil {
		t.Fatal("Get returned nil after Save")
	}
	if loaded.Content != frag.Content || loaded.Project != "nusashell" {
		t.Errorf("loaded = %+v", loaded)
	}
	if loaded.CreatedAt.IsZero() || loaded.UpdatedAt.IsZero() {
		t.Error("timestamps not set")
	}
}

func TestFragmentsSavePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	f1, _ := NewFragments(dir)
	_ = f1.Save(&domain.MemoryFragment{Category: "user", Content: "prefers dark mode"})

	f2, err := NewFragments(dir)
	if err != nil {
		t.Fatal(err)
	}
	frags := f2.List(domain.FragmentSearchFilter{})
	if len(frags) != 1 || frags[0].Content != "prefers dark mode" {
		t.Errorf("reload lost fragments: %+v", frags)
	}
}

func TestFragmentsDelete(t *testing.T) {
	dir := t.TempDir()
	f, _ := NewFragments(dir)
	frag := &domain.MemoryFragment{Content: "to be deleted"}
	_ = f.Save(frag)
	if err := f.Delete(frag.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if f.Get(frag.ID) != nil {
		t.Error("Get returned fragment after Delete")
	}
	if err := f.Delete(frag.ID); err == nil {
		t.Error("Delete should fail for missing fragment")
	}
}

func TestFragmentsListMetadataFilter(t *testing.T) {
	dir := t.TempDir()
	f, _ := NewFragments(dir)
	_ = f.Save(&domain.MemoryFragment{Category: "project", Project: "A", Content: "alpha"})
	_ = f.Save(&domain.MemoryFragment{Category: "user", Content: "beta"})
	_ = f.Save(&domain.MemoryFragment{Category: "project", Project: "B", Tags: []string{"x"}, Content: "gamma"})

	got := f.List(domain.FragmentSearchFilter{Category: "project"})
	if len(got) != 2 {
		t.Errorf("category=project: got %d, want 2", len(got))
	}
	got = f.List(domain.FragmentSearchFilter{Project: "A"})
	if len(got) != 1 || got[0].Content != "alpha" {
		t.Errorf("project=A: %+v", got)
	}
	got = f.List(domain.FragmentSearchFilter{Tags: []string{"x"}})
	if len(got) != 1 || got[0].Content != "gamma" {
		t.Errorf("tags=x: %+v", got)
	}
}

func TestFragmentsSearchBM25(t *testing.T) {
	dir := t.TempDir()
	f, _ := NewFragments(dir)
	_ = f.Save(&domain.MemoryFragment{Category: "project", Content: "The repo uses Go and Clean Architecture."})
	_ = f.Save(&domain.MemoryFragment{Category: "user", Content: "User prefers Indonesian language."})
	_ = f.Save(&domain.MemoryFragment{Category: "general", Content: "Random unrelated note."})

	hits := f.Search(domain.FragmentSearchFilter{Query: "Go architecture", Limit: 5})
	if len(hits) == 0 {
		t.Fatal("Search returned no hits")
	}
	if !strings.Contains(strings.ToLower(hits[0].Fragment.Content), "go") {
		t.Errorf("top hit should be the Go/Architecture fragment, got %q", hits[0].Fragment.Content)
	}
	if hits[0].Score <= 0 {
		t.Error("top hit should have a positive BM25 score")
	}
}

func TestFragmentsSearchWithMetadataFilter(t *testing.T) {
	dir := t.TempDir()
	f, _ := NewFragments(dir)
	_ = f.Save(&domain.MemoryFragment{Category: "project", Project: "A", Content: "Go architecture notes"})
	_ = f.Save(&domain.MemoryFragment{Category: "project", Project: "B", Content: "Go modules and packages"})
	_ = f.Save(&domain.MemoryFragment{Category: "user", Content: "Go is the preferred language"})

	hits := f.Search(domain.FragmentSearchFilter{Query: "Go", Category: "project", Limit: 10})
	if len(hits) != 2 {
		t.Fatalf("project+Go: got %d hits, want 2", len(hits))
	}
	for _, h := range hits {
		if h.Fragment.Category != "project" {
			t.Errorf("hit category = %q, want project", h.Fragment.Category)
		}
	}
}

func TestFragmentsSearchEmptyQueryReturnsAllFiltered(t *testing.T) {
	dir := t.TempDir()
	f, _ := NewFragments(dir)
	_ = f.Save(&domain.MemoryFragment{Category: "user", Content: "a"})
	_ = f.Save(&domain.MemoryFragment{Category: "user", Content: "b"})
	_ = f.Save(&domain.MemoryFragment{Category: "project", Content: "c"})

	hits := f.Search(domain.FragmentSearchFilter{Category: "user"})
	if len(hits) != 2 {
		t.Errorf("empty query + category=user: got %d, want 2", len(hits))
	}
	for _, h := range hits {
		if h.Score != 0 {
			t.Error("empty query hits should have Score 0")
		}
	}
}

func TestParseFragmentRoundTrip(t *testing.T) {
	original := &domain.MemoryFragment{
		ID:        "frag_test",
		Category:  "task",
		Project:   "nusashell",
		Task:      "memory-tier",
		Tags:      []string{"go", "arch"},
		Source:    "agent",
		Content:   "Two-tier memory with primary + fragments.",
		CreatedAt: mustParseTime("2026-08-19T12:00:00Z"),
		UpdatedAt: mustParseTime("2026-08-19T12:30:00Z"),
	}
	raw := serializeFragment(original)
	parsed, err := parseFragment(raw)
	if err != nil {
		t.Fatalf("parseFragment: %v", err)
	}
	if parsed.ID != original.ID || parsed.Category != original.Category || parsed.Project != original.Project {
		t.Errorf("meta mismatch: %+v", parsed)
	}
	if parsed.Content != original.Content {
		t.Errorf("content = %q, want %q", parsed.Content, original.Content)
	}
	if len(parsed.Tags) != 2 || parsed.Tags[0] != "go" {
		t.Errorf("tags = %+v", parsed.Tags)
	}
}

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
