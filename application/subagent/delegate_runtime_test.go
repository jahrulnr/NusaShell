package subagent

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"nusashell/contracts"
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
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string), _ func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
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
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string), _ func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
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
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string), _ func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
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

// TestDelegateAppendTranscriptCoalescesEmits covers the emitter-side
// batching: a burst of stream chunks must produce one coalesced run update
// (carrying the fully merged transcript), and finish must emit the terminal
// frame immediately with the complete state.
func TestDelegateAppendTranscriptCoalescesEmits(t *testing.T) {
	streams := NewRunStreamRegistry()
	svc := New(Deps{RunStreams: streams})
	rt := svc.delegates
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dr := &delegateRun{
		run: &domain.AcpRun{
			TaskState:      domain.TaskState[domain.AcpRunStatus]{ID: "run_burst", Status: domain.AcpRunRunning},
			ConversationID: "conv_parent",
			AgentID:        internalDelegateAgentID,
		},
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan *domain.AcpRun, 1),
	}
	rt.mu.Lock()
	rt.runs[dr.run.ID] = dr
	rt.mu.Unlock()

	sub := streams.Subscribe("conv_parent")
	defer sub.Close()
	for i := 0; i < 50; i++ {
		rt.appendTranscript(dr.run.ID, domain.AcpTranscriptChunk{Kind: "text", Text: "x"})
	}

	// Exactly one coalesced update may arrive from the burst, carrying the
	// merged transcript (all 50 chunks merged into one text chunk).
	select {
	case frame := <-sub.Frames():
		if frame.Type != contracts.EventAcpRunUpdated || frame.Run == nil {
			t.Fatalf("frame = %+v, want coalesced update", frame)
		}
		if len(frame.Run.Transcript) != 1 || frame.Run.Transcript[0].Text != strings.Repeat("x", 50) {
			t.Fatalf("coalesced update transcript = %+v, want one merged chunk of 50 deltas", frame.Run.Transcript)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("coalesced delegate update was never emitted")
	}
	select {
	case frame := <-sub.Frames():
		t.Fatalf("burst produced an extra frame: %+v", frame)
	case <-time.After(2 * runStreamCoalesceInterval):
	}

	// finish emits the terminal frame immediately — no coalescing delay —
	// and the snapshot carries the full transcript.
	rt.finish(dr.run.ID, dr, "", "final answer", nil)
	select {
	case frame := <-sub.Frames():
		if frame.Type != contracts.EventAcpRunDone || frame.Run == nil || frame.Run.Status != string(domain.AcpRunCompleted) {
			t.Fatalf("terminal frame = %+v, want done/completed", frame)
		}
		if len(frame.Run.Transcript) != 1 || frame.Run.Transcript[0].Text != strings.Repeat("x", 50) {
			t.Fatalf("done transcript = %+v, want the merged stream", frame.Run.Transcript)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("done frame was delayed behind the coalescing timer")
	}
}

// TestParallelDelegateRunsStreamWithoutConvoy proves parallel delegate runs
// streaming transcript bursts do not deadlock or starve each other: each
// run's coalesced emit timer fires independently and every run reaches a
// terminal frame. Run under -race to surface lock-ordering problems between
// DelegateRuntime.mu and the run stream registry.
func TestParallelDelegateRunsStreamWithoutConvoy(t *testing.T) {
	streams := NewRunStreamRegistry()
	sub := streams.Subscribe("conv_parent")
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			select {
			case <-sub.Frames():
			case <-sub.Done():
				return
			}
		}
	}()
	defer func() { sub.Close(); <-drainDone }()

	release := make(chan struct{})
	svc := New(Deps{
		RunStreams:   streams,
		ResolveModel: func(string) (string, error) { return "provider:model", nil },
		Headless: func(_ context.Context, _ string, _ string, _ domain.TrustLevel, _ map[string]any, _ func(string), onTranscript func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
			for i := 0; i < 20; i++ {
				onTranscript(domain.AcpTranscriptChunk{Kind: "text", Text: "x"})
			}
			<-release
			return map[string]any{"output": "done"}, "", nil
		},
	})
	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"agent_id":"internal","prompt":"work","count":4}`))
	if err != nil {
		t.Fatalf("spawn parallel delegates: %v", err)
	}
	ids := regexp.MustCompile(`\brun_[A-Za-z0-9]+`).FindAllString(out, -1)
	if len(ids) != 4 {
		t.Fatalf("spawn result ids = %v, want 4", ids)
	}
	// Hold the turns open past the coalescing interval so each run's emit
	// timer fires while the others are still streaming.
	time.Sleep(2 * runStreamCoalesceInterval)
	close(release)
	for _, id := range ids {
		run := waitForSettled(t, svc, id)
		if run.Status != domain.AcpRunCompleted {
			t.Fatalf("parallel delegate %s status = %q, want completed", id, run.Status)
		}
	}
	resub := streams.Subscribe("conv_parent")
	defer resub.Close()
	snapshot := resub.Snapshot()
	if len(snapshot.Runs) != 4 {
		t.Fatalf("snapshot runs = %d, want all 4 terminal runs retained", len(snapshot.Runs))
	}
	for _, run := range snapshot.Runs {
		if run.Status != string(domain.AcpRunCompleted) {
			t.Fatalf("snapshot run %s status = %q, want completed", run.ID, run.Status)
		}
	}
}

func TestDelegateStopCancelsTheHeadlessTurn(t *testing.T) {
	svc := New(Deps{
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string), _ func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
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
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string), _ func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
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
