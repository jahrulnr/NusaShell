package application

import (
	"context"
	"testing"
	"time"

	"nusashell/domain"
)

func TestBFSFindsRelatedNodes(t *testing.T) {
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	// Build: A -- B -- C -- D
	g.AddEdge("A", "B", domain.EdgeRelated, 0.5)
	g.AddEdge("B", "C", domain.EdgeRelated, 0.5)
	g.AddEdge("C", "D", domain.EdgeRelated, 0.5)

	// BFS from A, 2 hops → should find B (hop 1) and C (hop 2), not D
	expanded := g.BFS([]string{"A"}, 2)
	if !contains(expanded, "B") || !contains(expanded, "C") {
		t.Errorf("BFS(A, 2) = %v, want [B C]", expanded)
	}
	if contains(expanded, "D") {
		t.Errorf("BFS(A, 2) should not reach D (3 hops away)")
	}
}

func TestBFSZeroHopsReturnsEmpty(t *testing.T) {
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	g.AddEdge("A", "B", domain.EdgeRelated, 0.5)
	if expanded := g.BFS([]string{"A"}, 0); len(expanded) != 0 {
		t.Errorf("BFS with 0 hops should return empty, got %v", expanded)
	}
}

func TestBFSNoSeedsReturnsEmpty(t *testing.T) {
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	g.AddEdge("A", "B", domain.EdgeRelated, 0.5)
	if expanded := g.BFS(nil, 2); len(expanded) != 0 {
		t.Errorf("BFS with no seeds should return empty, got %v", expanded)
	}
}

func TestNeighborIDsUndirected(t *testing.T) {
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	g.AddEdge("A", "B", domain.EdgeRelated, 0.5)
	// Should find A as neighbor of B (undirected)
	neighbors := g.NeighborIDs("B", "")
	if !contains(neighbors, "A") {
		t.Errorf("NeighborIDs(B) = %v, want [A]", neighbors)
	}
}

func TestSearchWithGraphBFSChannel(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_docker": {ID: "skill_docker", Name: "docker", Description: "container builds", Content: "how to build docker images"},
		"skill_k8s":    {ID: "skill_k8s", Name: "kubernetes", Description: "orchestration", Content: "deploy pods k8s"},
	}}
	mem := &fakeMemoryStore{}
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	// Connect docker → k8s (related)
	g.AddEdge("skill_docker", "skill_k8s", domain.EdgeRelated, 0.8)

	s := NewLearningSearcher(skills, mem, nil, g)
	// Search "docker" — should find docker via BM25, k8s via BFS
	results, err := s.SearchSkills(context.Background(), "docker", 10)
	if err != nil {
		t.Fatalf("SearchSkills: %v", err)
	}
	foundDocker := false
	foundK8s := false
	for _, r := range results {
		if r.ID == "skill_docker" {
			foundDocker = true
		}
		if r.ID == "skill_k8s" {
			foundK8s = true
		}
	}
	if !foundDocker {
		t.Error("expected to find skill_docker via BM25")
	}
	if !foundK8s {
		t.Error("expected to find skill_k8s via graph BFS expansion")
	}
}

func TestTemporalDecayBoostsRecentEntries(t *testing.T) {
	now := time.Now()
	skills := []*domain.Skill{
		{ID: "old", Name: "old skill", Description: "test", Content: "test", LastUsedAt: now.AddDate(-1, 0, 0)},
		{ID: "new", Name: "new skill", Description: "test", Content: "test", LastUsedAt: now.Add(-1 * time.Hour)},
	}
	// Both have same RRF score
	fused := []rrfResult{
		{ID: "old", Score: 1.0},
		{ID: "new", Score: 1.0},
	}
	s := &LearningSearcher{}
	decayed := s.applyTemporalDecay(fused, skills, nil)
	// "new" should have higher score after decay
	if decayed[0].ID != "new" {
		t.Errorf("expected 'new' to rank first after decay, got %s (score=%.4f) vs %s (score=%.4f)",
			decayed[0].ID, decayed[0].Score, decayed[1].ID, decayed[1].Score)
	}
}

func TestCollectSeedsDeduplicates(t *testing.T) {
	lists := [][]string{
		{"A", "B", "C"},
		{"B", "C", "D"},
	}
	seeds := collectSeeds(lists)
	if len(seeds) != 4 {
		t.Errorf("collectSeeds = %v (len %d), want 4 unique", seeds, len(seeds))
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// Compile-time check that fakeEdgeStore implements LearningEdgeStore.
var _ LearningEdgeStore = (*fakeEdgeStore)(nil)
