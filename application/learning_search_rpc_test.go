package application

import (
	"fmt"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

func TestHandleLearningSearchSkills(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Git rebase", Description: "Rebase workflow", Content: "How to rebase a branch onto main"},
		"skill_2": {ID: "skill_2", Name: "Docker build", Description: "Container builds", Content: "Build multi-stage Docker images"},
	}}
	memory := &fakeMemoryStore{}
	app := &App{
		Skills:   skills,
		Memory:   memory,
		Settings: &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, rpcErr := app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "git rebase",
		Kind:  "skills",
		Limit: 5,
	})
	if rpcErr != nil {
		t.Fatalf("handleLearningSearch: %v", rpcErr)
	}
	result, ok := resp.(contracts.LearningSearchResult)
	if !ok {
		t.Fatalf("unexpected response type: %T", resp)
	}
	if len(result.Items) == 0 {
		t.Fatal("expected at least 1 result for 'git rebase'")
	}
	if result.Items[0].Kind != "skill" {
		t.Fatalf("kind = %q, want skill", result.Items[0].Kind)
	}
	if result.Items[0].ID != "skill_1" {
		t.Fatalf("id = %q, want skill_1", result.Items[0].ID)
	}
}

func TestHandleLearningSearchBoth(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Git rebase", Content: "Rebase workflow"},
	}}
	memory := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "User prefers git rebase over merge"},
	}}
	app := &App{
		Skills:   skills,
		Memory:   memory,
		Settings: &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, _ := app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "rebase",
		Limit: 10,
	})
	result := resp.(contracts.LearningSearchResult)
	kinds := map[string]bool{}
	for _, item := range result.Items {
		kinds[item.Kind] = true
	}
	if !kinds["skill"] {
		t.Error("expected skill results")
	}
	if !kinds["memory"] {
		t.Error("expected memory results")
	}
}

func TestHandleLearningSearchEmptyQuery(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Test", Content: "content"},
	}}
	memory := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "A memory entry"},
	}}
	app := &App{
		Skills:   skills,
		Memory:   memory,
		Settings: &fakeSettingsStore{settings: domain.Settings{}},
	}
	// kind=skills: empty query lists all skills (unfiltered browse).
	resp, _ := app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "",
		Kind:  "skills",
	})
	result := resp.(contracts.LearningSearchResult)
	if len(result.Items) != 1 || result.Items[0].ID != "skill_1" {
		t.Fatalf("empty query + kind=skills should list all skills, got %+v", result.Items)
	}
	// kind=memory: empty query lists all memory entries.
	resp, _ = app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "",
		Kind:  "memory",
	})
	result = resp.(contracts.LearningSearchResult)
	if len(result.Items) != 1 || result.Items[0].ID != "mem_1" {
		t.Fatalf("empty query + kind=memory should list all memory, got %+v", result.Items)
	}
	// no kind: empty query lists both, capped by limit.
	resp, _ = app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "",
		Limit: 10,
	})
	result = resp.(contracts.LearningSearchResult)
	if len(result.Items) != 2 {
		t.Fatalf("empty query should list both kinds, got %d items", len(result.Items))
	}
}

// fakeSkillStore is a minimal SkillStore for tests.
type fakeSkillStore struct {
	items map[string]*domain.Skill
}

func (f *fakeSkillStore) List() []*domain.Skill {
	out := make([]*domain.Skill, 0, len(f.items))
	for _, s := range f.items {
		out = append(out, s)
	}
	return out
}
func (f *fakeSkillStore) Get(id, ownedBy string) (*domain.Skill, error) {
	s, ok := f.items[id]
	if !ok {
		return nil, errNotFound
	}
	return s, nil
}
func (f *fakeSkillStore) ReadFile(id, ownedBy, path string, offset, maxChars int) (*domain.SkillFile, error) {
	return nil, errNotFound
}
func (f *fakeSkillStore) Files(id, ownedBy string) ([]domain.SkillFileEntry, error) {
	return nil, errNotFound
}
func (f *fakeSkillStore) Save(s *domain.Skill) error {
	if f.items == nil {
		f.items = map[string]*domain.Skill{}
	}
	f.items[s.ID] = s
	return nil
}
func (f *fakeSkillStore) Delete(id, ownedBy string) error {
	delete(f.items, id)
	return nil
}
func (f *fakeSkillStore) Install(zipData []byte) (string, error) {
	return "", fmt.Errorf("not supported")
}
func (f *fakeSkillStore) MountPluginSkills(pluginID, dir string) error { return nil }
func (f *fakeSkillStore) UnmountPluginSkills(pluginID string) error    { return nil }

// fakeMemoryStore is a minimal MemoryStore for tests.
type fakeMemoryStore struct {
	entries []*domain.MemoryEntry
}

func (f *fakeMemoryStore) List() []*domain.MemoryEntry { return f.entries }
func (f *fakeMemoryStore) Save(e *domain.MemoryEntry) error {
	for i, existing := range f.entries {
		if existing.ID == e.ID {
			f.entries[i] = e
			return nil
		}
	}
	f.entries = append(f.entries, e)
	return nil
}
func (f *fakeMemoryStore) Delete(id string) error {
	for i, e := range f.entries {
		if e.ID == id {
			f.entries = append(f.entries[:i], f.entries[i+1:]...)
			return nil
		}
	}
	return errNotFound
}

// fakeSettingsStore is a minimal SettingsStore for tests.
type fakeSettingsStore struct {
	settings domain.Settings
}

func (f *fakeSettingsStore) Get() domain.Settings { return f.settings }
func (f *fakeSettingsStore) Set(s domain.Settings) error {
	f.settings = s
	return nil
}

func TestHandleLearningGraphFiltersDanglingEdges(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Git", Content: "x"},
	}}
	memory := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "memory one"},
	}}
	app := &App{
		Skills:   skills,
		Memory:   memory,
		Settings: &fakeSettingsStore{settings: domain.Settings{}},
		LearningEdges: &fakeEdgeStore{edges: []*domain.LearningEdge{
			{SourceID: "skill_1", TargetID: "mem_1", Type: domain.EdgeUsedWith, Weight: 0.8},      // valid
			{SourceID: "skill_1", TargetID: "mem_deleted", Type: domain.EdgeRelated, Weight: 0.9}, // dangling
			{SourceID: "mem_gone", TargetID: "mem_1", Type: domain.EdgeRelated, Weight: 0.7},      // dangling
		}},
	}
	resp, rpcErr := app.handleLearningGraph()
	if rpcErr != nil {
		t.Fatalf("handleLearningGraph: %v", rpcErr)
	}
	result := resp.(contracts.LearningGraphResult)
	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(result.Nodes))
	}
	if len(result.Edges) != 1 {
		t.Fatalf("edges = %d, want 1 (dangling edges filtered), got %+v", len(result.Edges), result.Edges)
	}
	if result.Edges[0].From != "skill_1" || result.Edges[0].To != "mem_1" {
		t.Fatalf("unexpected edge: %+v", result.Edges[0])
	}
}
