package memorystore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	if len(mem.Entries) != 1 || mem.Entries[0].Content != "" {
		t.Errorf("new agent document should be empty, got %+v", mem)
	}
	if !strings.HasPrefix(a.Path(), path) {
		t.Errorf("Path = %q, want under %q", a.Path(), path)
	}
}

func TestAgentUpdateEnforcesCap(t *testing.T) {
	dir := t.TempDir()
	a, _ := NewAgent(dir)
	big := strings.Repeat("x", domain.AgentCharCap+1)
	if err := a.Update([]domain.PrimaryEntry{{Content: big}}); err == nil {
		t.Error("agent Update should reject content over the agent cap")
	}
}

func TestAgentReplaceAndPersist(t *testing.T) {
	dir := t.TempDir()
	a, _ := NewAgent(dir)
	_ = a.Update([]domain.PrimaryEntry{{Content: "convention: gates must be stdlib-only", Source: "agent"}})
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
}

func TestPrimaryAutoCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PrimaryFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("primary.md should not exist yet")
	}
	p, err := NewPrimary(dir)
	if err != nil {
		t.Fatalf("NewPrimary: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("primary.md not auto-created: %v", err)
	}
	mem := p.Load()
	if len(mem.Entries) != 1 || mem.Entries[0].Content != "" {
		t.Errorf("new primary should be empty, got %+v", mem)
	}
}

func TestPrimaryUpdateAndLoad(t *testing.T) {
	dir := t.TempDir()
	p, err := NewPrimary(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := "You are a backend developer living in Jakarta.\n\nYou prefer pragmatic solutions."
	if err := p.Update([]domain.PrimaryEntry{{Content: body, Source: "agent"}}); err != nil {
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

func TestPrimaryUpdateMergesEntries(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewPrimary(dir)
	entries := []domain.PrimaryEntry{
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

func TestPrimaryUpdateEnforcesCap(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewPrimary(dir)
	big := strings.Repeat("x", domain.PrimaryCharCap+1)
	if err := p.Update([]domain.PrimaryEntry{{Content: big}}); err == nil {
		t.Error("Update should reject content over the cap")
	}
}

func TestPrimaryReplace(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewPrimary(dir)
	_ = p.Update([]domain.PrimaryEntry{{Content: "user prefers English", Source: "agent"}})
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
	_ = p.Update([]domain.PrimaryEntry{{Content: "hello"}})
	if err := p.Replace("nonexistent", "new"); err == nil {
		t.Error("Replace should fail when no text matches")
	}
}

func TestPrimaryPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	p1, _ := NewPrimary(dir)
	_ = p1.Update([]domain.PrimaryEntry{{Content: "persisted document body"}})

	p2, err := NewPrimary(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded := p2.Load()
	if len(loaded.Entries) != 1 || loaded.Entries[0].Content != "persisted document body" {
		t.Errorf("reload lost document: %+v", loaded)
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
		t.Fatalf("read primary.md: %v", err)
	}
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatal("primary.md should start with YAML frontmatter delimiter '---'")
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
	mem := p.Load()
	if len(mem.Entries) != 1 || mem.Entries[0].Content != "" {
		t.Errorf("auto-created primary should be empty, got %+v", mem)
	}
}

func TestPrimaryUpdateWritesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewPrimary(dir)
	_ = p.Update([]domain.PrimaryEntry{{Content: "test document body"}})
	raw, _ := os.ReadFile(filepath.Join(dir, PrimaryFile))
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatal("updated primary.md should start with YAML frontmatter")
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

func TestPrimaryMultiParagraphDocument(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewPrimary(dir)
	body := "You are a backend developer.\n\nYou prefer Go and clean architecture.\n\nYou work on NusaShell."
	if err := p.Update([]domain.PrimaryEntry{{Content: body, Source: "agent"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	loaded := p.Load()
	if len(loaded.Entries) != 1 {
		t.Fatalf("loaded %d entries, want 1 (entire body = 1 entry)", len(loaded.Entries))
	}
	if loaded.Entries[0].Content != body {
		t.Errorf("body should be preserved as-is:\nwant: %q\ngot:  %q", body, loaded.Entries[0].Content)
	}
	if !strings.HasPrefix(loaded.Entries[0].ID, "prim_") {
		t.Errorf("ID should be prim_ prefixed, got %q", loaded.Entries[0].ID)
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

func TestFragmentsSaveIfAbsentNormalizesExactDuplicates(t *testing.T) {
	dir := t.TempDir()
	f, err := NewFragments(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := &domain.MemoryFragment{Category: domain.FragmentCategoryProject, Content: "line one\nline two"}
	existing, saved, err := f.SaveIfAbsent(first)
	if err != nil || !saved || existing != nil {
		t.Fatalf("first SaveIfAbsent = existing=%v saved=%v err=%v", existing, saved, err)
	}
	second := &domain.MemoryFragment{Category: domain.FragmentCategoryUser, Content: "  line one  \r\nline two\r\n"}
	existing, saved, err = f.SaveIfAbsent(second)
	if err != nil {
		t.Fatal(err)
	}
	if saved || existing == nil || existing.ID != first.ID {
		t.Fatalf("duplicate SaveIfAbsent = existing=%v saved=%v, want existing first and saved=false", existing, saved)
	}
	if got := len(f.List(domain.FragmentSearchFilter{})); got != 1 {
		t.Fatalf("fragment count = %d, want 1", got)
	}
}

func TestFragmentsSaveIfAbsentConcurrentExactDuplicate(t *testing.T) {
	dir := t.TempDir()
	f, err := NewFragments(dir)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = f.SaveIfAbsent(&domain.MemoryFragment{Content: "same fact\n"})
		}()
	}
	wg.Wait()
	if got := len(f.List(domain.FragmentSearchFilter{})); got != 1 {
		t.Fatalf("concurrent fragment count = %d, want 1", got)
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
