package automation

import (
	"context"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func waitRunTerminal(t *testing.T, svc *Automation, runID string, d time.Duration) *domain.WorkflowRun {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		got, err := svc.Runs.Get(context.Background(), runID)
		if err == nil && got.Status.IsTerminal() {
			return got
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach terminal within %s", runID, d)
	return nil
}

func findRunByEvent(t *testing.T, svc *Automation, workflowID, eventID string, d time.Duration) *domain.WorkflowRun {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		runs, _ := svc.Runs.List(context.Background(), RunFilter{WorkflowID: workflowID})
		for _, r := range runs {
			if r.EventID == eventID {
				return r
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("no run for event %s", eventID)
	return nil
}

func TestConcurrencyRenderedKeysDoNotBlockEachOther(t *testing.T) {
	fx := &fakeExec{Slow: 80 * time.Millisecond}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID: "chat-wf", Name: "chat-wf", Enabled: true,
		Concurrency: domain.Concurrency{Key: "tg-${event.chat_id}", Policy: domain.ConcurrencyQueue},
		Triggers:    []domain.Trigger{{ID: "t1", Kind: domain.TriggerEvent, Event: "chat.message"}},
		Jobs:        []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	if err := svc.Workflows.Put(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "ea", Type: "chat.message", Attributes: map[string]any{"chat_id": "tg-A"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "eb", Type: "chat.message", Attributes: map[string]any{"chat_id": "tg-B"}}); err != nil {
		t.Fatal(err)
	}
	ra := findRunByEvent(t, svc, w.ID, "ea", 2*time.Second)
	rb := findRunByEvent(t, svc, w.ID, "eb", 2*time.Second)
	waitRunTerminal(t, svc, ra.ID, 3*time.Second)
	waitRunTerminal(t, svc, rb.ID, 3*time.Second)
	// Distinct rendered keys must both complete (neither skipped as queued forever).
	gotA, _ := svc.Runs.Get(context.Background(), ra.ID)
	gotB, _ := svc.Runs.Get(context.Background(), rb.ID)
	if gotA.Status != domain.StatusSuccess || gotB.Status != domain.StatusSuccess {
		t.Fatalf("distinct keys must both succeed, A=%s B=%s", gotA.Status, gotB.Status)
	}
}

func TestConcurrencyQueueWaitsThenResumesFIFO(t *testing.T) {
	started := make(chan string, 3)
	block := make(chan struct{})
	fx := &blockingExec{started: started, block: block}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID: "q-wf", Name: "q-wf", Enabled: true,
		Concurrency: domain.Concurrency{Key: "res-${event.id}", Policy: domain.ConcurrencyQueue},
		Triggers:    []domain.Trigger{{ID: "t1", Kind: domain.TriggerEvent, Event: "tick"}},
		Jobs:        []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	// Use a static key so all events share one resource.
	w.Concurrency.Key = "shared-resource"
	if err := svc.Workflows.Put(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e2", "e3"} {
		if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: id, Type: "tick"}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first run never started")
	}
	r2 := findRunByEvent(t, svc, w.ID, "e2", 2*time.Second)
	r3 := findRunByEvent(t, svc, w.ID, "e3", 2*time.Second)
	if r2.Status.IsTerminal() || r3.Status.IsTerminal() {
		t.Fatalf("waiters must stay non-terminal while first holds lock, r2=%s r3=%s", r2.Status, r3.Status)
	}
	close(block)
	got2 := waitRunTerminal(t, svc, r2.ID, 5*time.Second)
	got3 := waitRunTerminal(t, svc, r3.ID, 5*time.Second)
	if got2.Status != domain.StatusSuccess || got3.Status != domain.StatusSuccess {
		t.Fatalf("FIFO waiters must succeed, r2=%s r3=%s", got2.Status, got3.Status)
	}
}

type blockingExec struct {
	started chan string
	block   chan struct{}
}

func (f *blockingExec) Prepare(_ context.Context, req PrepareRequest) (ExecutionWorkspace, error) {
	return ExecutionWorkspace{Dir: req.Workspace}, nil
}
func (f *blockingExec) Cleanup(_ context.Context, _ CleanupRequest) error { return nil }
func (f *blockingExec) RunStep(ctx context.Context, req RunStepRequest) (StepResult, error) {
	id := ""
	if req.Run != nil {
		id = req.Run.ID
	}
	select {
	case f.started <- id:
	default:
	}
	select {
	case <-f.block:
	case <-ctx.Done():
		return StepResult{Error: ctx.Err().Error()}, ctx.Err()
	}
	return StepResult{ExitCode: 0, Outputs: map[string]any{"status": "ok"}}, nil
}

func TestConcurrencyQueueOverflowDropsNew(t *testing.T) {
	started := make(chan string, 1)
	block := make(chan struct{})
	fx := &blockingExec{started: started, block: block}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID: "ov-wf", Name: "ov-wf", Enabled: true,
		Concurrency: domain.Concurrency{Key: "one", Policy: domain.ConcurrencyQueue},
		Triggers:    []domain.Trigger{{ID: "t1", Kind: domain.TriggerEvent, Event: "tick"}},
		Jobs:        []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	if err := svc.Workflows.Put(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "hold", Type: "tick"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("holder never started")
	}
	// Fill the queue to capacity.
	for i := 0; i < defaultConcurrencyQueueCap; i++ {
		id := "q" + string(rune('a'+i))
		if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: id, Type: "tick"}); err != nil {
			t.Fatal(err)
		}
	}
	overflowID := "overflow"
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: overflowID, Type: "tick"}); err != nil {
		t.Fatal(err)
	}
	skipped := findRunByEvent(t, svc, w.ID, overflowID, 2*time.Second)
	got := waitRunTerminal(t, svc, skipped.ID, 2*time.Second)
	if got.Status != domain.StatusSkipped || !strings.Contains(got.Error, queueOverflowReason) {
		t.Fatalf("overflow run = status %s error %q, want skipped/%s", got.Status, got.Error, queueOverflowReason)
	}
	close(block)
}

func TestConcurrencyQueueWaitTimeoutFinalizes(t *testing.T) {
	started := make(chan string, 1)
	block := make(chan struct{})
	fx := &blockingExec{started: started, block: block}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID: "to-wf", Name: "to-wf", Enabled: true,
		Concurrency: domain.Concurrency{Key: "one", Policy: domain.ConcurrencyQueue},
		Triggers:    []domain.Trigger{{ID: "t1", Kind: domain.TriggerEvent, Event: "tick"}},
		Jobs:        []domain.Job{{ID: "j", Timeout: 50 * time.Millisecond, Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	if err := svc.Workflows.Put(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "hold", Type: "tick"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("holder never started")
	}
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "waiter", Type: "tick"}); err != nil {
		t.Fatal(err)
	}
	waiter := findRunByEvent(t, svc, w.ID, "waiter", 2*time.Second)
	got := waitRunTerminal(t, svc, waiter.ID, 2*time.Second)
	if got.Status != domain.StatusSkipped || !strings.Contains(got.Error, queueWaitTimeoutReason) {
		t.Fatalf("timed-out waiter = status %s error %q, want skipped/%s", got.Status, got.Error, queueWaitTimeoutReason)
	}
	if got.Status == domain.StatusRunning {
		t.Fatal("timed-out waiter must not stay running")
	}
	close(block)
}

func TestConcurrencySkipPerRenderedKey(t *testing.T) {
	fx := &fakeExec{Slow: 100 * time.Millisecond}
	svc, _, _ := testAutomation(t, fx)
	w := &domain.WorkflowDefinition{
		ID: "skip-wf", Name: "skip-wf", Enabled: true,
		Concurrency: domain.Concurrency{Key: "tg-${event.chat_id}", Policy: domain.ConcurrencySkip},
		Triggers:    []domain.Trigger{{ID: "t1", Kind: domain.TriggerEvent, Event: "chat.message"}},
		Jobs:        []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "echo"}}}},
	}
	if err := svc.Workflows.Put(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "a1", Type: "chat.message", Attributes: map[string]any{"chat_id": "A"}}); err != nil {
		t.Fatal(err)
	}
	ra := findRunByEvent(t, svc, w.ID, "a1", 2*time.Second)
	// Same key while active: skipped (no new run).
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "a2", Type: "chat.message", Attributes: map[string]any{"chat_id": "A"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	runs, _ := svc.Runs.List(context.Background(), RunFilter{WorkflowID: w.ID})
	sameKey := 0
	for _, r := range runs {
		if r.EventID == "a2" {
			sameKey++
		}
	}
	if sameKey != 0 {
		t.Fatalf("skip must drop same-key event, got %d runs", sameKey)
	}
	// Different key must still start.
	if err := svc.Sched.IngestEvent(context.Background(), domain.Event{ID: "b1", Type: "chat.message", Attributes: map[string]any{"chat_id": "B"}}); err != nil {
		t.Fatal(err)
	}
	rb := findRunByEvent(t, svc, w.ID, "b1", 2*time.Second)
	waitRunTerminal(t, svc, ra.ID, 3*time.Second)
	waitRunTerminal(t, svc, rb.ID, 3*time.Second)
}
