package application

import (
	"fmt"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

func TestHandleLearningSearchSkills(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Git rebase", Description: "Rebase workflow", Content: "How to rebase a branch onto main", Status: domain.SkillStatusTrusted},
		"skill_2": {ID: "skill_2", Name: "Docker build", Description: "Container builds", Content: "Build multi-stage Docker images", Status: domain.SkillStatusTrusted},
	}}
	app := &App{
		Skills:        skills,
		MemoryRecords: &fakeMemoryRecordStore{},
		Settings:      &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, rpcErr := app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "git rebase",
		Kind:  "skills",
		Limit: 5,
	})
	if rpcErr != nil {
		t.Fatalf("handleLearningSearch: %v", rpcErr)
	}
	result := resp.(contracts.LearningSearchResult)
	if len(result.Items) == 0 {
		t.Fatal("expected at least 1 result for 'git rebase'")
	}
	if result.Items[0].Kind != "skill" || result.Items[0].ID != "skill_1" {
		t.Fatalf("got %+v", result.Items[0])
	}
}

func TestHandleLearningSearchBoth(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Git rebase", Content: "Rebase workflow", Status: domain.SkillStatusTrusted},
	}}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "mem_1", Body: "User prefers git rebase over merge", Status: domain.MemoryStatusLearned, Type: domain.MemoryTypePreference},
	}}
	app := &App{
		Skills:        skills,
		MemoryRecords: records,
		Settings:      &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, _ := app.handleLearningSearch(contracts.LearningSearchRequest{Query: "rebase", Limit: 10})
	result := resp.(contracts.LearningSearchResult)
	kinds := map[string]bool{}
	tiers := map[string]bool{}
	for _, item := range result.Items {
		kinds[item.Kind] = true
		if item.Kind == "memory" {
			tiers[item.Tier] = true
		}
	}
	if !kinds["skill"] {
		t.Error("expected skill results")
	}
	if !kinds["memory"] {
		t.Error("expected memory results")
	}
	if !tiers[contracts.MemoryTierRecord] {
		t.Error("expected memory result with tier=record")
	}
}

func TestHandleLearningSearchEmptyQuery(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Test", Content: "content", Status: domain.SkillStatusTrusted},
	}}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "mem_1", Body: "A memory entry", Status: domain.MemoryStatusLearned},
	}}
	app := &App{
		Skills:        skills,
		MemoryRecords: records,
		Settings:      &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, _ := app.handleLearningSearch(contracts.LearningSearchRequest{Query: "", Kind: "skills"})
	result := resp.(contracts.LearningSearchResult)
	if len(result.Items) != 1 || result.Items[0].ID != "skill_1" {
		t.Fatalf("empty query + kind=skills: %+v", result.Items)
	}
	resp, _ = app.handleLearningSearch(contracts.LearningSearchRequest{Query: "", Kind: "memory"})
	result = resp.(contracts.LearningSearchResult)
	if len(result.Items) != 1 || result.Items[0].ID != "mem_1" {
		t.Fatalf("empty query + kind=memory: %+v", result.Items)
	}
}

func TestHandleLearningSearchTierBadge(t *testing.T) {
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "mem_1", Body: "A record", Status: domain.MemoryStatusLearned},
	}}
	user := &fakeUserStore{entries: []domain.DocumentEntry{
		{ID: "user_1", Content: "A user entry"},
	}}
	app := &App{
		MemoryRecords: records,
		User:          user,
		Settings:      &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, _ := app.handleLearningSearch(contracts.LearningSearchRequest{Query: "", Kind: "memory"})
	result := resp.(contracts.LearningSearchResult)
	tiers := map[string]string{}
	for _, item := range result.Items {
		tiers[item.ID] = item.Tier
	}
	if tiers["mem_1"] != contracts.MemoryTierRecord {
		t.Errorf("mem_1 tier = %q", tiers["mem_1"])
	}
	if tiers["user_1"] != domain.MemoryTierUser {
		t.Errorf("user_1 tier = %q", tiers["user_1"])
	}
}

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
func (f *fakeSkillStore) WriteFile(id, ownedBy, path, content string) error { return errNotFound }
func (f *fakeSkillStore) Save(s *domain.Skill) error {
	if f.items == nil {
		f.items = map[string]*domain.Skill{}
	}
	if existing, ok := f.items[s.ID]; ok && existing != nil {
		next := existing.Version
		if next < 1 {
			next = 1
		}
		s.Version = next + 1
		s.ActiveVersion = s.Version
	} else if s.Version < 1 {
		s.Version = 1
		s.ActiveVersion = 1
	}
	f.items[s.ID] = s
	return nil
}
func (f *fakeSkillStore) Delete(id, ownedBy string) error { delete(f.items, id); return nil }
func (f *fakeSkillStore) Install(zipData []byte) (string, error) {
	return "", fmt.Errorf("not supported")
}
func (f *fakeSkillStore) MountPluginSkills(pluginID, dir string) error { return nil }
func (f *fakeSkillStore) UnmountPluginSkills(pluginID string) error    { return nil }
func (f *fakeSkillStore) Promote(id, ownedBy string) (*domain.Skill, error) {
	s, err := f.Get(id, ownedBy)
	if err != nil {
		return nil, err
	}
	s.Status = domain.SkillStatusTrusted
	return s, nil
}
func (f *fakeSkillStore) Rollback(id, ownedBy string, version int) (*domain.Skill, error) {
	s, err := f.Get(id, ownedBy)
	if err != nil {
		return nil, err
	}
	s.ActiveVersion = version
	return s, nil
}

type fakeSettingsStore struct {
	settings domain.Settings
}

func (f *fakeSettingsStore) Get() domain.Settings        { return f.settings }
func (f *fakeSettingsStore) Set(s domain.Settings) error { f.settings = s; return nil }

type fakeUserStore struct {
	entries []domain.DocumentEntry
}

func (f *fakeUserStore) Load() *domain.MemoryDocument {
	return &domain.MemoryDocument{Entries: f.entries}
}
func (f *fakeUserStore) Update(entries []domain.DocumentEntry) error { f.entries = entries; return nil }
func (f *fakeUserStore) Replace(oldText, content string) error       { return nil }
func (f *fakeUserStore) Path() string                                { return "" }

func TestHandleLearningGraphUserNodeTierAndLabel(t *testing.T) {
	user := &fakeUserStore{entries: []domain.DocumentEntry{
		{ID: "user_abc", Content: "You are a backend developer living in Jakarta.\nYou prefer Go and pragmatic solutions."},
	}}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "mem_1", Body: "multi\nline fact", Status: domain.MemoryStatusLearned},
	}}
	app := &App{
		Skills:        &fakeSkillStore{items: map[string]*domain.Skill{}},
		User:          user,
		MemoryRecords: records,
		Settings:      &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, rpcErr := app.handleLearningGraph()
	if rpcErr != nil {
		t.Fatalf("handleLearningGraph: %v", rpcErr)
	}
	result := resp.(contracts.LearningGraphResult)
	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(result.Nodes))
	}
	byID := map[string]contracts.LearningGraphNode{}
	for _, n := range result.Nodes {
		byID[n.ID] = n
	}
	pn := byID["user_abc"]
	if pn.Tier != domain.MemoryTierUser {
		t.Fatalf("user node: %+v", result.Nodes)
	}
	if pn.Name != "You are a backend developer living in Jakarta." {
		t.Fatalf("user label = %q", pn.Name)
	}
	fn := byID["mem_1"]
	if fn.Tier != contracts.MemoryTierRecord {
		t.Fatalf("record node: %+v", result.Nodes)
	}
	if fn.Name != "multi line fact" {
		t.Fatalf("record label = %q", fn.Name)
	}
}

func TestHandleLearningGraphFiltersDanglingEdges(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Git", Content: "x", Status: domain.SkillStatusTrusted},
	}}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "mem_1", Body: "memory one", Status: domain.MemoryStatusLearned},
	}}
	app := &App{
		Skills:        skills,
		MemoryRecords: records,
		Settings:      &fakeSettingsStore{settings: domain.Settings{}},
		LearningEdges: &fakeEdgeStore{edges: []*domain.LearningEdge{
			{SourceID: "skill_1", TargetID: "mem_1", Type: domain.EdgeUsedWith, Weight: 0.8},
			{SourceID: "skill_1", TargetID: "mem_deleted", Type: domain.EdgeRelated, Weight: 0.9},
			{SourceID: "mem_gone", TargetID: "mem_1", Type: domain.EdgeRelated, Weight: 0.7},
		}},
	}
	resp, rpcErr := app.handleLearningGraph()
	if rpcErr != nil {
		t.Fatalf("handleLearningGraph: %v", rpcErr)
	}
	result := resp.(contracts.LearningGraphResult)
	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %d", len(result.Nodes))
	}
	if len(result.Edges) != 1 {
		t.Fatalf("edges = %d, want 1, got %+v", len(result.Edges), result.Edges)
	}
	if result.Edges[0].From != "skill_1" || result.Edges[0].To != "mem_1" {
		t.Fatalf("unexpected edge: %+v", result.Edges[0])
	}
}
