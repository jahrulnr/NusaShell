package application

import (
	"fmt"
	"sync"

	"nusashell/domain"
	clock "nusashell/pkg/time"
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
	mu    sync.RWMutex
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
	g.mu.Lock()
	defer g.mu.Unlock()
	if sourceID == "" || targetID == "" {
		return nil, fmt.Errorf("learning graph: source and target IDs are required")
	}
	// Related and used_with are undirected relationships. Canonicalizing
	// their endpoints prevents map iteration order from persisting both
	// A→B and B→A across repeated graph rebuilds.
	if (edgeType == domain.EdgeRelated || edgeType == domain.EdgeUsedWith) && sourceID > targetID {
		sourceID, targetID = targetID, sourceID
	}
	if sourceID == targetID {
		return nil, fmt.Errorf("learning graph: self-loops are not allowed")
	}
	if weight < 0 {
		weight = 0
	} else if weight > 1 {
		weight = 1
	}
	now := clock.NewTime().Time()
	// Find existing valid edge with same (source, target, type).
	for _, e := range g.edges.List() {
		if e.Type != edgeType || e.InvalidAt != nil {
			continue
		}
		sameEndpoints := e.SourceID == sourceID && e.TargetID == targetID
		if edgeType == domain.EdgeRelated || edgeType == domain.EdgeUsedWith {
			sameEndpoints = sameEndpoints || (e.SourceID == targetID && e.TargetID == sourceID)
		}
		if sameEndpoints {
			// Strengthen: combine weights, keep original ValidAt.
			e.Weight = domain.CombineWeights(e.Weight, weight)
			e.SourceID = sourceID
			e.TargetID = targetID
			if err := g.replaceEdge(e); err != nil {
				return nil, err
			}
			return e, nil
		}
	}
	// Create new edge.
	edge := &domain.LearningEdge{
		ID:        domain.NewULID(domain.IDPrefixEdge),
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
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.edges.List() {
		if e.ID == id {
			if e.InvalidAt != nil {
				return fmt.Errorf("learning graph: edge %s already invalidated", id)
			}
			now := clock.NewTime().Time()
			e.InvalidAt = &now
			return g.replaceEdge(e)
		}
	}
	return fmt.Errorf("learning graph: edge %s not found", id)
}

// Neighbors returns valid edges from the given node, optionally filtered by
// edge type. Only edges with InvalidAt == nil are returned. Checks both
// SourceID and TargetID so the graph is treated as undirected for traversal.
func (g *LearningGraphService) Neighbors(nodeID string, edgeType domain.LearningEdgeType) []*domain.LearningEdge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []*domain.LearningEdge
	for _, e := range g.edges.List() {
		if e.InvalidAt != nil {
			continue
		}
		if e.SourceID != nodeID && e.TargetID != nodeID {
			continue
		}
		if edgeType != "" && e.Type != edgeType {
			continue
		}
		out = append(out, e)
	}
	return out
}

// NeighborIDs returns the IDs of nodes adjacent to nodeID (undirected),
// optionally filtered by edge type. Excludes nodeID itself.
func (g *LearningGraphService) NeighborIDs(nodeID string, edgeType domain.LearningEdgeType) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	seen := map[string]bool{nodeID: true}
	var out []string
	for _, e := range g.edges.List() {
		if e.InvalidAt != nil {
			continue
		}
		if edgeType != "" && e.Type != edgeType {
			continue
		}
		var other string
		if e.SourceID == nodeID {
			other = e.TargetID
		} else if e.TargetID == nodeID {
			other = e.SourceID
		} else {
			continue
		}
		if !seen[other] {
			seen[other] = true
			out = append(out, other)
		}
	}
	return out
}

// BFS expands from seed node IDs up to maxHops, returning all reachable
// node IDs (excluding the seeds themselves). Used as a graph search
// channel: seeds from BM25/embedding matches are expanded 2 hops to
// find related-but-lexically-different entries.
func (g *LearningGraphService) BFS(seeds []string, maxHops int) []string {
	if maxHops <= 0 || len(seeds) == 0 {
		return nil
	}
	visited := make(map[string]bool, len(seeds))
	for _, s := range seeds {
		visited[s] = true
	}
	frontier := seeds
	var out []string
	for hop := 0; hop < maxHops; hop++ {
		var next []string
		for _, node := range frontier {
			for _, n := range g.NeighborIDs(node, "") {
				if !visited[n] {
					visited[n] = true
					out = append(out, n)
					next = append(next, n)
				}
			}
		}
		frontier = next
		if len(frontier) == 0 {
			break
		}
	}
	return out
}

// AllEdges returns all edges (including invalidated ones) for inspection.
func (g *LearningGraphService) AllEdges() []*domain.LearningEdge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.edges.List()
}

// DeleteEdge permanently removes an edge from the store.
func (g *LearningGraphService) DeleteEdge(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.edges.Delete(id)
}

// SaveEdge updates an existing edge in the store. Used by consolidation
// to rewire edges when merging near-duplicate entries.
func (g *LearningGraphService) SaveEdge(e *domain.LearningEdge) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.edges.Save(e)
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
