package application

import (
	"testing"
	"time"

	"nusashell/domain"
)

// fakeEdgeStore is an in-memory LearningEdgeStore for tests.
type fakeEdgeStore struct {
	edges []*domain.LearningEdge
}

func (f *fakeEdgeStore) List() []*domain.LearningEdge {
	out := make([]*domain.LearningEdge, len(f.edges))
	copy(out, f.edges)
	return out
}

func (f *fakeEdgeStore) Save(e *domain.LearningEdge) error {
	f.edges = append(f.edges, e)
	return nil
}

func (f *fakeEdgeStore) Delete(id string) error {
	for i, e := range f.edges {
		if e.ID == id {
			f.edges = append(f.edges[:i], f.edges[i+1:]...)
			return nil
		}
	}
	return errNotFound
}

func TestAddEdgeCreatesNewEdge(t *testing.T) {
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	edge, err := g.AddEdge("skill_1", "skill_2", domain.EdgeRelated, 0.5)
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if edge.SourceID != "skill_1" || edge.TargetID != "skill_2" {
		t.Fatalf("wrong endpoints: %+v", edge)
	}
	if edge.Weight != 0.5 {
		t.Fatalf("weight = %v, want 0.5", edge.Weight)
	}
	if edge.InvalidAt != nil {
		t.Fatal("new edge should be valid")
	}
}

func TestAddEdgeStrengthensExisting(t *testing.T) {
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	_, _ = g.AddEdge("a", "b", domain.EdgeRelated, 0.5)
	edge, err := g.AddEdge("a", "b", domain.EdgeRelated, 0.5)
	if err != nil {
		t.Fatalf("AddEdge (strengthen): %v", err)
	}
	if edge.Weight <= 0.5 || edge.Weight >= 1.0 {
		t.Fatalf("strengthened weight = %v, want (0.5, 1.0)", edge.Weight)
	}
	if len(store.edges) != 1 {
		t.Fatalf("expected 1 edge after strengthen, got %d", len(store.edges))
	}
}

func TestAddEdgeRejectsSelfLoop(t *testing.T) {
	g := NewLearningGraphService(&fakeEdgeStore{})
	_, err := g.AddEdge("a", "a", domain.EdgeRelated, 0.5)
	if err == nil {
		t.Fatal("expected error for self-loop")
	}
}

func TestInvalidateEdge(t *testing.T) {
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	edge, _ := g.AddEdge("a", "b", domain.EdgeRelated, 0.5)
	if err := g.InvalidateEdge(edge.ID); err != nil {
		t.Fatalf("InvalidateEdge: %v", err)
	}
	neighbors := g.Neighbors("a", "")
	if len(neighbors) != 0 {
		t.Fatalf("expected 0 valid neighbors after invalidation, got %d", len(neighbors))
	}
}

func TestNeighborsFiltersByType(t *testing.T) {
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	_, _ = g.AddEdge("a", "b", domain.EdgeRelated, 0.5)
	_, _ = g.AddEdge("a", "c", domain.EdgeUsedWith, 0.3)
	related := g.Neighbors("a", domain.EdgeRelated)
	if len(related) != 1 || related[0].TargetID != "b" {
		t.Fatalf("related neighbors = %+v", related)
	}
	usedWith := g.Neighbors("a", domain.EdgeUsedWith)
	if len(usedWith) != 1 || usedWith[0].TargetID != "c" {
		t.Fatalf("used_with neighbors = %+v", usedWith)
	}
	all := g.Neighbors("a", "")
	if len(all) != 2 {
		t.Fatalf("all neighbors = %d, want 2", len(all))
	}
}

func TestAddEdgeClampsWeight(t *testing.T) {
	g := NewLearningGraphService(&fakeEdgeStore{})
	edge, _ := g.AddEdge("a", "b", domain.EdgeRelated, 1.5)
	if edge.Weight != 1.0 {
		t.Fatalf("weight = %v, want 1.0 (clamped)", edge.Weight)
	}
	edge2, _ := g.AddEdge("c", "d", domain.EdgeRelated, -0.5)
	if edge2.Weight != 0 {
		t.Fatalf("weight = %v, want 0 (clamped)", edge2.Weight)
	}
}

func TestInvalidateEdgeAlreadyInvalidated(t *testing.T) {
	store := &fakeEdgeStore{}
	g := NewLearningGraphService(store)
	edge, _ := g.AddEdge("a", "b", domain.EdgeRelated, 0.5)
	_ = g.InvalidateEdge(edge.ID)
	err := g.InvalidateEdge(edge.ID)
	if err == nil {
		t.Fatal("expected error for double invalidation")
	}
}

// Ensure time is used to avoid unused import in case of future edits.
var _ = time.Now
