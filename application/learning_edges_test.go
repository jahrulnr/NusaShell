package application

import (
	"context"
	"testing"

	"nusashell/domain"
)

func TestBuildTokenOverlapEdges(t *testing.T) {
	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "use docker for container builds"},
		{ID: "mem_2", Content: "prefer postgres for the database"},
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
	// mem_1 should connect to skill_1 (docker overlap)
	neighbors := graph.Neighbors("mem_1", "")
	found := false
	for _, n := range neighbors {
		if n.TargetID == "skill_1" {
			found = true
		}
	}
	if !found {
		t.Error("expected edge mem_1 → skill_1 (docker overlap)")
	}
	// mem_2 should NOT connect to skill_2 (no overlap)
	neighbors2 := graph.Neighbors("mem_2", "")
	for _, n := range neighbors2 {
		if n.TargetID == "skill_2" {
			t.Error("did not expect edge mem_2 → skill_2 (no overlap)")
		}
	}
}

func TestBuildTokenOverlapMinLen(t *testing.T) {
	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "use go"},
	}}
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "go", Description: "", Content: "go programming"},
	}}
	graph := NewLearningGraphService(&fakeEdgeStore{})
	cfg := DefaultEdgeBuilderConfig()
	cfg.MinTokenLen = 3 // "go" is 2 chars, should be filtered
	b := NewEdgeBuilder(mem, skills, graph, nil, nil, cfg, "")
	b.Build(context.Background())
	neighbors := graph.Neighbors("mem_1", "")
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
	got := jaccardSimilarity(a, b)
	if got < 0.49 || got > 0.51 {
		t.Errorf("jaccard = %v, want ~0.5", got)
	}
}

func TestTokenizeForOverlap(t *testing.T) {
	toks := tokenizeForOverlap("Docker, container. Build!", 3)
	if !toks["docker"] || !toks["container"] || !toks["build"] {
		t.Errorf("missing tokens: %v", toks)
	}
	if toks["go"] {
		t.Error("'go' should be filtered (len < 3)")
	}
}

func TestEdgeBuilderIdempotent(t *testing.T) {
	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "docker container build"},
	}}
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill_1": {ID: "skill_1", Name: "docker", Description: "container", Content: "build images"},
	}}
	graph := NewLearningGraphService(&fakeEdgeStore{})
	b := NewEdgeBuilder(mem, skills, graph, nil, nil, DefaultEdgeBuilderConfig(), "")
	b.Build(context.Background())
	b.Build(context.Background()) // second run should strengthen, not duplicate
	neighbors := graph.Neighbors("mem_1", "")
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
