package application

import (
	"fmt"
	"time"

	"nusashell/domain"
)

// LearningGraphService manages the bitemporal edge graph between learning
// nodes (skills and memory entries). It builds edges, strengthens them on
// repeat observation via probability-union (CombineWeights), and answers
// neighborhood queries for graph-augmented retrieval.
//
// The service is stateless beyond the LearningEdgeStore — it does not cache
// the graph in memory. The store keeps an in-memory index backed by JSONL,
// so List() is fast and suitable for neighborhood queries.
type LearningGraphService struct {
	edges LearningEdgeStore
}

// NewLearningGraphService creates a graph service backed by the given store.
func NewLearningGraphService(edges LearningEdgeStore) *LearningGraphService {
	return &LearningGraphService{edges: edges}
}

// AddEdge creates a new edge or strengthens an existing one. If an edge with
// the same (source, target, type) already exists and is still valid, its
// weight is combined with the new weight via probability-union. The edge's
// ValidAt is set to now if creating a new edge; existing edges keep their
// original ValidAt.
func (g *LearningGraphService) AddEdge(sourceID, targetID string, edgeType domain.LearningEdgeType, weight float64) (*domain.LearningEdge, error) {
	if sourceID == "" || targetID == "" {
		return nil, fmt.Errorf("learning graph: source and target IDs are required")
	}
	if sourceID == targetID {
		return nil, fmt.Errorf("learning graph: self-loops are not allowed")
	}
	if weight < 0 {
		weight = 0
	} else if weight > 1 {
		weight = 1
	}
	now := time.Now().UTC()
	// Find existing valid edge with same (source, target, type).
	for _, e := range g.edges.List() {
		if e.SourceID == sourceID && e.TargetID == targetID && e.Type == edgeType && e.InvalidAt == nil {
			// Strengthen: combine weights, keep original ValidAt.
			e.Weight = domain.CombineWeights(e.Weight, weight)
			if err := g.replaceEdge(e); err != nil {
				return nil, err
			}
			return e, nil
		}
	}
	// Create new edge.
	edge := &domain.LearningEdge{
		ID:        domain.NewULID("edge"),
		SourceID:  sourceID,
		TargetID:  targetID,
		Type:      edgeType,
		Weight:    weight,
		ValidAt:   now,
		CreatedAt: now,
	}
	if err := g.edges.Save(edge); err != nil {
		return nil, fmt.Errorf("learning graph: save edge: %w", err)
	}
	return edge, nil
}

// InvalidateEdge marks an edge as no longer current by setting InvalidAt.
// Returns ErrNotFound if the edge does not exist or is already invalidated.
func (g *LearningGraphService) InvalidateEdge(id string) error {
	for _, e := range g.edges.List() {
		if e.ID == id {
			if e.InvalidAt != nil {
				return fmt.Errorf("learning graph: edge %s already invalidated", id)
			}
			now := time.Now().UTC()
			e.InvalidAt = &now
			return g.replaceEdge(e)
		}
	}
	return fmt.Errorf("learning graph: edge %s not found", id)
}

// Neighbors returns valid edges from the given node, optionally filtered by
// edge type. Only edges with InvalidAt == nil are returned.
func (g *LearningGraphService) Neighbors(nodeID string, edgeType domain.LearningEdgeType) []*domain.LearningEdge {
	var out []*domain.LearningEdge
	for _, e := range g.edges.List() {
		if e.InvalidAt != nil {
			continue
		}
		if e.SourceID != nodeID {
			continue
		}
		if edgeType != "" && e.Type != edgeType {
			continue
		}
		out = append(out, e)
	}
	return out
}

// AllEdges returns all edges (including invalidated ones) for inspection.
func (g *LearningGraphService) AllEdges() []*domain.LearningEdge {
	return g.edges.List()
}

// DeleteEdge permanently removes an edge from the store.
func (g *LearningGraphService) DeleteEdge(id string) error {
	return g.edges.Delete(id)
}

// replaceEdge rewrites an edge by deleting and re-saving it. The JSONL store
// appends, so this produces a new line with the updated fields. The in-memory
// index is updated atomically by the store.
func (g *LearningGraphService) replaceEdge(updated *domain.LearningEdge) error {
	if err := g.edges.Delete(updated.ID); err != nil {
		return fmt.Errorf("learning graph: replace edge (delete): %w", err)
	}
	return g.edges.Save(updated)
}
