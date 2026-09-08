package automation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
			Path:       "/data/automation/pipelines/bad.yaml",
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
	if err := svc.Sched.EnableWorkflow(context.Background(), w); err == nil {
		t.Fatal("expected enable to reject invalid yaml")
	}
	got, err := svc.Workflows.Get(context.Background(), "broken")
	if err == nil && got.Enabled {
		t.Fatal("invalid workflow must not be persisted as enabled")
	}
}

type failingScheduleStore struct {
	ScheduleStore
	err error
}

func (s failingScheduleStore) Put(context.Context, *domain.ScheduleRecord) error {
	return s.err
}

func TestEnableWorkflowRollsBackWhenScheduleRegistrationFails(t *testing.T) {
	svc, _, clock := testAutomation(t, &fakeExec{})
	sentinel := errors.New("schedule store unavailable")
	failing := failingScheduleStore{ScheduleStore: svc.Schedules, err: sentinel}
	svc.Sched.Schedules = failing
	at := clock.T.Add(time.Hour)
	w := &domain.WorkflowDefinition{
		ID:       "scheduled",
		Name:     "scheduled",
		Triggers: []domain.Trigger{{ID: "once", Kind: domain.TriggerOnce, Family: domain.FamilyOnce, At: &at}},
		Jobs:     []domain.Job{{ID: "job", Steps: []domain.Step{{ID: "step", Run: "echo"}}}},
	}

	err := svc.Sched.EnableWorkflow(context.Background(), w)
	if !errors.Is(err, sentinel) {
		t.Fatalf("EnableWorkflow error = %v, want %v", err, sentinel)
	}
	if w.Enabled {
		t.Fatal("workflow should be disabled after activation failure")
	}
	stored, getErr := svc.Workflows.Get(context.Background(), w.ID)
	if getErr != nil {
		t.Fatalf("read rolled-back workflow: %v", getErr)
	}
	if stored.Enabled {
		t.Fatal("persisted workflow should be disabled after activation failure")
	}
}

func TestSaveWorkflowRollsBackWhenActivationFails(t *testing.T) {
	svc, _, clock := testAutomation(t, &fakeExec{})
	sentinel := errors.New("schedule store unavailable")
	failing := failingScheduleStore{ScheduleStore: svc.Schedules, err: sentinel}
	svc.Schedules = failing
	svc.Sched.Schedules = failing
	at := clock.T.Add(time.Hour)
	w := &domain.WorkflowDefinition{
		ID:       "scheduled",
		Name:     "scheduled",
		Enabled:  true,
		Triggers: []domain.Trigger{{ID: "once", Kind: domain.TriggerOnce, Family: domain.FamilyOnce, At: &at}},
		Jobs:     []domain.Job{{ID: "job", Steps: []domain.Step{{ID: "step", Run: "echo"}}}},
	}

	got, _, err := svc.SaveWorkflow(context.Background(), w)
	if !errors.Is(err, sentinel) {
		t.Fatalf("SaveWorkflow error = %v, want %v", err, sentinel)
	}
	if got.Enabled {
		t.Fatal("returned workflow must be disabled after activation failure")
	}
	stored, getErr := svc.Workflows.Get(context.Background(), w.ID)
	if getErr != nil {
		t.Fatalf("read rolled-back workflow: %v", getErr)
	}
	if stored.Enabled {
		t.Fatal("persisted workflow must be disabled after activation failure")
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
