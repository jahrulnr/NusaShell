package automation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/domain"
)

// memConversationKeyStore is an in-memory ConversationKeyStore for tests.
type memConversationKeyStore struct {
	mu   sync.Mutex
	byID map[string]string // workflowID+"\x00"+key -> convID
}

func newMemConversationKeyStore() *memConversationKeyStore {
	return &memConversationKeyStore{byID: map[string]string{}}
}

func (m *memConversationKeyStore) GetConversation(ctx context.Context, workflowID, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.byID[workflowID+"\x00"+key]
	return v, ok, nil
}

func (m *memConversationKeyStore) SetConversation(ctx context.Context, workflowID, key, conversationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID[workflowID+"\x00"+key] = conversationID
	return nil
}

// recordingAgentRunner records the conversationID each agent step received
// and fabricates conversation ids so reuse can be observed.
type recordingAgentRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingAgentRunner) RunAgentStep(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, conversationID string) (map[string]any, string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, conversationID)
	created := "conv-fresh"
	if conversationID != "" {
		created = conversationID
	}
	r.mu.Unlock()
	return map[string]any{"output": "done"}, created, nil
}

func reuseWorkflow() *domain.WorkflowDefinition {
	return &domain.WorkflowDefinition{
		ID:          "reuse-wf",
		Name:        "Reuse wf",
		Concurrency: domain.Concurrency{Policy: domain.ConcurrencyQueue},
		Jobs:        []domain.Job{{ID: "reply", Steps: []domain.Step{{ID: "agent-reply", Agent: &domain.AgentStep{Prompt: "work", Reuse: true, Conversation: "shared"}}}}},
	}
}

func TestAgentStepReuseCarriesConversationAcrossRuns(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	runner := &recordingAgentRunner{}
	svc.Exec.Agent = runner
	svc.Exec.Convs = newMemConversationKeyStore()
	wf := reuseWorkflow()
	if result := domain.ValidateSyntax(wf); result.Verdict() != "VALID" {
		t.Fatalf("workflow must validate: %+v", result.Issues)
	}
	// First run: fresh conversation is created and recorded under the key.
	run1 := NewWorkflowRun(*wf, "event")
	if err := svc.Exec.Runs.Create(context.Background(), run1); err != nil {
		t.Fatal(err)
	}
	if err := svc.Exec.runJob(context.Background(), run1.ID, "reply"); err != nil {
		t.Fatalf("first runJob: %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "" {
		t.Fatalf("first run must start a fresh conversation, calls=%v", runner.calls)
	}
	// Second run: the executor must resume the conversation recorded by run 1.
	run2 := NewWorkflowRun(*wf, "event")
	if err := svc.Exec.Runs.Create(context.Background(), run2); err != nil {
		t.Fatal(err)
	}
	if err := svc.Exec.runJob(context.Background(), run2.ID, "reply"); err != nil {
		t.Fatalf("second runJob: %v", err)
	}
	if len(runner.calls) != 2 || runner.calls[1] != "conv-fresh" {
		t.Fatalf("second run must reuse conversation of first run, calls=%v", runner.calls)
	}
	if got, ok, _ := svc.Exec.Convs.GetConversation(context.Background(), "reuse-wf", "shared"); !ok || got != "conv-fresh" {
		t.Fatalf("conversation mapping = %q,%v, want conv-fresh", got, ok)
	}
}

func TestAgentStepFreshByDefault(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	runner := &recordingAgentRunner{}
	svc.Exec.Agent = runner
	svc.Exec.Convs = newMemConversationKeyStore()
	wf := &domain.WorkflowDefinition{
		ID:   "fresh-wf",
		Name: "Fresh wf",
		Jobs: []domain.Job{{ID: "reply", Steps: []domain.Step{{ID: "agent-reply", Agent: &domain.AgentStep{Prompt: "work"}}}}},
	}
	run1 := NewWorkflowRun(*wf, "event")
	if err := svc.Exec.Runs.Create(context.Background(), run1); err != nil {
		t.Fatal(err)
	}
	if err := svc.Exec.runJob(context.Background(), run1.ID, "reply"); err != nil {
		t.Fatalf("runJob: %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "" {
		t.Fatalf("non-reuse agent step must stay fresh, calls=%v", runner.calls)
	}
	if _, ok, _ := svc.Exec.Convs.GetConversation(context.Background(), "fresh-wf", "fresh-wf"); ok {
		t.Fatal("non-reuse agent step must not record a conversation mapping")
	}
}

// busyAgentRunner signals entry on started (once), then blocks until block closes.
type busyAgentRunner struct {
	started chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (b *busyAgentRunner) RunAgentStep(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, conversationID string) (map[string]any, string, error) {
	b.once.Do(func() { close(b.started) })
	_ = blockOrCtx(ctx, b.block)
	return map[string]any{"output": "done"}, "conv-busy", nil
}

func blockOrCtx(ctx context.Context, block <-chan struct{}) error {
	select {
	case <-block:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestReuseQueueDoesNotBusyFail(t *testing.T) {
	// reuse + queue: overlapping same-key events wait then resume; the
	// executor must not fail the second run with a conversation-busy error.
	svc, _, _ := testAutomation(t, &fakeExec{})
	started := make(chan struct{})
	block := make(chan struct{})
	svc.Exec.Agent = &busyAgentRunner{started: started, block: block}
	svc.Exec.Convs = newMemConversationKeyStore()
	wf := reuseWorkflow()
	wf.Enabled = true
	wf.Triggers = []domain.Trigger{{ID: "t1", Kind: domain.TriggerEvent, Event: "chat.message"}}
	wf.Concurrency = domain.Concurrency{Key: "tg-${event.chat_id}", Policy: domain.ConcurrencyQueue}
	wf.Jobs[0].Steps[0].Agent.Conversation = "tg-${event.chat_id}"
	if err := svc.Workflows.Put(context.Background(), wf); err != nil {
		t.Fatal(err)
	}
	ev1 := domain.Event{ID: "e1", Type: "chat.message", Attributes: map[string]any{"chat_id": "A"}}
	if err := svc.Sched.IngestEvent(context.Background(), ev1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not reach the agent step")
	}
	ev2 := domain.Event{ID: "e2", Type: "chat.message", Attributes: map[string]any{"chat_id": "A"}}
	if err := svc.Sched.IngestEvent(context.Background(), ev2); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var queued *domain.WorkflowRun
	for time.Now().Before(deadline) {
		runs, _ := svc.Runs.List(context.Background(), RunFilter{WorkflowID: wf.ID})
		for _, r := range runs {
			if r.EventID == "e2" {
				queued = r
				break
			}
		}
		if queued != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if queued == nil {
		t.Fatal("second event must create a queued run")
	}
	if queued.Status == domain.StatusFailed && strings.Contains(queued.Error, "busy") {
		t.Fatalf("reuse+queue must not busy-fail, got %+v", queued)
	}
	close(block)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.Runs.Get(context.Background(), queued.ID)
		if err == nil && got.Status.IsTerminal() {
			if got.Status == domain.StatusFailed && strings.Contains(got.Error, "busy") {
				t.Fatalf("queued reuse run failed busy: %+v", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("queued reuse run did not finish after first run released")
}
