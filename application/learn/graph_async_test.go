package learn

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type graphEdgeStore struct {
	edges []*domain.LearningEdge
	saves int
}

func (s *graphEdgeStore) List() []*domain.LearningEdge { return s.edges }
func (s *graphEdgeStore) Save(edge *domain.LearningEdge) error {
	s.saves++
	s.edges = append(s.edges, edge)
	return nil
}
func (s *graphEdgeStore) Delete(id string) error {
	for i, edge := range s.edges {
		if edge != nil && edge.ID == id {
			s.edges = append(s.edges[:i], s.edges[i+1:]...)
			break
		}
	}
	return nil
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
