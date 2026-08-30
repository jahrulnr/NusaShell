package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestAvailabilityOfInvalidIncludesReason(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	w := &domain.WorkflowDefinition{ID: "broken", Name: "broken"}
	avail, reason := svc.AvailabilityOf(context.Background(), w)
	if avail != "invalid" {
		t.Fatalf("availability = %q, want invalid", avail)
	}
	if reason == "" {
		t.Fatal("invalid availability must include a reason")
	}
}

func TestAvailabilityOfParseErrorIsInvalid(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	w := &domain.WorkflowDefinition{
		ID:   "bad",
		Name: "bad",
		Source: domain.WorkflowSource{
			Kind:       "file",
			Path:       "/data/ci/pipelines/bad.yaml",
			ParseError: "yaml: mapping values are not allowed here",
		},
	}
	avail, reason := svc.AvailabilityOf(context.Background(), w)
	if avail != "invalid" {
		t.Fatalf("availability = %q, want invalid", avail)
	}
	if !strings.Contains(reason, "mapping values") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestEnableWorkflowRejectsInvalidSyntax(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	w := &domain.WorkflowDefinition{ID: "broken", Name: "broken"}
	if err := svc.Auto.EnableWorkflow(context.Background(), w); err == nil {
		t.Fatal("expected enable to reject invalid yaml")
	}
	got, err := svc.Workflows.Get(context.Background(), "broken")
	if err == nil && got.Enabled {
		t.Fatal("invalid workflow must not be persisted as enabled")
	}
}

func TestRunWorkflowRejectsInvalidSyntax(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	w := &domain.WorkflowDefinition{ID: "broken", Name: "broken"}
	if err := svc.Workflows.Put(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	run, err := svc.RunWorkflow(context.Background(), "broken", "ui")
	if err == nil {
		t.Fatal("expected run to reject invalid yaml")
	}
	if run != nil {
		t.Fatalf("invalid workflow must not start a run, got %+v", run)
	}
}
