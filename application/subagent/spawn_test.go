package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

const testBrief = "## Objective\nBuild the feature\n\n## Done when\nTests pass\n\n## Findings\nsrc/x.go:10"

func TestWithParentPlanFileInsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	planDir := filepath.Join(ws, ".nusashell", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(planDir, "conv_1.plan.md")
	if err := os.WriteFile(planPath, []byte(testBrief), 0o600); err != nil {
		t.Fatal(err)
	}

	got := withParentPlan("do the slice", planPath, testBrief, ws)
	if !strings.Contains(got, "do the slice") {
		t.Errorf("original prompt lost:\n%s", got)
	}
	if !strings.Contains(got, "Parent plan file (read this first): "+planPath) {
		t.Errorf("plan path not injected:\n%s", got)
	}
	if strings.Contains(got, "Parent plan summary") {
		t.Errorf("in-workspace plan must not add a fallback summary:\n%s", got)
	}
}

func TestWithParentPlanFileOutsideWorkspaceAddsSummary(t *testing.T) {
	planDir := t.TempDir()
	ws := t.TempDir()
	planPath := filepath.Join(planDir, "conv_1.plan.md")
	if err := os.WriteFile(planPath, []byte(testBrief), 0o600); err != nil {
		t.Fatal(err)
	}

	got := withParentPlan("do the slice", planPath, testBrief, ws)
	if !strings.Contains(got, "Parent plan file (read this first): "+planPath) {
		t.Errorf("plan path not injected:\n%s", got)
	}
	if !strings.Contains(got, "Parent plan summary") {
		t.Errorf("out-of-workspace plan must add a fallback summary:\n%s", got)
	}
	if !strings.Contains(got, "## Objective\nBuild the feature") {
		t.Errorf("summary missing Objective:\n%s", got)
	}
	if strings.Contains(got, "Findings") {
		t.Errorf("summary must stay compact (no Findings):\n%s", got)
	}
}

func TestWithParentPlanMissingFileFallsBackToSummary(t *testing.T) {
	got := withParentPlan("do the slice", "/nonexistent/conv_1.plan.md", testBrief, t.TempDir())
	if strings.Contains(got, "Parent plan file") {
		t.Errorf("missing file must not be advertised:\n%s", got)
	}
	if !strings.Contains(got, "Parent plan summary:\n## Objective\nBuild the feature") {
		t.Errorf("missing file must inline the summary:\n%s", got)
	}
}

func TestWithParentPlanNoPathUsesSummary(t *testing.T) {
	got := withParentPlan("do the slice", "", testBrief, "")
	if !strings.Contains(got, "Parent plan summary") {
		t.Errorf("empty plan path must inline the summary:\n%s", got)
	}
}

// fakeBriefs is a minimal TodoBriefs for internal-spawn handoff tests.
type fakeBriefs struct {
	brief string
	path  string
}

func (f fakeBriefs) GetBrief(string) string { return f.brief }
func (f fakeBriefs) PlanPath(string) string { return f.path }

// TestWithInternalParentPlanInlinesCompactSummary proves the internal
// delegate handoff inlines the compact Objective + Done when summary from
// the parent brief without advertising a plan file path the child cannot
// read (the plan file lives outside the child workspace).
func TestWithInternalParentPlanInlinesCompactSummary(t *testing.T) {
	got := withInternalParentPlan("do the slice", testBrief)
	if !strings.Contains(got, "do the slice") {
		t.Errorf("original prompt lost:\n%s", got)
	}
	if !strings.Contains(got, "## Objective\nBuild the feature") {
		t.Errorf("handoff missing compact Objective:\n%s", got)
	}
	if !strings.Contains(got, "## Done when\nTests pass") {
		t.Errorf("handoff missing compact Done when:\n%s", got)
	}
	if strings.Contains(got, "Findings") {
		t.Errorf("handoff must stay compact (no Findings):\n%s", got)
	}
	if strings.Contains(got, "Parent plan file (read this first)") {
		t.Errorf("internal handoff must not advertise an unreadable plan path:\n%s", got)
	}
}

// TestWithInternalParentPlanOmitsHandoffWhenNoBrief proves the helper adds
// nothing when the parent has no brief, so the explicit parent prompt stays
// authoritative and uncluttered.
func TestWithInternalParentPlanOmitsHandoffWhenNoBrief(t *testing.T) {
	got := withInternalParentPlan("do the slice", "")
	if got != "do the slice" {
		t.Errorf("empty brief must return the prompt unchanged, got:\n%s", got)
	}
}

// TestSpawnInternalAppendsParentPlanHandoff proves the internal spawn path
// forwards the compact parent plan summary to the headless delegate turn
// when the parent conversation has a brief, and never advertises a plan
// file path the child workspace cannot read.
func TestSpawnInternalAppendsParentPlanHandoff(t *testing.T) {
	var capturedPrompt string
	svc := New(Deps{
		Todos:        fakeBriefs{brief: testBrief, path: "/outside/child/conv_parent.plan.md"},
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string), _ func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
			capturedPrompt = prompt
			return map[string]any{"output": "done"}, "conv_delegate", nil
		},
	})
	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"prompt":"do the slice","agent_id":"internal"}`))
	if err != nil {
		t.Fatalf("spawn internal: %v", err)
	}
	waitForSettled(t, svc, firstSpawnedRunID(t, out))
	if !strings.Contains(capturedPrompt, "do the slice") {
		t.Errorf("delegate prompt lost the parent prompt:\n%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "## Objective\nBuild the feature") {
		t.Errorf("delegate prompt missing compact Objective handoff:\n%s", capturedPrompt)
	}
	if strings.Contains(capturedPrompt, "Parent plan file (read this first)") {
		t.Errorf("internal delegate must not be pointed at an unreadable plan path:\n%s", capturedPrompt)
	}
}

// TestSpawnInternalOmitsHandoffWhenParentHasNoBrief proves no handoff is
// appended when the parent conversation has no brief.
func TestSpawnInternalOmitsHandoffWhenParentHasNoBrief(t *testing.T) {
	var capturedPrompt string
	svc := New(Deps{
		Todos:        fakeBriefs{},
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string), _ func(domain.AcpTranscriptChunk)) (map[string]any, string, error) {
			capturedPrompt = prompt
			return map[string]any{"output": "done"}, "conv_delegate", nil
		},
	})
	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"prompt":"do the slice","agent_id":"internal"}`))
	if err != nil {
		t.Fatalf("spawn internal: %v", err)
	}
	waitForSettled(t, svc, firstSpawnedRunID(t, out))
	if capturedPrompt != "do the slice" {
		t.Errorf("no brief must leave the prompt unchanged, got:\n%s", capturedPrompt)
	}
}

// failSpawnRuntime fails every Spawn before a run exists, like an ACP
// session/setup rejection ("acp Invalid params").
type failSpawnRuntime struct {
	Runtime
	err  error
	last SpawnRequest
}

func (r *failSpawnRuntime) Spawn(_ context.Context, req SpawnRequest) (*domain.AcpRun, error) {
	r.last = req
	return nil, r.err
}

func (r *failSpawnRuntime) List(string) []*domain.AcpRun      { return nil }
func (r *failSpawnRuntime) Get(string) (*domain.AcpRun, bool) { return nil, false }

type fakeRunStorage struct {
	saved []domain.AcpRunRecord
}

func (s *fakeRunStorage) Save(rec domain.AcpRunRecord) error {
	s.saved = append(s.saved, rec)
	return nil
}

func (s *fakeRunStorage) Load(id string) (domain.AcpRunRecord, bool) {
	for _, rec := range s.saved {
		if rec.ID == id {
			return rec, true
		}
	}
	return domain.AcpRunRecord{}, false
}

func (s *fakeRunStorage) List(conversationID string) []domain.AcpRunRecord {
	var out []domain.AcpRunRecord
	for _, rec := range s.saved {
		if conversationID == "" || rec.ConversationID == conversationID {
			out = append(out, rec)
		}
	}
	return out
}

func (s *fakeRunStorage) Path(conversationID, runID string) string {
	return filepath.Join("/data", conversationID, "acp", runID+".json")
}

type fakeConversationLookup struct{}

func (fakeConversationLookup) Get(id string) (*domain.Conversation, error) {
	return &domain.Conversation{ID: id}, nil
}

// TestSpawnSubagentsFailedSpawnLeavesRunRecord proves a spawn rejected
// before the ACP session exists still produces a terminal run record:
// the spawn result carries the run id, the run is persisted and listed,
// the run stream publishes its done snapshot, and the shared completion
// path marks the parent tool call failed — the drawer must not silently
// drop failed spawns.
func TestSpawnSubagentsFailedSpawnLeavesRunRecord(t *testing.T) {
	rt := &failSpawnRuntime{err: fmt.Errorf("acp Invalid params")}
	agent := &domain.AcpAgent{ID: "acp_1", Name: "Cursor", Enabled: true}
	storage := &fakeRunStorage{}
	streams := NewRunStreamRegistry()

	var tracked []string
	var completed struct {
		toolCallID string
		status     domain.ToolCallStatus
		run        *domain.AcpRun
	}
	svc := New(Deps{
		Runtime:       rt,
		Agents:        titleAgentStore{agent: agent},
		RunStorage:    storage,
		Conversations: fakeConversationLookup{},
		RunStreams:    streams,
		TrackPending: func(conversationID, runID, tool string) {
			tracked = append(tracked, runID)
		},
		DeliverRunDone: func(conversationID, runID string, complete func(string) error) {
			if err := complete(conversationID); err != nil {
				t.Errorf("complete: %v", err)
			}
		},
		CompleteSubagent: func(conversationID, toolCallID string, status domain.ToolCallStatus, run *domain.AcpRun, outputPath string) error {
			completed.toolCallID = toolCallID
			completed.status = status
			completed.run = run
			return nil
		},
	})

	out, err := svc.SpawnSubagents(context.Background(), "conv_1", "call_1", []byte(`{"prompt":"probe","title":"probe-cursor","agent_id":"acp_1"}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if !strings.Contains(out, "status: failed") {
		t.Fatalf("spawn result must report failed status:\n%s", out)
	}
	if !strings.Contains(out, "acp Invalid params") {
		t.Fatalf("spawn result must carry the spawn error:\n%s", out)
	}
	runID := ""
	if match := regexp.MustCompile(`\bacprun_[A-Za-z0-9]+`).FindString(out); match != "" {
		runID = match
	}
	if runID == "" {
		t.Fatalf("failed spawn must still carry a run id the UI can open:\n%s", out)
	}

	if len(tracked) != 1 || tracked[0] != runID {
		t.Fatalf("failed run must be tracked pending: %v", tracked)
	}
	if completed.toolCallID != "call_1" {
		t.Fatalf("completion must target the parent tool call, got %q", completed.toolCallID)
	}
	if completed.status != domain.ToolFailed {
		t.Fatalf("completion status = %q, want fail", completed.status)
	}
	if completed.run == nil || completed.run.ID != runID || completed.run.Status != domain.AcpRunFailed {
		t.Fatalf("completion run = %+v", completed.run)
	}

	if len(storage.saved) != 1 {
		t.Fatalf("persisted records = %d, want 1", len(storage.saved))
	}
	rec := storage.saved[0]
	if rec.ID != runID || rec.Status != domain.AcpRunFailed || rec.ConversationID != "conv_1" ||
		rec.ParentToolCallID != "call_1" || rec.Title != "probe-cursor" || rec.Error == "" {
		t.Fatalf("persisted record = %+v", rec)
	}

	list, rpcErr := svc.HandleRunsList(contracts.AcpRunsListRequest{ConversationID: "conv_1"})
	if rpcErr != nil {
		t.Fatalf("runs.list: %v", rpcErr)
	}
	runs := list.(contracts.AcpRunsListResult).Runs
	if len(runs) != 1 || runs[0].ID != runID || runs[0].Status != string(domain.AcpRunFailed) {
		t.Fatalf("runs.list = %+v", runs)
	}

	sub := streams.Subscribe("conv_1")
	defer sub.Close()
	snapRuns := sub.Snapshot().Runs
	if len(snapRuns) != 1 || snapRuns[0].ID != runID || snapRuns[0].Status != string(domain.AcpRunFailed) {
		t.Fatalf("run stream snapshot = %+v", snapRuns)
	}
}

// finishedSpawnRuntime returns an already-terminal run, like an ACP spawn
// whose drivePrompt finished (and fired OnDone) before Spawn returned its
// snapshot. Its lifecycle is owned by the runtime — the service must not
// drive completion or pending tracking a second time.
type finishedSpawnRuntime struct {
	Runtime
	run *domain.AcpRun
}

func (r *finishedSpawnRuntime) Spawn(context.Context, SpawnRequest) (*domain.AcpRun, error) {
	return r.run, nil
}

func (r *finishedSpawnRuntime) List(string) []*domain.AcpRun      { return nil }
func (r *finishedSpawnRuntime) Get(string) (*domain.AcpRun, bool) { return nil, false }

// TestSpawnSubagentsTerminalRunNotRecompleted locks the ownership boundary:
// a runtime-returned terminal run is already completing through the
// runtime's own done callback, so SpawnSubagents must neither deliver the
// result again nor register a pending entry that nothing can clear.
func TestSpawnSubagentsTerminalRunNotRecompleted(t *testing.T) {
	finished := &domain.AcpRun{
		TaskState:      domain.TaskState[domain.AcpRunStatus]{ID: "acprun_done1", Status: domain.AcpRunFailed},
		ConversationID: "conv_1",
		Title:          "probe",
	}
	rt := &finishedSpawnRuntime{run: finished}
	agent := &domain.AcpAgent{ID: "acp_1", Name: "Cursor", Enabled: true}
	storage := &fakeRunStorage{}
	streams := NewRunStreamRegistry()

	var tracked, completed int
	svc := New(Deps{
		Runtime:       rt,
		Agents:        titleAgentStore{agent: agent},
		RunStorage:    storage,
		Conversations: fakeConversationLookup{},
		RunStreams:    streams,
		TrackPending: func(conversationID, runID, tool string) {
			tracked++
		},
		DeliverRunDone: func(conversationID, runID string, complete func(string) error) {
			complete(conversationID)
		},
		CompleteSubagent: func(conversationID, toolCallID string, status domain.ToolCallStatus, run *domain.AcpRun, outputPath string) error {
			completed++
			return nil
		},
	})

	out, err := svc.SpawnSubagents(context.Background(), "conv_1", "call_1", []byte(`{"prompt":"probe","agent_id":"acp_1"}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if !strings.Contains(out, "acprun_done1") {
		t.Fatalf("result must still carry the terminal run id:\n%s", out)
	}
	if tracked != 0 {
		t.Fatalf("runtime-owned terminal run must not be tracked pending: %d", tracked)
	}
	if completed != 0 {
		t.Fatalf("runtime-owned terminal run must not be completed again: %d", completed)
	}
	if len(storage.saved) != 0 {
		t.Fatalf("runtime-owned terminal run must not be persisted again: %d", len(storage.saved))
	}
	sub := streams.Subscribe("conv_1")
	defer sub.Close()
	if got := sub.Snapshot().Runs; len(got) != 0 {
		t.Fatalf("runtime-owned terminal run must not be re-emitted: %+v", got)
	}
}
