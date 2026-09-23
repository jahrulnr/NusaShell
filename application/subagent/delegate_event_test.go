package subagent

import (
	"context"
	"strconv"
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

func TestRunStreamSnapshotsAndFramesAreCloned(t *testing.T) {
	streams := NewRunStreamRegistry()
	run := contracts.AcpRunDTO{
		ID: "run_1", ConversationID: "conv_1", Status: "running",
		AvailableModes: []contracts.AcpModeDTO{{ID: "default"}},
		Transcript:     []contracts.AcpTranscriptChunkDTO{{Kind: "text", Text: "stored"}},
		PendingPermission: &contracts.AcpPermissionDTO{
			ID: "permission_1", Paths: []string{"/workspace/file"},
			Options: []contracts.AcpPermissionOptionDTO{{ID: "allow", Name: "Allow", Kind: "allow_once"}},
		},
	}
	streams.Publish(contracts.EventAcpRunStarted, run)
	run.AvailableModes[0].ID = "caller mutation"
	run.Transcript[0].Text = "caller mutation"
	run.PendingPermission.Paths[0] = "caller mutation"
	run.PendingPermission.Options[0].Name = "caller mutation"

	sub := streams.Subscribe("conv_1")
	defer sub.Close()
	snapshot := sub.Snapshot()
	stored := snapshot.Runs[0]
	if stored.AvailableModes[0].ID != "default" || stored.Transcript[0].Text != "stored" || stored.PendingPermission.Paths[0] != "/workspace/file" || stored.PendingPermission.Options[0].Name != "Allow" {
		t.Fatalf("publish caller mutated registry snapshot: %+v", stored)
	}
	stored.Transcript[0].Text = "snapshot mutation"

	updated := sub.Snapshot().Runs[0]
	updated.Transcript[0].Text = "live update"
	streams.Publish(contracts.EventAcpRunUpdated, updated)
	frame := <-sub.Frames()
	frame.Run.Transcript[0].Text = "subscriber mutation"

	reconnected := streams.Subscribe("conv_1")
	defer reconnected.Close()
	if got := reconnected.Snapshot().Runs[0].Transcript[0].Text; got != "live update" {
		t.Fatalf("subscriber mutated registry frame: %q", got)
	}
}

func TestRunStreamSnapshotAndUpdatesAreConversationScoped(t *testing.T) {
	streams := NewRunStreamRegistry()
	started := contracts.AcpRunDTO{
		ID: "run_1", ConversationID: "conv_1", Status: "running",
		Transcript: []contracts.AcpTranscriptChunkDTO{{Kind: "text", Text: "first"}},
	}
	streams.Publish(contracts.EventAcpRunStarted, started)
	streams.Publish(contracts.EventAcpRunStarted, contracts.AcpRunDTO{ID: "run_other", ConversationID: "conv_other", Status: "running"})

	sub := streams.Subscribe("conv_1")
	defer sub.Close()
	snapshot := sub.Snapshot()
	if snapshot.Type != contracts.EventAcpRunSnapshot || snapshot.ConversationID != "conv_1" || snapshot.Seq != 1 || len(snapshot.Runs) != 1 || snapshot.Runs[0].ID != started.ID {
		t.Fatalf("initial snapshot = %+v", snapshot)
	}

	updated := started
	updated.Transcript = []contracts.AcpTranscriptChunkDTO{{Kind: "text", Text: "complete"}}
	streams.Publish(contracts.EventAcpRunUpdated, updated)
	select {
	case frame := <-sub.Frames():
		if frame.Seq != 2 || frame.Type != contracts.EventAcpRunUpdated || frame.Run.Transcript[0].Text != "complete" {
			t.Fatalf("live run update = %+v", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("live ACP run update was not delivered")
	}

	reconnected := streams.Subscribe("conv_1")
	defer reconnected.Close()
	resumed := reconnected.Snapshot()
	if resumed.Seq != 2 || len(resumed.Runs) != 1 || resumed.Runs[0].Transcript[0].Text != "complete" {
		t.Fatalf("reconnect snapshot = %+v", resumed)
	}
}

func TestInternalDelegateEmitsDoneEventToRunStream(t *testing.T) {
	recorder := &delegateEventRecorder{}
	streams := NewRunStreamRegistry()
	sub := streams.Subscribe("conv_parent")
	defer sub.Close()
	svc := New(Deps{
		Bus:          recorder,
		RunStreams:   streams,
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

	deadline := time.After(2 * time.Second)
	for {
		select {
		case frame := <-sub.Frames():
			if frame.Type != contracts.EventAcpRunDone {
				continue
			}
			if frame.Run.ID != runID || frame.Run.Status != string(domain.AcpRunCompleted) {
				t.Fatalf("done frame = %+v, want completed run %s", frame, runID)
			}
			for _, event := range recorder.snapshot() {
				if event.typ == contracts.EventAcpRunStarted || event.typ == contracts.EventAcpRunUpdated || event.typ == contracts.EventAcpRunDone {
					t.Fatalf("ACP run event %q was also sent through the WebSocket bus", event.typ)
				}
			}
			return
		case <-deadline:
			t.Fatal("run.done was not delivered through the run stream")
		}
	}
}

func TestInternalDelegateForwardsLiveTranscriptChunksToRunStream(t *testing.T) {
	streams := NewRunStreamRegistry()
	sub := streams.Subscribe("conv_parent")
	defer sub.Close()
	svc := New(Deps{
		RunStreams:   streams,
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
drain:
	for {
		select {
		case frame := <-sub.Frames():
			if frame.Type != contracts.EventAcpRunUpdated || frame.Run == nil || frame.Run.ID != runID || frame.Run.Activity != string(domain.AcpRunActivityThinking) {
				continue
			}
			for _, chunk := range frame.Run.Transcript {
				if chunk.Text == "thinking" || chunk.Text == "answer" {
					foundLiveUpdate = true
					break
				}
			}
		default:
			break drain
		}
	}
	if !foundLiveUpdate {
		t.Fatal("internal delegate emitted no live transcript update to its run stream")
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

func TestSlowRunStreamSubscriberReconnectsFromCurrentSnapshot(t *testing.T) {
	streams := NewRunStreamRegistry()
	sub := streams.Subscribe("conv_1")
	defer sub.Close()
	run := contracts.AcpRunDTO{ID: "run_1", ConversationID: "conv_1", Status: "running"}
	for i := 0; i <= runStreamSubscriberBuffer; i++ {
		run.Activity = strconv.Itoa(i)
		streams.Publish(contracts.EventAcpRunUpdated, run)
	}
	select {
	case <-sub.Done():
	case <-time.After(time.Second):
		t.Fatal("slow subscriber was not disconnected after its bounded queue filled")
	}

	reconnected := streams.Subscribe("conv_1")
	defer reconnected.Close()
	snapshot := reconnected.Snapshot()
	if snapshot.Seq != int64(runStreamSubscriberBuffer+1) || len(snapshot.Runs) != 1 || snapshot.Runs[0].Activity != strconv.Itoa(runStreamSubscriberBuffer) {
		t.Fatalf("reconnect snapshot = %+v", snapshot)
	}
}

func TestRunStreamIgnoresLateStartedSnapshotAfterNewerUpdate(t *testing.T) {
	streams := NewRunStreamRegistry()
	updatedAt := time.Now()
	updated := contracts.AcpRunDTO{
		ID: "run_1", ConversationID: "conv_1", Status: "running", Activity: "tool",
		Transcript: []contracts.AcpTranscriptChunkDTO{{Kind: "text", Text: "newer"}},
	}
	streams.publish(contracts.EventAcpRunUpdated, updated, updatedAt)
	started := updated
	started.Activity = "starting"
	started.Transcript = nil
	streams.publish(contracts.EventAcpRunStarted, started, updatedAt.Add(-time.Second))

	sub := streams.Subscribe("conv_1")
	defer sub.Close()
	snapshot := sub.Snapshot()
	if snapshot.Seq != 1 || len(snapshot.Runs) != 1 || snapshot.Runs[0].Activity != "tool" || snapshot.Runs[0].Transcript[0].Text != "newer" {
		t.Fatalf("late started snapshot regressed the run: %+v", snapshot)
	}
}

func TestRunStreamDoesNotRegressAfterDone(t *testing.T) {
	streams := NewRunStreamRegistry()
	doneAt := time.Now()
	done := contracts.AcpRunDTO{ID: "run_1", ConversationID: "conv_1", Status: "completed", Activity: "done"}
	streams.publish(contracts.EventAcpRunDone, done, doneAt)
	late := done
	late.Status = "running"
	late.Activity = "thinking"
	streams.publish(contracts.EventAcpRunUpdated, late, doneAt.Add(time.Second))

	sub := streams.Subscribe("conv_1")
	defer sub.Close()
	snapshot := sub.Snapshot()
	if snapshot.Seq != 1 || snapshot.Runs[0].Status != "completed" || snapshot.Runs[0].Activity != "done" {
		t.Fatalf("late update regressed a terminal run: %+v", snapshot)
	}
}

func TestRunStreamDeduplicatesSameSourceVersion(t *testing.T) {
	streams := NewRunStreamRegistry()
	updatedAt := time.Now()
	run := contracts.AcpRunDTO{ID: "run_1", ConversationID: "conv_1", Status: "running", Activity: "thinking"}
	streams.publish(contracts.EventAcpRunUpdated, run, updatedAt)
	streams.publish(contracts.EventAcpRunUpdated, run, updatedAt)

	sub := streams.Subscribe("conv_1")
	defer sub.Close()
	if snapshot := sub.Snapshot(); snapshot.Seq != 1 {
		t.Fatalf("duplicate source version advanced sequence to %d", snapshot.Seq)
	}
}

func TestRunStreamRetainsAndExpiresTerminalSnapshots(t *testing.T) {
	streams := NewRunStreamRegistry()
	streams.Publish(contracts.EventAcpRunDone, contracts.AcpRunDTO{ID: "run_1", ConversationID: "conv_1", Status: "completed"})

	streams.mu.Lock()
	stream := streams.conversations["conv_1"]
	entry := stream.runs["run_1"]
	if entry.expires.Sub(entry.updated) != runStreamRecentTTL {
		streams.mu.Unlock()
		t.Fatalf("terminal retention = %s, want %s", entry.expires.Sub(entry.updated), runStreamRecentTTL)
	}
	streams.pruneLocked(entry.expires.Add(-time.Nanosecond))
	_, retained := streams.conversations["conv_1"].runs["run_1"]
	streams.pruneLocked(entry.expires)
	_, expired := streams.conversations["conv_1"]
	streams.mu.Unlock()
	if !retained || expired {
		t.Fatalf("terminal snapshot retention before/at TTL = %v/%v", retained, expired)
	}
}

func TestRunStreamCapsSnapshotRegistry(t *testing.T) {
	streams := NewRunStreamRegistry()
	for i := 0; i <= runStreamMaxSnapshots; i++ {
		streams.Publish(contracts.EventAcpRunUpdated, contracts.AcpRunDTO{
			ID: "run_" + strconv.Itoa(i), ConversationID: "conv_1", Status: "running",
		})
	}
	streams.mu.Lock()
	count := 0
	for _, conversation := range streams.conversations {
		count += len(conversation.runs)
	}
	streams.mu.Unlock()
	if count != runStreamMaxSnapshots {
		t.Fatalf("retained run snapshots = %d, want cap %d", count, runStreamMaxSnapshots)
	}
}

func TestRunStreamCloseRemovesSubscriber(t *testing.T) {
	streams := NewRunStreamRegistry()
	sub := streams.Subscribe("conv_1")
	sub.Close()
	select {
	case <-sub.Done():
	default:
		t.Fatal("closed subscriber did not signal completion")
	}
	streams.mu.Lock()
	_, retained := streams.conversations["conv_1"]
	streams.mu.Unlock()
	if retained {
		t.Fatal("empty conversation stream retained a closed subscriber")
	}
}

func TestRunStreamSubscriberCountIsBounded(t *testing.T) {
	const maxSubscribers = 32
	streams := NewRunStreamRegistry()
	subs := make([]*RunStreamSub, 0, maxSubscribers+1)
	defer func() {
		for _, sub := range subs {
			if sub != nil {
				sub.Close()
			}
		}
	}()
	for i := 0; i < maxSubscribers; i++ {
		sub := streams.Subscribe("conv_" + strconv.Itoa(i))
		if sub == nil {
			t.Fatalf("subscriber %d was rejected below the limit", i)
		}
		subs = append(subs, sub)
	}
	if sub := streams.Subscribe("overflow"); sub != nil {
		subs = append(subs, sub)
		t.Fatal("subscriber was accepted above the global limit")
	}

	subs[0].Close()
	replacement := streams.Subscribe("replacement")
	if replacement == nil {
		t.Fatal("closed subscriber did not release its registry slot")
	}
	subs = append(subs, replacement)
}
