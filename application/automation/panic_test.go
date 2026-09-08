package automation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/domain"
)

type panicAgentStepRunner struct{}

func (panicAgentStepRunner) RunAgentStep(context.Context, string, string, domain.TrustLevel, map[string]any) (map[string]any, string, error) {
	panic("simulated Telegram adapter panic")
}

func TestRunJobRecoversAgentPanicAsFailedJob(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	svc.Exec.Agent = panicAgentStepRunner{}
	workflow := &domain.WorkflowDefinition{
		ID:       "telegram-reply",
		Name:     "Telegram reply",
		Trust:    domain.TrustSafe,
		Triggers: []domain.Trigger{{ID: "telegram-message", Kind: domain.TriggerEvent, Event: "telegram.message"}},
		Jobs: []domain.Job{{
			ID: "reply",
			Steps: []domain.Step{{
				ID:    "agent-reply",
				Agent: &domain.AgentStep{Prompt: "reply to the incoming Telegram message"},
			}},
		}},
	}
	if result := domain.ValidateSyntax(workflow); result.Verdict() != "VALID" {
		t.Fatalf("test workflow must be structurally valid: %+v", result.Issues)
	}

	run := NewWorkflowRun(*workflow, "event")
	if err := svc.Exec.Runs.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	var runErr error
	panicked := false
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicked = true
			}
		}()
		runErr = svc.Exec.runJob(context.Background(), run.ID, "reply")
	}()
	if panicked {
		t.Fatal("runJob allowed an agent panic to escape")
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "panic") {
		t.Fatalf("runJob error = %v, want recovered panic diagnostic", runErr)
	}

	got, err := svc.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobRun := got.JobRunByID("reply")
	if jobRun == nil {
		t.Fatal("reply job run missing")
	}
	if jobRun.Status != domain.StatusFailed {
		t.Fatalf("reply job status = %s, want failed", jobRun.Status)
	}
	if !strings.Contains(jobRun.Error, "panic") {
		t.Fatalf("reply job error = %q, want panic diagnostic", jobRun.Error)
	}
	if got.Status != domain.StatusRunning {
		t.Fatalf("run status = %s, want running until scheduler finalizes it", got.Status)
	}
}

type panicOnceRunStore struct {
	PipelineRunStore
	panicked chan struct{}
	once     sync.Once
}

func (s *panicOnceRunStore) Get(ctx context.Context, id string) (*domain.WorkflowRun, error) {
	panicNow := false
	s.once.Do(func() {
		panicNow = true
		close(s.panicked)
	})
	if panicNow {
		panic("simulated async scheduler panic")
	}
	return s.PipelineRunStore.Get(ctx, id)
}

func TestStartRunAsyncContainsTickPanic(t *testing.T) {
	fx := &fakeExec{}
	svc, _, _ := testAutomation(t, fx)
	base := svc.Exec.Runs
	store := &panicOnceRunStore{PipelineRunStore: base, panicked: make(chan struct{})}
	svc.Exec.Runs = store
	workflow := &domain.WorkflowDefinition{
		ID:   "async-panic",
		Name: "async-panic",
		Jobs: []domain.Job{{ID: "job", Steps: []domain.Step{{ID: "step", Run: "echo"}}}},
	}
	run := NewWorkflowRun(*workflow, "test")
	if err := svc.Exec.StartRunAsync(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.panicked:
	case <-time.After(time.Second):
		t.Fatal("background scheduler did not reach the panic boundary")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, err := store.Get(context.Background(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status.IsTerminal() {
			if got.Status != domain.StatusFailed {
				t.Fatalf("run status = %s, want failed after scheduler panic", got.Status)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run was not failed after background scheduler panic")
}

type panicRunNotifier struct {
	called chan struct{}
	once   sync.Once
}

func (n *panicRunNotifier) NotifyRunCompleted(context.Context, string, *domain.WorkflowRun) error {
	n.once.Do(func() { close(n.called) })
	panic("simulated webhook notifier panic")
}

type panicWebhookEmitter struct {
	done chan struct{}
	once sync.Once
}

func (e *panicWebhookEmitter) Emit(typ string, _ any) {
	if typ == "automation.webhook.failed" {
		e.once.Do(func() { close(e.done) })
	}
}

func TestNotifyWebhookRecoversNotifierPanic(t *testing.T) {
	notifier := &panicRunNotifier{called: make(chan struct{})}
	emitter := &panicWebhookEmitter{done: make(chan struct{})}
	svc := &ExecutionScheduler{Notifier: notifier, Bus: emitter}
	run := &domain.WorkflowRun{
		TaskState: domain.TaskState[domain.RunStatus]{ID: "run-1"},
		Definition: domain.WorkflowDefinition{
			WebhookURL: "https://example.invalid/automation",
		},
	}

	svc.notifyWebhook(context.Background(), run)
	select {
	case <-notifier.called:
	case <-time.After(time.Second):
		t.Fatal("webhook notifier was not called")
	}
	select {
	case <-emitter.done:
	case <-time.After(time.Second):
		t.Fatal("webhook notifier panic was not contained")
	}
}
