package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"nusashell/domain"
)

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

type nopEmitter struct{}

func (nopEmitter) Emit(string, any) {}

type stubCaps struct{}

type failingEventCaps struct {
	stubCaps
	err error
}

func (c failingEventCaps) Resolve(_ context.Context, name string, _ domain.AutoStartPolicy) (domain.CapabilityBinding, error) {
	return domain.CapabilityBinding{Capability: name, Kind: domain.CapabilityMCP, Status: domain.CapNotRunning}, nil
}

func (c failingEventCaps) EnsureAvailable(_ context.Context, b domain.CapabilityBinding, _ domain.AutoStartPolicy) (domain.CapabilityBinding, error) {
	return b, c.err
}

func (stubCaps) Resolve(_ context.Context, name string, _ domain.AutoStartPolicy) (domain.CapabilityBinding, error) {
	return domain.CapabilityBinding{Capability: name, Status: domain.CapMissing}, fmt.Errorf("unknown capability %q", name)
}
func (stubCaps) EnsureAvailable(_ context.Context, b domain.CapabilityBinding, _ domain.AutoStartPolicy) (domain.CapabilityBinding, error) {
	return b, nil
}
func (stubCaps) Execute(_ context.Context, _ domain.CapabilityBinding, _ json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("not configured")
}
func (stubCaps) List(_ context.Context) []domain.CapabilityBinding { return nil }
func (stubCaps) Dependents(_ context.Context, _ string) ([]*domain.WorkflowDefinition, error) {
	return nil, nil
}
func (stubCaps) SetDisabled(_ context.Context, _ string, _ bool) error { return nil }

func testAutomation(t *testing.T, exec JobExecutor) (*Automation, *AutomationStore, *FrozenClock) {
	t.Helper()
	mem := NewAutomationStore()
	clock := &FrozenClock{T: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)}
	caps := stubCaps{}
	es := NewExecutionScheduler()
	es.Runs = RunMem{AutomationStore: mem}
	es.Logs = LogMem{AutomationStore: mem}
	es.Exec = exec
	es.Caps = caps
	es.Waits = WaitMem{AutomationStore: mem}
	es.Clock = clock
	es.Bus = nopEmitter{}
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

func TestSchedulerParallelDAG(t *testing.T) {
	fx := &fakeExec{}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID:   "verify",
		Name: "verify",
		Jobs: []domain.Job{
			{ID: "frontend", Steps: []domain.Step{{ID: "s", Run: "fe"}}},
			{ID: "backend", Steps: []domain.Step{{ID: "s", Run: "be"}}},
			{ID: "build", Needs: []domain.JobNeed{{Job: "frontend"}, {Job: "backend"}}, Steps: []domain.Step{{ID: "s", Run: "build"}}},
		},
	}
	run := NewWorkflowRun(*w, "test")
	if err := svc.Exec.StartRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusSuccess {
		t.Fatalf("status = %s jobs=%+v", got.Status, got.Jobs)
	}
	fx.mu.Lock()
	n := len(fx.Calls)
	fx.mu.Unlock()
	if n != 3 {
		t.Fatalf("calls = %d %v", n, fx.Calls)
	}
}

func TestSchedulerFailureBlocksDependents(t *testing.T) {
	fx := &fakeExec{Fail: "test"}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID: "p", Name: "p",
		Jobs: []domain.Job{
			{ID: "test", Steps: []domain.Step{{ID: "s", Run: "t"}}},
			{ID: "build", Needs: []domain.JobNeed{{Job: "test"}}, Steps: []domain.Step{{ID: "s", Run: "b"}}},
		},
	}
	run := NewWorkflowRun(*w, "test")
	_ = svc.Exec.StartRun(context.Background(), run)
	got, _ := svc.Runs.Get(context.Background(), run.ID)
	if got.JobRunByID("test").Status != domain.StatusFailed {
		t.Fatalf("test status %s", got.JobRunByID("test").Status)
	}
	if got.JobRunByID("build").Status != domain.StatusBlocked {
		t.Fatalf("build should be blocked, got %s", got.JobRunByID("build").Status)
	}
}

func TestWaitUntilDoesNotOccupyExecutor(t *testing.T) {
	fx := &fakeExec{}
	svc, _, clock := testAutomation(t, fx)
	wake := clock.T.Add(2 * time.Hour)
	w := &domain.WorkflowDefinition{
		ID: "reminder", Name: "reminder",
		Jobs: []domain.Job{{ID: "send", Steps: []domain.Step{
			{ID: "wait", WaitUntil: &wake},
			{ID: "mail", Run: "send"},
		}}},
	}
	run := NewWorkflowRun(*w, "test")
	if err := svc.Exec.StartRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Runs.Get(context.Background(), run.ID)
	if got.Status != domain.StatusWaiting {
		t.Fatalf("status = %s want waiting", got.Status)
	}
	fx.mu.Lock()
	calls := len(fx.Calls)
	fx.mu.Unlock()
	if calls != 0 {
		t.Fatalf("executor ran while waiting: %v", fx.Calls)
	}
	clock.Advance(3 * time.Hour)
	if err := svc.Sched.resumeWaits(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Runs.Get(context.Background(), run.ID)
	if got.Status != domain.StatusSuccess {
		t.Fatalf("after wake status = %s jobs=%+v", got.Status, got.Jobs)
	}
}

func TestOnceTriggerFires(t *testing.T) {
	fx := &fakeExec{}
	svc, _, clock := testAutomation(t, fx)
	at := clock.T.Add(time.Hour)
	w := &domain.WorkflowDefinition{
		ID: "once", Name: "once", Enabled: true,
		Triggers: []domain.Trigger{{ID: "t1", Kind: domain.TriggerOnce, At: &at, Timezone: "UTC"}},
		Jobs:     []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	if err := svc.Sched.EnableWorkflow(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sched.FireDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, _ := svc.Runs.List(context.Background(), RunFilter{WorkflowID: "once"})
	if len(runs) != 0 {
		t.Fatalf("should not fire early, got %d", len(runs))
	}
	clock.Advance(2 * time.Hour)
	if err := svc.Sched.FireDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, _ = svc.Runs.List(context.Background(), RunFilter{WorkflowID: "once"})
	if len(runs) != 1 {
		t.Fatalf("got %d runs", len(runs))
	}
}

type failingEventStore struct {
	EventStore
	err error
}

func (s failingEventStore) PutEvent(context.Context, *domain.Event) error {
	return s.err
}

func TestIngestEventRejectsMissingType(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "missing-type"}); err == nil {
		t.Fatal("expected missing event type to be rejected")
	}
}

func TestIngestEventRequiresEventStore(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	svc.Sched.Events = nil
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "e1", Type: "tick"}); err == nil {
		t.Fatal("expected event store requirement")
	}
}

func TestIngestEventPropagatesEventPersistenceFailure(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	svc.Sched.Events = failingEventStore{EventStore: svc.Sched.Events, err: fmt.Errorf("event store unavailable")}
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "e1", Type: "tick"}); err == nil {
		t.Fatal("expected event persistence failure")
	}
}

func TestEnableWorkflowPropagatesEventProviderFailure(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	sentinel := errors.New("provider start failed")
	svc.Sched.Caps = failingEventCaps{err: sentinel}
	w := &domain.WorkflowDefinition{
		ID:       "events",
		Name:     "events",
		Enabled:  true,
		Triggers: []domain.Trigger{{ID: "message", Kind: domain.TriggerEvent, Event: "telegram.message"}},
		Jobs:     []domain.Job{{ID: "job", Steps: []domain.Step{{ID: "step", Run: "echo"}}}},
	}

	err := svc.Sched.EnableWorkflow(context.Background(), w)
	if !errors.Is(err, sentinel) {
		t.Fatalf("EnableWorkflow error = %v, want %v", err, sentinel)
	}
	stored, getErr := svc.Workflows.Get(context.Background(), w.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.Enabled {
		t.Fatal("workflow must be disabled after event provider activation failure")
	}
}

func TestFireDuePropagatesRescheduleFailure(t *testing.T) {
	svc, _, clock := testAutomation(t, &fakeExec{})
	base := svc.Schedules
	sentinel := errors.New("schedule update unavailable")
	svc.Sched.Schedules = failingScheduleStore{ScheduleStore: base, err: sentinel}
	at := clock.T.Add(-time.Minute)
	w := &domain.WorkflowDefinition{
		ID:       "interval",
		Name:     "interval",
		Enabled:  true,
		Triggers: []domain.Trigger{{ID: "every", Kind: domain.TriggerInterval, Family: domain.FamilyEvery, Interval: time.Hour}},
		Jobs:     []domain.Job{{ID: "job", Steps: []domain.Step{{ID: "step", Run: "echo"}}}},
	}
	if err := svc.Workflows.Put(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if err := base.Put(context.Background(), &domain.ScheduleRecord{
		ID: "every", WorkflowID: w.ID, TriggerID: "every", Kind: domain.TriggerInterval,
		RunAt: at, NextRunAt: at, Status: domain.SchedulePending, CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}

	err := svc.Sched.FireDue(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("FireDue error = %v, want %v", err, sentinel)
	}
	stored, getErr := base.List(context.Background())
	if getErr != nil {
		t.Fatal(getErr)
	}
	if len(stored) != 1 || stored[0].Status != domain.ScheduleFired {
		t.Fatalf("claimed schedule = %+v, want fired after failed reschedule", stored)
	}
}

func TestDisableWorkflowCancelsPendingSchedules(t *testing.T) {
	svc, _, clock := testAutomation(t, &fakeExec{})
	at := clock.T.Add(time.Hour)
	w := &domain.WorkflowDefinition{
		ID:       "disable-me",
		Name:     "disable-me",
		Triggers: []domain.Trigger{{ID: "once", Kind: domain.TriggerOnce, Family: domain.FamilyOnce, At: &at}},
		Jobs:     []domain.Job{{ID: "job", Steps: []domain.Step{{ID: "step", Run: "echo"}}}},
	}
	if err := svc.Sched.EnableWorkflow(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sched.DisableWorkflow(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Workflows.Get(context.Background(), w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Fatal("disabled workflow remained enabled")
	}
	schedules, err := svc.Schedules.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 1 || schedules[0].Status != domain.ScheduleCancelled {
		t.Fatalf("schedules = %+v, want one cancelled schedule", schedules)
	}
	clock.Advance(2 * time.Hour)
	if err := svc.Sched.FireDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, err := svc.Runs.List(context.Background(), RunFilter{WorkflowID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("disabled workflow started %d runs", len(runs))
	}
}

func TestEventIdempotentDelivery(t *testing.T) {
	fx := &fakeExec{}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID: "mail", Name: "mail", Enabled: true,
		Triggers: []domain.Trigger{{ID: "t1", Kind: domain.TriggerEvent, Event: "email.received", Where: map[string]any{"mailbox": "finance"}}},
		Jobs:     []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	_ = svc.Workflows.Put(context.Background(), w)
	ev := domain.Event{ID: "e1", Type: "email.received", Subject: "Invoice", Attributes: map[string]any{"mailbox": "finance"}}
	if err := svc.Sched.IngestEvent(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sched.IngestEvent(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	runs, _ := svc.Runs.List(context.Background(), RunFilter{WorkflowID: "mail"})
	if len(runs) != 1 {
		t.Fatalf("duplicate delivery created %d runs", len(runs))
	}
}

type failingCreateRunStore struct {
	PipelineRunStore
	err error
}

func (s failingCreateRunStore) Create(context.Context, *domain.WorkflowRun) error {
	return s.err
}

func TestIngestEventRollsBackDeliveryWhenRunStartFails(t *testing.T) {
	svc, mem, _ := testAutomation(t, &fakeExec{})
	sentinel := errors.New("run store unavailable")
	svc.Exec.Runs = failingCreateRunStore{PipelineRunStore: svc.Exec.Runs, err: sentinel}
	w := &domain.WorkflowDefinition{
		ID:       "mail-rollback",
		Name:     "mail-rollback",
		Enabled:  true,
		Triggers: []domain.Trigger{{ID: "message", Kind: domain.TriggerEvent, Event: "email.received"}},
		Jobs:     []domain.Job{{ID: "job", Steps: []domain.Step{{ID: "step", Run: "echo"}}}},
	}
	if err := svc.Workflows.Put(context.Background(), w); err != nil {
		t.Fatal(err)
	}

	err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "e-rollback", Type: "email.received"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("IngestEvent error = %v, want %v", err, sentinel)
	}
	if len(mem.Deliveries) != 0 {
		t.Fatalf("delivery claim was not rolled back: %v", mem.Deliveries)
	}
}

func TestBlockedWhenCapabilityMissing(t *testing.T) {
	fx := &fakeExec{}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID: "inv", Name: "Invoice", Enabled: true,
		Triggers: []domain.Trigger{{ID: "t1", Kind: domain.TriggerEvent, Event: "email.received"}},
		Jobs:     []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Uses: "email.read"}}}},
	}
	r := svc.Sched.Validate(context.Background(), w)
	if r.Verdict() != "INVALID" {
		t.Fatalf("missing capability should be INVALID, got %s %+v", r.Verdict(), r.Issues)
	}
}

func TestConcurrencySkip(t *testing.T) {
	fx := &fakeExec{Slow: 50 * time.Millisecond}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID: "mon", Name: "mon", Enabled: true,
		Concurrency: domain.Concurrency{Key: "mon", Policy: domain.ConcurrencySkip},
		Triggers:    []domain.Trigger{{ID: "t1", Kind: domain.TriggerEvent, Event: "tick"}},
		Jobs:        []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	_ = svc.Workflows.Put(context.Background(), w)
	existing := NewWorkflowRun(*w, "test")
	existing.Status = domain.StatusRunning
	if err := svc.Exec.Runs.Create(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
	svc.LocksAcquireForTest(w.Concurrency.Key, existing.ID)
	svc.Exec.trackConcurrencyLock(existing.ID, w.Concurrency.Key, svc.Sched.Locks)
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "e2", Type: "tick"}); err != nil {
		t.Fatal(err)
	}
	runs, _ := svc.Runs.List(context.Background(), RunFilter{WorkflowID: "mon"})
	started := 0
	for _, r := range runs {
		if r.ID != existing.ID {
			started++
		}
	}
	if started != 0 {
		t.Fatalf("skip should not start a run, got %d new runs", started)
	}
}

func (a *Automation) LocksAcquireForTest(key, runID string) {
	_ = a.Sched.Locks.Acquire(context.Background(), key, runID)
}

func TestStartRunAsyncReturnsImmediately(t *testing.T) {
	fx := &fakeExec{Slow: 100 * time.Millisecond}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID:   "async",
		Name: "async",
		Jobs: []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	run := NewWorkflowRun(*w, "test")
	start := time.Now()
	if err := svc.Exec.StartRunAsync(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Fatalf("StartRunAsync blocked for %v, should return immediately", elapsed)
	}
	got, err := svc.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusQueued && got.Status != domain.StatusRunning && got.Status != domain.StatusSuccess {
		t.Fatalf("unexpected initial status %s", got.Status)
	}
}

func TestStartRunAsyncCompletesInBackground(t *testing.T) {
	fx := &fakeExec{}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID:   "async-done",
		Name: "async-done",
		Jobs: []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	run := NewWorkflowRun(*w, "test")
	if err := svc.Exec.StartRunAsync(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := svc.Runs.Get(context.Background(), run.ID)
		if got.Status.IsTerminal() {
			if got.Status != domain.StatusSuccess {
				t.Fatalf("expected success, got %s", got.Status)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run did not complete within 2s")
}

func TestWaitRunBlocksUntilTerminal(t *testing.T) {
	fx := &fakeExec{Slow: 50 * time.Millisecond}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID:   "wait",
		Name: "wait",
		Jobs: []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	run := NewWorkflowRun(*w, "test")
	if err := svc.Exec.StartRunAsync(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, err := svc.WaitRun(context.Background(), run.ID, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Status.IsTerminal() {
		t.Fatalf("expected terminal status, got %s", got.Status)
	}
}

func TestWaitRunTimeoutReturnsNonTerminal(t *testing.T) {
	fx := &fakeExec{Slow: 2 * time.Second}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID:   "wait-timeout",
		Name: "wait-timeout",
		Jobs: []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	run := NewWorkflowRun(*w, "test")
	if err := svc.Exec.StartRunAsync(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, err := svc.WaitRun(context.Background(), run.ID, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.IsTerminal() {
		t.Fatalf("expected non-terminal status before slow job finishes, got %s", got.Status)
	}
}
