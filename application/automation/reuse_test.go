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
		ID:   "reuse-wf",
		Name: "Reuse wf",
		Jobs: []domain.Job{{ID: "reply", Steps: []domain.Step{{ID: "agent-reply", Agent: &domain.AgentStep{Prompt: "work", Reuse: true, Conversation: "shared"}}}}},
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

// busyAgentRunner signals entry on started, then blocks until block closes.
type busyAgentRunner struct {
	started chan struct{}
	block   chan struct{}
}

func (b busyAgentRunner) RunAgentStep(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, conversationID string) (map[string]any, string, error) {
	close(b.started)
	<-b.block
	return map[string]any{"output": "done"}, "conv-busy", nil
}

func TestReuseBusyConversationFailsSecondRun(t *testing.T) {
	svc, _, _ := testAutomation(t, &fakeExec{})
	started := make(chan struct{})
	block := make(chan struct{})
	svc.Exec.Agent = busyAgentRunner{started: started, block: block}
	svc.Exec.Convs = newMemConversationKeyStore()
	wf := reuseWorkflow()
	run1 := NewWorkflowRun(*wf, "event")
	if err := svc.Exec.Runs.Create(context.Background(), run1); err != nil {
		t.Fatal(err)
	}
	done1 := make(chan error, 1)
	go func() { done1 <- svc.Exec.runJob(context.Background(), run1.ID, "reply") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not reach the agent step")
	}
	run2 := NewWorkflowRun(*wf, "event")
	if err := svc.Exec.Runs.Create(context.Background(), run2); err != nil {
		t.Fatal(err)
	}
	if err := svc.Exec.runJob(context.Background(), run2.ID, "reply"); err != nil {
		t.Fatalf("runJob2 returned %v (want persisted busy failure)", err)
	}
	got2, err := svc.Exec.Runs.Get(context.Background(), run2.ID)
	if err != nil {
		t.Fatal(err)
	}
	job2 := got2.JobRunByID("reply")
	if job2 == nil || job2.Status != domain.StatusFailed || !strings.Contains(job2.Error, "busy") {
		t.Fatalf("overlapping reuse run must fail busy, job=%+v", job2)
	}
	close(block)
	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not finish after unblock")
	}
}
