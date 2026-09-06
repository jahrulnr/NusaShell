package application

import (
	"context"
	"nusashell/domain"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- from automation_scheduler_test.go ---

type fakeExec struct {
	mu    sync.Mutex
	Calls []string
	Fail  string
	Slow  time.Duration
}

func (f *fakeExec) Prepare(_ context.Context, req PrepareRequest) (ExecutionWorkspace, error) {
	return ExecutionWorkspace{Dir: req.Workspace}, nil
}
func (f *fakeExec) Cleanup(_ context.Context, _ CleanupRequest) error { return nil }
func (f *fakeExec) RunStep(ctx context.Context, req RunStepRequest) (StepResult, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, req.Job.ID+":"+req.Step.Run)
	fail := f.Fail
	slow := f.Slow
	f.mu.Unlock()
	if slow > 0 {
		select {
		case <-ctx.Done():
			return StepResult{Error: ctx.Err().Error()}, ctx.Err()
		case <-time.After(slow):
		}
	}
	if fail != "" && req.Job.ID == fail {
		return StepResult{ExitCode: 1, Error: "failed"}, nil
	}
	return StepResult{ExitCode: 0, Outputs: map[string]any{"status": "ok"}}, nil
}

func testAutomation(t *testing.T, exec JobExecutor) (*Automation, *AutomationStore, *FrozenClock) {
	t.Helper()
	mem := NewAutomationStore()
	clock := &FrozenClock{T: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)}
	caps := NewCapabilityRegistry()
	caps.Workflows = WorkflowMem{AutomationStore: mem}
	caps.State = ProviderStateMem{AutomationStore: mem}
	es := NewExecutionScheduler()
	es.Runs = RunMem{AutomationStore: mem}
	es.Logs = LogMem{AutomationStore: mem}
	es.Exec = exec
	es.Caps = caps
	es.Waits = WaitMem{AutomationStore: mem}
	es.Clock = clock
	es.Bus = NewBus()
	auto := &AutomationScheduler{
		Workflows: WorkflowMem{AutomationStore: mem},
		Schedules: ScheduleMem{AutomationStore: mem},
		Events:    EventMem{AutomationStore: mem},
		Waits:     WaitMem{AutomationStore: mem},
		Locks:     LockMem{AutomationStore: mem},
		Debounce:  DebounceMem{AutomationStore: mem},
		Caps:      caps,
		Exec:      es,
		Clock:     clock,
		Bus:       es.Bus,
	}
	return &Automation{
		Workflows: WorkflowMem{AutomationStore: mem},
		Runs:      RunMem{AutomationStore: mem},
		Schedules: ScheduleMem{AutomationStore: mem},
		Events:    EventMem{AutomationStore: mem},
		Exec:      es,
		Sched:     auto,
		Caps:      caps,
		Logs:      LogMem{AutomationStore: mem},
		Clock:     clock,
	}, mem, clock
}

// --- from automation_invalid_test.go ---

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

// --- from automation_discover_test.go ---

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
				Source:   domain.WorkflowSource{Kind: "file", Path: "/data/automation/pipelines/deploy.yaml"},
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
				Source: domain.WorkflowSource{Kind: "file", Path: "/data/automation/pipelines/nightly.yaml"},
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
				Source:   domain.WorkflowSource{Kind: "file", Path: "/data/automation/pipelines/deploy.yaml"},
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

func TestDiscoverPipelinesListsInvalidWithoutEnabling(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	svc.Pipelines = &stubPipelineDiscoverer{
		defs: []*domain.WorkflowDefinition{
			{
				ID:   "broken",
				Name: "Broken",
				Source: domain.WorkflowSource{
					Kind:       "file",
					Path:       "/data/automation/pipelines/broken.yaml",
					ParseError: "yaml: did not find expected key",
				},
			},
			{
				ID:       "empty-jobs",
				Name:     "Empty jobs",
				Triggers: []domain.Trigger{{ID: "t1", Kind: domain.TriggerManual, Family: domain.FamilyManual, Manual: true}},
				Source:   domain.WorkflowSource{Kind: "file", Path: "/data/automation/pipelines/empty-jobs.yaml"},
			},
		},
	}

	loaded, err := svc.DiscoverPipelines(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPipelines: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("invalid pipelines must still be listed, got %d", len(loaded))
	}

	for _, id := range []string{"broken", "empty-jobs"} {
		got, err := svc.Workflows.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("workflow %s not listed: %v", id, err)
		}
		if got.Enabled {
			t.Fatalf("workflow %s must not be enabled", id)
		}
		avail, reason := svc.AvailabilityOf(context.Background(), got)
		if avail != "invalid" {
			t.Fatalf("workflow %s availability = %q, want invalid", id, avail)
		}
		if reason == "" {
			t.Fatalf("workflow %s invalid reason is empty", id)
		}
	}

	schedules, _ := svc.Schedules.List(context.Background())
	if len(schedules) != 0 {
		t.Fatalf("invalid pipelines must not register schedules, got %d", len(schedules))
	}
}
