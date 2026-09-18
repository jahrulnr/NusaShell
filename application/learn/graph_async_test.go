package learn

import (
	"context"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type graphEdgeStore struct {
	edges   []*domain.LearningEdge
	lists   int
	saves   int
	deletes int
}

func (s *graphEdgeStore) List() []*domain.LearningEdge {
	s.lists++
	return s.edges
}
func (s *graphEdgeStore) Save(edge *domain.LearningEdge) error {
	s.saves++
	s.edges = append(s.edges, edge)
	return nil
}
func (s *graphEdgeStore) Delete(id string) error {
	s.deletes++
	for i, edge := range s.edges {
		if edge != nil && edge.ID == id {
			s.edges = append(s.edges[:i], s.edges[i+1:]...)
			break
		}
	}
	return nil
}

func TestHandleLearningGraphQueuesOneBuildForAnUnchangedCatalog(t *testing.T) {
	records := &scopeRecordStore{records: []*domain.MemoryRecord{
		{ID: "memory-1", Body: "docker container workflow", Status: domain.MemoryStatusLearned},
	}}
	skills := &listSkillCatalog{skills: []*domain.Skill{
		{ID: "skill-1", Name: "docker", Content: "docker container workflow", Status: domain.SkillStatusTrusted},
	}}
	edges := &graphEdgeStore{}
	queued := make([]func(), 0, 2)
	service := New(Deps{
		Records: records,
		Skills:  skills,
		Edges:   edges,
		Go: func(_ string, fn func()) {
			queued = append(queued, fn)
		},
	})
	service.InitEdgeBuilder()

	if _, rpcErr := service.HandleLearningGraph(); rpcErr != nil {
		t.Fatalf("first graph request: %v", rpcErr)
	}
	if _, rpcErr := service.HandleLearningGraph(); rpcErr != nil {
		t.Fatalf("second graph request: %v", rpcErr)
	}
	if len(queued) != 1 {
		t.Fatalf("queued builds = %d, want one while the first build is pending", len(queued))
	}

	queued[0]()
	records.records[0].Body = "updated docker container workflow"
	if _, rpcErr := service.HandleLearningGraph(); rpcErr != nil {
		t.Fatalf("changed graph request: %v", rpcErr)
	}
	if len(queued) != 2 {
		t.Fatalf("queued builds after catalog change = %d, want two", len(queued))
	}
}

func TestEdgeBuilderDoesNotRewriteAnAlreadyStrongDerivedEdge(t *testing.T) {
	records := &scopeRecordStore{records: []*domain.MemoryRecord{
		{ID: "memory-1", Body: "docker container workflow", Status: domain.MemoryStatusLearned},
	}}
	skills := &listSkillCatalog{skills: []*domain.Skill{
		{ID: "skill-1", Name: "docker", Content: "docker container workflow", Status: domain.SkillStatusTrusted},
	}}
	edges := &graphEdgeStore{edges: []*domain.LearningEdge{{
		ID: "edge-1", SourceID: "memory-1", TargetID: "skill-1", Type: domain.EdgeRelated, Weight: 0.9,
	}}}
	graph := NewLearningGraphService(edges)
	builder := NewEdgeBuilder(records, skills, graph, nil, nil, DefaultEdgeBuilderConfig(), "")

	if err := builder.Build(context.Background()); err != nil {
		t.Fatalf("build: %v", err)
	}
	if edges.saves != 0 || edges.deletes != 0 {
		t.Fatalf("derived edge was rewritten: saves=%d deletes=%d", edges.saves, edges.deletes)
	}
	if edges.lists > 2 {
		t.Fatalf("edge store was listed %d times, want one graph snapshot plus one inspection", edges.lists)
	}
}

func TestHandleLearningGraphQueuesEdgeBuildWithoutBlockingRPC(t *testing.T) {
	records := &scopeRecordStore{records: []*domain.MemoryRecord{
		{ID: "memory-1", Body: "docker container workflow", Status: domain.MemoryStatusLearned},
	}}
	skills := &listSkillCatalog{skills: []*domain.Skill{
		{ID: "skill-1", Name: "docker", Content: "docker container workflow", Status: domain.SkillStatusTrusted},
	}}
	edges := &graphEdgeStore{}
	var queuedName string
	var queued func()
	service := New(Deps{
		Records: records,
		Skills:  skills,
		Edges:   edges,
		Go: func(name string, fn func()) {
			queuedName = name
			queued = fn
		},
	})
	service.InitEdgeBuilder()

	response, rpcErr := service.HandleLearningGraph()
	if rpcErr != nil {
		t.Fatalf("HandleLearningGraph: %v", rpcErr)
	}
	result, ok := response.(contracts.LearningGraphResult)
	if !ok {
		t.Fatalf("response type = %T, want contracts.LearningGraphResult", response)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(result.Nodes))
	}
	if queued == nil {
		t.Fatal("learning graph build was not queued")
	}
	if queuedName != "learning" {
		t.Fatalf("queued job name = %q, want learning", queuedName)
	}
	if edges.saves != 0 {
		t.Fatalf("edge builder ran during RPC: saves = %d", edges.saves)
	}

	queued()
	if edges.saves == 0 {
		t.Fatal("queued graph build did not persist any edge")
	}
}
