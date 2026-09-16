package subagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

type delegateEventRecorder struct {
	mu     sync.Mutex
	events []struct {
		typ     string
		payload any
	}
}

func (r *delegateEventRecorder) Emit(typ string, payload any) {
	r.mu.Lock()
	r.events = append(r.events, struct {
		typ     string
		payload any
	}{typ: typ, payload: payload})
	r.mu.Unlock()
}

func (r *delegateEventRecorder) snapshot() []struct {
	typ     string
	payload any
} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]struct {
		typ     string
		payload any
	}(nil), r.events...)
}

func TestInternalDelegateEmitsDoneEvent(t *testing.T) {
	recorder := &delegateEventRecorder{}
	svc := New(Deps{
		Bus:          recorder,
		ResolveModel: func(string) (string, error) { return "provider:model", nil },
		Headless: func(context.Context, string, string, domain.TrustLevel, map[string]any, func(string), func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
			return map[string]any{"output": "done"}, "", nil
		},
	})

	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"agent_id":"internal","prompt":"work"}`))
	if err != nil {
		t.Fatalf("spawn internal delegate: %v", err)
	}
	runID := firstSpawnedRunID(t, out)
	run := waitForSettled(t, svc, runID)
	if run.Status != domain.AcpRunCompleted {
		t.Fatalf("run status = %q, want completed", run.Status)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		done := 0
		for _, event := range recorder.snapshot() {
			if event.typ != contracts.EventAcpRunDone {
				continue
			}
			done++
			payload, ok := event.payload.(contracts.AcpRunEvent)
			if !ok {
				t.Fatalf("done payload = %T, want contracts.AcpRunEvent", event.payload)
			}
			if payload.Run.ID != runID || payload.Run.Status != string(domain.AcpRunCompleted) {
				t.Fatalf("done payload = %+v, want completed run %s", payload.Run, runID)
			}
		}
		if done == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("done event count = %d, want 1; events = %+v", done, recorder.snapshot())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestInternalDelegateForwardsLiveTranscriptChunks(t *testing.T) {
	recorder := &delegateEventRecorder{}
	svc := New(Deps{
		Bus:          recorder,
		ResolveModel: func(string) (string, error) { return "provider:model", nil },
		Headless: func(_ context.Context, _ string, _ string, _ domain.TrustLevel, _ map[string]any, _ func(string), onTranscript func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
			onTranscript(domain.AcpTranscriptChunk{Kind: "thought", Text: "thinking"})
			onTranscript(domain.AcpTranscriptChunk{Kind: "text", Text: "answer"})
			return map[string]any{"output": "answer"}, "", nil
		},
	})

	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"agent_id":"internal","prompt":"work"}`))
	if err != nil {
		t.Fatalf("spawn internal delegate: %v", err)
	}
	runID := firstSpawnedRunID(t, out)
	waitForSettled(t, svc, runID)

	var foundLiveUpdate bool
	for _, event := range recorder.snapshot() {
		if event.typ != contracts.EventAcpRunUpdated {
			continue
		}
		payload, ok := event.payload.(contracts.AcpRunEvent)
		if !ok || payload.Run.ID != runID {
			continue
		}
		if payload.Run.Activity != string(domain.AcpRunActivityThinking) {
			continue
		}
		for _, chunk := range payload.Run.Transcript {
			if chunk.Text == "thinking" || chunk.Text == "answer" {
				foundLiveUpdate = true
				break
			}
		}
		if foundLiveUpdate {
			break
		}
	}
	if !foundLiveUpdate {
		t.Fatalf("internal delegate emitted no live transcript update; events = %+v", recorder.snapshot())
	}
}

func TestDelegateTranscriptKeepsSteeringPromptsWithoutDuplicatingInitialPrompt(t *testing.T) {
	conversation := &domain.Conversation{
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "initial work"},
			{Role: domain.RoleAssistant, Content: "first answer"},
			{Role: domain.RoleUser, Content: "focus on the failing test"},
			{Role: domain.RoleAssistant, Content: "updated answer"},
		},
	}

	transcript := delegateTranscriptFromConversation(conversation, "initial work")
	var prompts []string
	for _, chunk := range transcript {
		if chunk.Kind == "prompt" {
			prompts = append(prompts, chunk.Text)
		}
	}
	if len(prompts) != 1 || prompts[0] != "focus on the failing test" {
		t.Fatalf("steering prompts = %v, want only the later prompt", prompts)
	}
}
