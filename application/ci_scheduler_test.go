package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
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

func testAutomation(t *testing.T, exec JobExecutor) (*CI, *CIStore, *FrozenClock) {
	t.Helper()
	mem := NewCIStore()
	clock := &FrozenClock{T: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)}
	caps := NewCapabilityRegistry()
	caps.Workflows = WorkflowMem{CIStore: mem}
	caps.State = ProviderStateMem{CIStore: mem}
	es := NewExecutionScheduler()
	es.Runs = RunMem{CIStore: mem}
	es.Logs = LogMem{CIStore: mem}
	es.Exec = exec
	es.Caps = caps
	es.Waits = WaitMem{CIStore: mem}
	es.Clock = clock
	es.Bus = NewBus()
	auto := &CIScheduler{
		Workflows: WorkflowMem{CIStore: mem},
		Schedules: ScheduleMem{CIStore: mem},
		Events:    EventMem{CIStore: mem},
		Waits:     WaitMem{CIStore: mem},
		Locks:     LockMem{CIStore: mem},
		Debounce:  DebounceMem{CIStore: mem},
		Caps:      caps,
		Exec:      es,
		Clock:     clock,
		Bus:       es.Bus,
	}
	return &CI{
		Workflows: WorkflowMem{CIStore: mem},
		Runs:      RunMem{CIStore: mem},
		Schedules: ScheduleMem{CIStore: mem},
		Events:    EventMem{CIStore: mem},
		Exec:      es,
		Sched:     auto,
		Caps:      caps,
		Logs:      LogMem{CIStore: mem},
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

func TestCapabilityBuiltinAvailable(t *testing.T) {
	reg := NewCapabilityRegistry()
	b, err := reg.Resolve(context.Background(), "filesystem.read", domain.DefaultAutoStart)
	if err != nil {
		t.Fatal(err)
	}
	if b.Kind != domain.CapabilityBuiltin || b.Status != domain.CapAvailable {
		t.Fatalf("%+v", b)
	}
	out, err := reg.Execute(context.Background(), b, json.RawMessage(`{"path":"/no/such"}`))
	if err == nil {
		t.Fatalf("expected read error, got %s", out)
	}
}

func TestDisabledProviderBlocksNotFails(t *testing.T) {
	mem := NewCIStore()
	reg := NewCapabilityRegistry()
	reg.State = ProviderStateMem{CIStore: mem}
	reg.Plugins = &capPluginStore{items: []*domain.Plugin{{
		Manifest: domain.PluginManifest{ID: "mail-mcp", Name: "mail"},
	}}}
	reg.MCP = &capMCP{tools: map[string][]contracts.MCPToolDTO{
		"plugin:mail-mcp": {{Name: "email_read"}},
	}}
	_ = reg.SetDisabled(context.Background(), "mail-mcp", true)
	b, err := reg.Resolve(context.Background(), "email.read", domain.DefaultAutoStart)
	if err != nil && b.Status != domain.CapDisabled {
		t.Fatal(err)
	}
	if b.Status != domain.CapDisabled {
		t.Fatalf("status = %s", b.Status)
	}
	if domain.MapAvailability(b.Status, true) != domain.AvailBlocked {
		t.Fatal("disabled maps to blocked, not failed")
	}
}

type capPluginStore struct{ items []*domain.Plugin }

func (s *capPluginStore) List() ([]*domain.Plugin, error) { return s.items, nil }
func (s *capPluginStore) Get(id string) (*domain.Plugin, error) {
	for _, p := range s.items {
		if p.Manifest.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (s *capPluginStore) Install(string) (*domain.Plugin, error) { return nil, fmt.Errorf("no") }
func (s *capPluginStore) Uninstall(string) error                 { return nil }
func (s *capPluginStore) Save(*domain.Plugin) error              { return nil }
func (s *capPluginStore) Delete(string) error                    { return nil }

type capMCP struct {
	tools map[string][]contracts.MCPToolDTO
}

func (m *capMCP) ToolsFor(id string) ([]contracts.MCPToolDTO, bool) {
	t, ok := m.tools[id]
	return t, ok
}
func (m *capMCP) Connect(_ context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error) {
	tools, _ := m.ToolsFor(p.Manifest.MCPServerID())
	return tools, nil
}
func (m *capMCP) Drop(string) {}

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
	svc.LocksAcquireForTest(w.Concurrency.Key, "run_existing")
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "e2", Type: "tick"}); err != nil {
		t.Fatal(err)
	}
	runs, _ := svc.Runs.List(context.Background(), RunFilter{WorkflowID: "mon"})
	if len(runs) != 0 {
		t.Fatalf("skip should not start a run, got %d", len(runs))
	}
}

func (a *CI) LocksAcquireForTest(key, runID string) {
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
