package application

import (
	"context"
	"testing"

	"nusashell/application/service/textsim"
	"nusashell/domain"
)

func TestBuildTokenOverlapEdges(t *testing.T) {
	mem := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "frag_1", Status: domain.MemoryStatusLearned, Body: "use docker for container builds"},
		{ID: "frag_2", Status: domain.MemoryStatusLearned, Body: "prefer postgres for the database"},
		{ID: "frag_3", Status: domain.MemoryStatusLearned, Body: "docker compose for local container orchestration"},
	}}
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "docker", Description: "container builds", Content: "how to build docker images"},
		"skill_2": {ID: "skill_2", Name: "git", Description: "version control", Content: "git rebase workflow"},
	}}
	graph := NewLearningGraphService(&fakeEdgeStore{})
	b := NewEdgeBuilder(mem, skills, graph, nil, nil, DefaultEdgeBuilderConfig(), "")
	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	// frag_1 should connect to skill_1 (docker overlap)
	neighbors := graph.Neighbors("frag_1", "")
	found := false
	for _, n := range neighbors {
		if n.TargetID == "skill_1" {
			found = true
		}
	}
	if !found {
		t.Error("expected edge frag_1 → skill_1 (docker overlap)")
	}
	// frag_2 should NOT connect to skill_2 (no overlap)
	neighbors2 := graph.Neighbors("frag_2", "")
	for _, n := range neighbors2 {
		if n.TargetID == "skill_2" {
			t.Error("did not expect edge frag_2 → skill_2 (no overlap)")
		}
	}
	// frag_1 and frag_3 share docker/container tokens → related edge
	// (memory-to-memory chain, not just spokes to skills).
	neighbors3 := graph.Neighbors("frag_1", "")
	foundMM := false
	for _, n := range neighbors3 {
		if n.TargetID == "frag_3" {
			foundMM = true
		}
	}
	if !foundMM {
		t.Error("expected edge frag_1 → frag_3 (docker/container overlap)")
	}
}

func TestBuildTokenOverlapSkipsUnrelatedMemoryPairs(t *testing.T) {
	mem := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "frag_1", Status: domain.MemoryStatusLearned, Body: "prefer postgres for the database"},
		{ID: "frag_2", Status: domain.MemoryStatusLearned, Body: "user likes dark mode and prefers Indonesian"},
	}}
	graph := NewLearningGraphService(&fakeEdgeStore{})
	b := NewEdgeBuilder(mem, &fakeSkillStore{}, graph, nil, nil, DefaultEdgeBuilderConfig(), "")
	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, n := range graph.Neighbors("frag_1", "") {
		if n.TargetID == "frag_2" || n.SourceID == "frag_2" {
			t.Error("did not expect edge between unrelated fragments")
		}
	}
}

func TestBuildRelatedEdgesUsesLearningMetadataWhenContentIsSparse(t *testing.T) {
	mem := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "frag_frontend_a", Body: "mobile shell", Status: domain.MemoryStatusLearned, Type: domain.MemoryTypeFact, Scope: domain.MemoryScope{Project: "NusaShell", Domain: "frontend"}, Subject: "mobile"},
		{ID: "frag_frontend_b", Body: "responsive shell", Status: domain.MemoryStatusLearned, Type: domain.MemoryTypeFact, Scope: domain.MemoryScope{Project: "NusaShell", Domain: "frontend"}, Subject: "mobile"},
		{ID: "frag_unrelated", Body: "recipe ingredients", Status: domain.MemoryStatusLearned, Type: domain.MemoryTypeEpisode, Scope: domain.MemoryScope{Project: "Cooking", Domain: "recipe"}},
	}}
	graph := NewLearningGraphService(&fakeEdgeStore{})
	b := NewEdgeBuilder(mem, &fakeSkillStore{}, graph, nil, nil, DefaultEdgeBuilderConfig(), "")

	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !hasEdge(graph.AllEdges(), "frag_frontend_a", "frag_frontend_b", domain.EdgeRelated) {
		t.Fatal("expected related edge from shared project/tags even when prose has little token overlap")
	}
	if hasEdge(graph.AllEdges(), "frag_frontend_a", "frag_unrelated", domain.EdgeRelated) {
		t.Fatal("did not expect related edge for unrelated metadata")
	}
}

func TestBuildPrunesDanglingEdges(t *testing.T) {
	store := &fakeEdgeStore{edges: []*domain.LearningEdge{
		{ID: "dangling", SourceID: "frag_live", TargetID: "frag_deleted", Type: domain.EdgeRelated},
	}}
	mem := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{{ID: "frag_live", Status: domain.MemoryStatusLearned, Body: "live fact"}}}
	graph := NewLearningGraphService(store)
	b := NewEdgeBuilder(mem, &fakeSkillStore{}, graph, nil, nil, DefaultEdgeBuilderConfig(), "")

	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(graph.AllEdges()) != 0 {
		t.Fatalf("edges = %+v, want dangling edge removed", graph.AllEdges())
	}
}

func TestLearningNodeIDsFromToolOutput(t *testing.T) {
	app := &App{
		DataDir: "/tmp/nusashell",
		Skills: &fakeSkillStore{items: map[string]*domain.Skill{
			"skill_frontend": {ID: "skill_frontend", Name: "frontend"},
		}},
		User: &fakeUserStore{entries: []domain.DocumentEntry{{ID: "user_1", Content: "user"}}},
	}

	memoryIDs := learningNodeIDsFromTool(app,
		domain.ToolCall{Name: "memory", Args: `{"op":"search","query":"frontend"}`},
		"---\ncount: 1\n---\n{\"id\":\"frag_1\",\"content\":\"frontend\"}",
	)
	if !containsString(memoryIDs, "frag_1") {
		t.Fatalf("memory IDs = %v, want frag_1", memoryIDs)
	}
	skillIDs := learningNodeIDsFromTool(app,
		domain.ToolCall{Name: "skill", Args: `{"op":"search","query":"frontend"}`},
		"---\ncount: 1\n---\n{\"id\":\"skill_frontend\",\"name\":\"frontend\"}",
	)
	if !containsString(skillIDs, "skill_frontend") {
		t.Fatalf("skill IDs = %v, want skill_frontend", skillIDs)
	}
}

func TestRecordLearningTurnNodesConnectsAcrossToolRounds(t *testing.T) {
	store := &fakeEdgeStore{}
	app := &App{LearningEdges: store}
	run := &TurnRun{}

	app.recordLearningTurnNodes(run, []string{"frag_1"})
	app.recordLearningTurnNodes(run, []string{"skill_1"})
	app.recordLearningTurnNodes(run, []string{"frag_1", "skill_1"})

	if got := len(store.edges); got != 1 {
		t.Fatalf("edge count = %d, want one edge after repeated observations", got)
	}
	if !hasEdge(store.edges, "frag_1", "skill_1", domain.EdgeUsedWith) {
		t.Fatalf("edges = %+v, want used_with edge", store.edges)
	}
}

func TestRecordLearningUsageCreatesUsedWithEdges(t *testing.T) {
	store := &fakeEdgeStore{}
	app := &App{LearningEdges: store}
	app.recordLearningUsage([]string{"frag_1", "skill_1", "frag_1"})

	if !hasEdge(store.edges, "frag_1", "skill_1", domain.EdgeUsedWith) {
		t.Fatalf("edges = %+v, want used_with edge", store.edges)
	}
	if got := len(store.edges); got != 1 {
		t.Fatalf("edge count = %d, want one deduplicated used_with edge", got)
	}
}

func hasEdge(edges []*domain.LearningEdge, left, right string, edgeType domain.LearningEdgeType) bool {
	for _, edge := range edges {
		if edge.Type != edgeType {
			continue
		}
		if (edge.SourceID == left && edge.TargetID == right) || (edge.SourceID == right && edge.TargetID == left) {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestBuildTokenOverlapMinLen(t *testing.T) {
	mem := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "frag_1", Status: domain.MemoryStatusLearned, Body: "use go"},
	}}
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "go", Description: "", Content: "go programming"},
	}}
	graph := NewLearningGraphService(&fakeEdgeStore{})
	cfg := DefaultEdgeBuilderConfig()
	cfg.MinTokenLen = 3 // "go" is 2 chars, should be filtered
	b := NewEdgeBuilder(mem, skills, graph, nil, nil, cfg, "")
	b.Build(context.Background())
	neighbors := graph.Neighbors("frag_1", "")
	for _, n := range neighbors {
		if n.TargetID == "skill_1" {
			t.Error("expected no edge (tokens too short)")
		}
	}
}

func TestJaccardSimilarity(t *testing.T) {
	a := map[string]bool{"docker": true, "container": true, "build": true}
	b := map[string]bool{"docker": true, "container": true, "image": true}
	// intersection=2, union=4, jaccard=0.5
	got := textsim.JaccardSimilarity(a, b)
	if got < 0.49 || got > 0.51 {
		t.Errorf("jaccard = %v, want ~0.5", got)
	}
}

func TestTokenizeForOverlap(t *testing.T) {
	toks := textsim.TokenizeForOverlap("Docker, container. Build!", 3)
	if !toks["docker"] || !toks["container"] || !toks["build"] {
		t.Errorf("missing tokens: %v", toks)
	}
	if toks["go"] {
		t.Error("'go' should be filtered (len < 3)")
	}
}

func TestEdgeBuilderIdempotent(t *testing.T) {
	mem := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "frag_1", Status: domain.MemoryStatusLearned, Body: "docker container build"},
	}}
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "docker", Description: "container", Content: "build images"},
	}}
	graph := NewLearningGraphService(&fakeEdgeStore{})
	b := NewEdgeBuilder(mem, skills, graph, nil, nil, DefaultEdgeBuilderConfig(), "")
	b.Build(context.Background())
	b.Build(context.Background()) // second run should strengthen, not duplicate
	neighbors := graph.Neighbors("frag_1", "")
	count := 0
	for _, n := range neighbors {
		if n.TargetID == "skill_1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 edge (strengthened), got %d", count)
	}
}
