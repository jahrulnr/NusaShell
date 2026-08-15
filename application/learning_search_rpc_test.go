package application

import (
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
	app := &App{
		Skills:   skills,
		Memory:   &fakeMemoryStore{},
		Settings: &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, _ := app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "",
		Kind:  "skills",
	})
	result := resp.(contracts.LearningSearchResult)
	// Empty query → BM25 returns no hits (no terms to match). This is
	// expected; the caller should use skills.list / memory.list for
	// unfiltered listings.
	if len(result.Items) != 0 {
		t.Fatalf("empty query should return 0 results, got %d", len(result.Items))
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
func (f *fakeSkillStore) Get(id string) (*domain.Skill, error) {
	s, ok := f.items[id]
	if !ok {
		return nil, errNotFound
	}
	return s, nil
}
func (f *fakeSkillStore) Save(s *domain.Skill) error {
	if f.items == nil {
		f.items = map[string]*domain.Skill{}
	}
	f.items[s.ID] = s
	return nil
}
func (f *fakeSkillStore) Delete(id string) error {
	delete(f.items, id)
	return nil
}

// fakeMemoryStore is a minimal MemoryStore for tests.
type fakeMemoryStore struct {
	entries []*domain.MemoryEntry
}

func (f *fakeMemoryStore) List() []*domain.MemoryEntry { return f.entries }
func (f *fakeMemoryStore) Save(e *domain.MemoryEntry) error {
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
