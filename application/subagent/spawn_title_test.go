package subagent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"nusashell/domain"
)

type titleCaptureRuntime struct {
	Runtime
	last SpawnRequest
}

func (r *titleCaptureRuntime) Spawn(_ context.Context, req SpawnRequest) (*domain.AcpRun, error) {
	r.last = req
	return &domain.AcpRun{
		TaskState: domain.TaskState[domain.AcpRunStatus]{
			ID:     "acprun_title",
			Status: domain.AcpRunRunning,
		},
		AgentID:   req.Agent.ID,
		AgentName: req.Agent.Name,
		Title:     req.Title,
		Prompt:    req.Prompt,
	}, nil
}

type titleAgentStore struct {
	agent *domain.AcpAgent
}

func (s titleAgentStore) List() []*domain.AcpAgent { return []*domain.AcpAgent{s.agent} }
func (s titleAgentStore) Get(id string) (*domain.AcpAgent, error) {
	if id != "" && id != s.agent.ID {
		return nil, fmt.Errorf("unknown agent")
	}
	return s.agent, nil
}
func (s titleAgentStore) Save(*domain.AcpAgent) error { return nil }
func (s titleAgentStore) Delete(string) error         { return nil }

func TestSpawnSubagentsPassesTitle(t *testing.T) {
	rt := &titleCaptureRuntime{}
	agent := &domain.AcpAgent{ID: "acp_1", Name: "Codex", Enabled: true, DefaultWorkspace: t.TempDir()}
	svc := New(Deps{Runtime: rt, Agents: titleAgentStore{agent: agent}})
	out, err := svc.SpawnSubagents(context.Background(), "conv_1", "tool_1", []byte(`{"prompt":"do work","title":"  Inspect pets  "}`))
	if err != nil {
		t.Fatal(err)
	}
	if rt.last.Title != "Inspect pets" {
		t.Fatalf("spawn title = %q, want Inspect pets", rt.last.Title)
	}
	if !strings.Contains(out, "title: Inspect pets") {
		t.Fatalf("spawn result missing title:\n%s", out)
	}
}

func TestSpawnDelegateRegistersTitle(t *testing.T) {
	svc := New(Deps{
		ResolveModel: func(string) (string, error) { return "p:m", nil },
		Go:           func(_ string, fn func()) { /* do not run background */ },
	})
	_, err := svc.SpawnDelegate(context.Background(), "conv_1", "tool_1", []byte(`{"prompt":"do work","title":"Review PR"}`))
	if err != nil {
		t.Fatal(err)
	}
	runs := svc.delegates.List("conv_1")
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	if runs[0].Title != "Review PR" {
		t.Fatalf("title = %q", runs[0].Title)
	}
	if domain.AcpRunLabel(runs[0]) != "Review PR" {
		t.Fatalf("label = %q", domain.AcpRunLabel(runs[0]))
	}
}
