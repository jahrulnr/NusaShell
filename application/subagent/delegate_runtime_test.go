package subagent

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func waitForSettled(t *testing.T, svc *Service, runID string) *domain.AcpRun {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if run, ok := svc.DelegateRunSnapshot(runID); ok && !run.Live() {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatal("internal delegate run never settled")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// firstSpawnedRunID extracts the run id from a FormatSpawnResult payload.
func firstSpawnedRunID(t *testing.T, out string) string {
	t.Helper()
	match := regexp.MustCompile(`\brun_[A-Za-z0-9]+`).FindString(out)
	if match == "" {
		t.Fatalf("spawn result carries no run id: %q", out)
	}
	return match
}

func TestDispatchSubagentRoutesOps(t *testing.T) {
	release := make(chan struct{})
	steered := make(chan string, 2)
	svc := New(Deps{
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string)) (map[string]any, string, error) {
			onUpdate("conv_delegate")
			<-release
			return map[string]any{"output": "done"}, "conv_delegate", nil
		},
		SteerHeadless: func(conversationID, text string) error {
			steered <- conversationID + ":" + text
			return nil
		},
	})

	// Default op spawns.
	out, err := svc.DispatchSubagent(context.Background(), "conv_parent", "call_parent", []byte(`{"prompt":"do work"}`))
	if err != nil {
		t.Fatalf("default spawn: %v", err)
	}
	runID := firstSpawnedRunID(t, out)

	// Explicit spawn op.
	out, err = svc.DispatchSubagent(context.Background(), "conv_parent", "call_parent", []byte(`{"op":"spawn","prompt":"more work"}`))
	if err != nil {
		t.Fatalf("spawn op: %v", err)
	}
	secondID := firstSpawnedRunID(t, out)
	if secondID == runID {
		t.Fatal("spawn op must create a new run")
	}

	// Steer op routes to the headless turn.
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("headless turn never started")
	default:
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := svc.delegates.Steer(runID, "change"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run never became steerable")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := svc.DispatchSubagent(context.Background(), "conv_parent", "call_parent", []byte(`{"op":"steer","id":"`+runID+`","text":"change direction"}`)); err != nil {
		t.Fatalf("steer op: %v", err)
	}
	// The poll steer and the op steer must both reach the headless turn, in
	// order.
	for i, want := range []string{"conv_delegate:change", "conv_delegate:change direction"} {
		select {
		case got := <-steered:
			if got != want {
				t.Fatalf("steer %d = %q, want %q", i, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("steer %d never reached the headless turn", i)
		}
	}

	// Unknown op errors.
	if _, err := svc.DispatchSubagent(context.Background(), "conv_parent", "call_parent", []byte(`{"op":"explode"}`)); err == nil || !strings.Contains(err.Error(), "unknown subagent op") {
		t.Fatalf("unknown op error = %v", err)
	}

	close(release)
	waitForSettled(t, svc, runID)
	waitForSettled(t, svc, secondID)
}

func TestDispatchSubagentStopOpCancels(t *testing.T) {
	svc := New(Deps{
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string)) (map[string]any, string, error) {
			<-ctx.Done()
			return nil, "", ctx.Err()
		},
	})
	out, err := svc.DispatchSubagent(context.Background(), "conv_parent", "call_parent", []byte(`{"op":"spawn","prompt":"long work"}`))
	if err != nil {
		t.Fatal(err)
	}
	runID := firstSpawnedRunID(t, out)

	if _, err := svc.DispatchSubagent(context.Background(), "conv_parent", "call_parent", []byte(`{"op":"stop","id":"`+runID+`"}`)); err != nil {
		t.Fatalf("stop op: %v", err)
	}
	run := waitForSettled(t, svc, runID)
	if run.Status != domain.AcpRunCancelled {
		t.Fatalf("stopped delegate status = %q, want cancelled", run.Status)
	}
}

func TestSpawnSubagentsDefaultsToInternalWithoutACPAgents(t *testing.T) {
	svc := New(Deps{
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Go:           func(_ string, fn func()) {}, // headless turn never runs
	})
	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"prompt":"do work"}`))
	if err != nil {
		t.Fatalf("spawn without ACP agents: %v", err)
	}
	runs := svc.delegates.List("conv_parent")
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (internal default)", len(runs))
	}
	if runs[0].AgentID != internalDelegateAgentID || runs[0].AgentName != internalDelegateAgentName {
		t.Fatalf("agent = %s/%s, want internal delegate", runs[0].AgentID, runs[0].AgentName)
	}
	if !strings.Contains(out, "async: true") {
		t.Fatalf("spawn result must stay async: %s", out)
	}
}

func TestDelegateRunWaitReturnsHeadlessOutput(t *testing.T) {
	svc := New(Deps{
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string)) (map[string]any, string, error) {
			return map[string]any{"output": "delegate finished the work"}, "conv_delegate", nil
		},
	})
	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"prompt":"do work","agent_id":"internal"}`))
	if err != nil {
		t.Fatal(err)
	}
	runID := firstSpawnedRunID(t, out)

	run := waitForSettled(t, svc, runID)
	if run.Status != domain.AcpRunCompleted {
		t.Fatalf("status = %q, want completed", run.Status)
	}
	if !strings.Contains(run.Transcript[len(run.Transcript)-1].Text, "delegate finished") {
		t.Fatalf("transcript must carry the headless output: %+v", run.Transcript)
	}
}

func TestDelegateStopCancelsTheHeadlessTurn(t *testing.T) {
	svc := New(Deps{
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string)) (map[string]any, string, error) {
			<-ctx.Done()
			return nil, "", ctx.Err()
		},
	})
	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"prompt":"long work","agent_id":"internal"}`))
	if err != nil {
		t.Fatal(err)
	}
	runID := firstSpawnedRunID(t, out)

	if err := svc.delegates.Stop(runID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	run := waitForSettled(t, svc, runID)
	if run.Status != domain.AcpRunCancelled {
		t.Fatalf("stopped delegate status = %q, want cancelled", run.Status)
	}
}

func TestDelegateRunSteerQueuesOnTheHeadlessTurn(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	steered := make(chan string, 1)
	svc := New(Deps{
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string)) (map[string]any, string, error) {
			onUpdate("conv_delegate")
			close(started)
			<-release
			return map[string]any{"output": "steered work done"}, "conv_delegate", nil
		},
		SteerHeadless: func(conversationID, text string) error {
			steered <- conversationID + ":" + text
			return nil
		},
	})
	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"prompt":"start work","agent_id":"internal"}`))
	if err != nil {
		t.Fatal(err)
	}
	runID := firstSpawnedRunID(t, out)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("headless turn never started")
	}
	if err := svc.delegates.Steer(runID, "change direction"); err != nil {
		t.Fatalf("steer: %v", err)
	}
	select {
	case got := <-steered:
		if got != "conv_delegate:change direction" {
			t.Fatalf("steer target = %q, want conv_delegate:change direction", got)
		}
	case <-time.After(time.Second):
		t.Fatal("steer never reached the headless turn")
	}
	close(release)
	waitForSettled(t, svc, runID)
}
