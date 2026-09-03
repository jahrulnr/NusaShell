package application

import (
	"fmt"
	"strings"
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
	fragments := &fakeFragmentStore{frags: []*domain.MemoryFragment{
		{ID: "frag_1", Content: "User prefers git rebase over merge", Category: domain.FragmentCategoryGeneral},
	}}
	app := &App{
		Skills:    skills,
		Fragments: fragments,
		Settings:  &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, _ := app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "rebase",
		Limit: 10,
	})
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
	if !tiers["fragment"] {
		t.Error("expected memory result with tier=fragment")
	}
}

func TestHandleLearningSearchEmptyQuery(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Test", Content: "content"},
	}}
	fragments := &fakeFragmentStore{frags: []*domain.MemoryFragment{
		{ID: "frag_1", Content: "A memory entry", Category: domain.FragmentCategoryGeneral},
	}}
	app := &App{
		Skills:    skills,
		Fragments: fragments,
		Settings:  &fakeSettingsStore{settings: domain.Settings{}},
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
	// kind=memory: empty query lists all memory entries (fragments + user document).
	resp, _ = app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "",
		Kind:  "memory",
	})
	result = resp.(contracts.LearningSearchResult)
	if len(result.Items) != 1 || result.Items[0].ID != "frag_1" {
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

// TestHandleLearningSearchTierBadge verifies that user and fragment
// memory entries are tagged with the correct tier so the UI can show
// a distinguishing badge.
func TestHandleLearningSearchTierBadge(t *testing.T) {
	fragments := &fakeFragmentStore{frags: []*domain.MemoryFragment{
		{ID: "frag_1", Content: "A fragment entry", Category: domain.FragmentCategoryGeneral},
	}}
	user := &stubUserStoreReview{mem: &domain.MemoryDocument{
		Entries: []domain.DocumentEntry{
			{ID: "user_1", Content: "A user entry"},
		},
	}}
	app := &App{
		Fragments: fragments,
		User:      user,
		Settings:  &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, _ := app.handleLearningSearch(contracts.LearningSearchRequest{
		Query: "",
		Kind:  "memory",
	})
	result := resp.(contracts.LearningSearchResult)
	tiers := map[string]string{}
	for _, item := range result.Items {
		tiers[item.ID] = item.Tier
	}
	if tiers["frag_1"] != "fragment" {
		t.Errorf("frag_1 tier = %q, want \"fragment\"", tiers["frag_1"])
	}
	if tiers["user_1"] != domain.MemoryTierUser {
		t.Errorf("user_1 tier = %q, want %q", tiers["user_1"], domain.MemoryTierUser)
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
func (f *fakeSkillStore) WriteFile(id, ownedBy, path, content string) error {
	return errNotFound
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
func (f *fakeMemoryStore) Replace(target, oldText, content string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		target = domain.MemoryTargetMemory
	}
	idx := -1
	for i, e := range f.entries {
		if e.Target != target {
			continue
		}
		if strings.Contains(e.Content, oldText) {
			if idx >= 0 {
				return fmt.Errorf("multiple matches")
			}
			idx = i
		}
	}
	if idx < 0 {
		return errNotFound
	}
	f.entries[idx].Content = content
	return nil
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

// fakeUserStore is a minimal user-tier store for tests.
type fakeUserStore struct {
	entries []domain.DocumentEntry
}

func (f *fakeUserStore) Load() *domain.MemoryDocument {
	return &domain.MemoryDocument{Entries: f.entries}
}
func (f *fakeUserStore) Update(entries []domain.DocumentEntry) error {
	f.entries = entries
	return nil
}
func (f *fakeUserStore) Replace(oldText, content string) error { return nil }
func (f *fakeUserStore) Path() string                          { return "" }

func TestHandleLearningGraphUserNodeTierAndLabel(t *testing.T) {
	user := &fakeUserStore{entries: []domain.DocumentEntry{
		{ID: "user_abc", Content: "You are a backend developer living in Jakarta.\nYou prefer Go and pragmatic solutions."},
	}}
	fragments := &fakeFragmentStore{frags: []*domain.MemoryFragment{
		{ID: "frag_1", Content: "multi\nline fact", Category: domain.FragmentCategoryGeneral},
	}}
	app := &App{
		Skills:    &fakeSkillStore{items: map[string]*domain.Skill{}},
		User:      user,
		Fragments: fragments,
		Settings:  &fakeSettingsStore{settings: domain.Settings{}},
	}
	resp, rpcErr := app.handleLearningGraph()
	if rpcErr != nil {
		t.Fatalf("handleLearningGraph: %v", rpcErr)
	}
	result := resp.(contracts.LearningGraphResult)
	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 (user + fragment)", len(result.Nodes))
	}
	byID := map[string]contracts.LearningGraphNode{}
	for _, n := range result.Nodes {
		byID[n.ID] = n
	}
	pn, ok := byID["user_abc"]
	if !ok || pn.Tier != domain.MemoryTierUser {
		t.Fatalf("user node missing or wrong tier: %+v", result.Nodes)
	}
	if pn.Name != "You are a backend developer living in Jakarta." {
		t.Fatalf("user label should be the first line, got %q", pn.Name)
	}
	fn, ok := byID["frag_1"]
	if !ok || fn.Tier != "fragment" {
		t.Fatalf("fragment node missing or wrong tier: %+v", result.Nodes)
	}
	if fn.Name != "multi line fact" {
		t.Fatalf("fragment label should collapse newlines, got %q", fn.Name)
	}
}

func TestHandleLearningGraphFiltersDanglingEdges(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "Git", Content: "x"},
	}}
	fragments := &fakeFragmentStore{frags: []*domain.MemoryFragment{
		{ID: "frag_1", Content: "memory one", Category: domain.FragmentCategoryGeneral},
	}}
	app := &App{
		Skills:    skills,
		Fragments: fragments,
		Settings:  &fakeSettingsStore{settings: domain.Settings{}},
		LearningEdges: &fakeEdgeStore{edges: []*domain.LearningEdge{
			{SourceID: "skill_1", TargetID: "frag_1", Type: domain.EdgeUsedWith, Weight: 0.8},     // valid
			{SourceID: "skill_1", TargetID: "mem_deleted", Type: domain.EdgeRelated, Weight: 0.9}, // dangling
			{SourceID: "mem_gone", TargetID: "frag_1", Type: domain.EdgeRelated, Weight: 0.7},     // dangling
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
	if result.Edges[0].From != "skill_1" || result.Edges[0].To != "frag_1" {
		t.Fatalf("unexpected edge: %+v", result.Edges[0])
	}
}

// fakeFragmentStore is a minimal FragmentStore for tests.
type fakeFragmentStore struct {
	frags []*domain.MemoryFragment
}

func (f *fakeFragmentStore) List(filter domain.FragmentSearchFilter) []*domain.MemoryFragment {
	limit := filter.Limit
	if limit <= 0 {
		limit = len(f.frags)
	}
	out := make([]*domain.MemoryFragment, 0, limit)
	for _, fr := range f.frags {
		if filter.Category != "" && fr.Category != filter.Category {
			continue
		}
		if filter.Project != "" && fr.Project != filter.Project {
			continue
		}
		if filter.Task != "" && fr.Task != filter.Task {
			continue
		}
		out = append(out, fr)
		if len(out) >= limit {
			break
		}
	}
	return out
}
func (f *fakeFragmentStore) Get(id string) *domain.MemoryFragment {
	for _, fr := range f.frags {
		if fr.ID == id {
			return fr
		}
	}
	return nil
}
func (f *fakeFragmentStore) Save(fr *domain.MemoryFragment) error {
	for i, existing := range f.frags {
		if existing.ID == fr.ID {
			f.frags[i] = fr
			return nil
		}
	}
	f.frags = append(f.frags, fr)
	return nil
}
func (f *fakeFragmentStore) Delete(id string) error {
	for i, fr := range f.frags {
		if fr.ID == id {
			f.frags = append(f.frags[:i], f.frags[i+1:]...)
			return nil
		}
	}
	return errNotFound
}
func (f *fakeFragmentStore) Search(filter domain.FragmentSearchFilter) []domain.FragmentSearchHit {
	q := strings.ToLower(strings.TrimSpace(filter.Query))
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	hits := make([]domain.FragmentSearchHit, 0, limit)
	for _, fr := range f.frags {
		if filter.Category != "" && fr.Category != filter.Category {
			continue
		}
		if filter.Project != "" && fr.Project != filter.Project {
			continue
		}
		if filter.Task != "" && fr.Task != filter.Task {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(fr.Content), q) {
			continue
		}
		score := 1.0
		if q == "" {
			score = 0
		}
		hits = append(hits, domain.FragmentSearchHit{Fragment: fr, Score: score})
		if len(hits) >= limit {
			break
		}
	}
	return hits
}
