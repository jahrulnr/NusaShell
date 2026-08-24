package application

import (
	"context"
	"testing"

	"nusashell/domain"
)

type stubPipelineDiscoverer struct {
	defs []*domain.WorkflowDefinition
	err  error
}

func (s *stubPipelineDiscoverer) Discover() ([]*domain.WorkflowDefinition, error) {
	return s.defs, s.err
}

func TestDiscoverPipelinesUpsertsAndEnables(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	svc.Pipelines = &stubPipelineDiscoverer{
		defs: []*domain.WorkflowDefinition{
			{
				ID:       "deploy",
				Name:     "Deploy",
				Enabled:  true,
				Triggers: []domain.Trigger{{ID: "t1", Kind: domain.TriggerManual, Family: domain.FamilyManual, Manual: true}},
				Jobs:     []domain.Job{{ID: "build", Steps: []domain.Step{{Run: "make build"}}}},
				Source:   domain.WorkflowSource{Kind: "file", Path: "/data/ci/pipelines/deploy.yaml"},
			},
			{
				ID:      "nightly",
				Name:    "Nightly",
				Enabled: true,
				Triggers: []domain.Trigger{{
					ID: "t1", Kind: domain.TriggerCron, Family: domain.FamilyEvery,
					Cron: "0 0 * * *",
				}},
				Jobs:   []domain.Job{{ID: "test", Steps: []domain.Step{{Run: "make test"}}}},
				Source: domain.WorkflowSource{Kind: "file", Path: "/data/ci/pipelines/nightly.yaml"},
			},
		},
	}

	loaded, err := svc.DiscoverPipelines(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPipelines: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 loaded workflows, got %d", len(loaded))
	}

	// Both should be in the WorkflowStore.
	for _, id := range []string{"deploy", "nightly"} {
		got, err := svc.Workflows.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("workflow %s not in store: %v", id, err)
		}
		if got.Source.Kind != "file" {
			t.Fatalf("workflow %s source kind = %q, want file", id, got.Source.Kind)
		}
	}

	// Nightly has a cron trigger → schedule should be registered.
	schedules, _ := svc.Schedules.List(context.Background())
	if len(schedules) == 0 {
		t.Fatal("expected at least one schedule for the cron trigger")
	}
	foundCron := false
	for _, s := range schedules {
		if s.WorkflowID == "nightly" {
			foundCron = true
		}
	}
	if !foundCron {
		t.Fatal("nightly cron schedule not registered")
	}
}

func TestDiscoverPipelinesEmpty(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	svc.Pipelines = &stubPipelineDiscoverer{}

	loaded, err := svc.DiscoverPipelines(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPipelines on empty: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 workflows, got %d", len(loaded))
	}
}

func TestDiscoverPipelinesIdempotent(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	svc.Pipelines = &stubPipelineDiscoverer{
		defs: []*domain.WorkflowDefinition{
			{
				ID:       "deploy",
				Name:     "Deploy",
				Enabled:  true,
				Triggers: []domain.Trigger{{ID: "t1", Kind: domain.TriggerManual, Family: domain.FamilyManual, Manual: true}},
				Jobs:     []domain.Job{{ID: "build", Steps: []domain.Step{{Run: "make build"}}}},
				Source:   domain.WorkflowSource{Kind: "file", Path: "/data/ci/pipelines/deploy.yaml"},
			},
		},
	}

	if _, err := svc.DiscoverPipelines(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DiscoverPipelines(context.Background()); err != nil {
		t.Fatal(err)
	}

	list, _ := svc.Workflows.List(context.Background())
	if len(list) != 1 {
		t.Fatalf("expected 1 workflow after 2 discoveries (idempotent), got %d", len(list))
	}
}
