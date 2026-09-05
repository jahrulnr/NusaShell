package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

type steerStopAcpRuntime struct {
	AcpRuntime
	run *domain.AcpRun
}

func (f *steerStopAcpRuntime) Get(runID string) (*domain.AcpRun, bool) {
	return f.run, f.run != nil && f.run.ID == runID
}

func (f *steerStopAcpRuntime) Steer(runID, text string) error {
	if f.run == nil || f.run.ID != runID {
		return errors.New("run not found")
	}
	f.run.QueuedSteer = text
	return nil
}

func (f *steerStopAcpRuntime) Stop(runID string) error {
	if f.run == nil || f.run.ID != runID {
		return errors.New("run not found")
	}
	f.run.Finish(domain.AcpRunCancelled, "", "cancelled", time.Unix(1, 0))
	return nil
}

func bloatedSubagentRun(id string, status domain.AcpRunStatus) *domain.AcpRun {
	chunks := []domain.AcpTranscriptChunk{
		{Kind: "thought", Text: "long private reasoning " + strings.Repeat("r", 2500)},
		{Kind: "text", Text: "Intermediate progress. " + strings.Repeat("p", 2500)},
		{Kind: "tool", ToolID: "tool-1", ToolTitle: "edit_file", ToolStatus: "completed"},
	}
	for i := 0; i < 4; i++ {
		chunks = append(chunks, domain.AcpTranscriptChunk{
			Kind: "text", Text: "Progress chunk that must not leak. " + strings.Repeat("x", 1800),
		})
	}
	chunks = append(chunks, domain.AcpTranscriptChunk{Kind: "text", Text: "Last meaningful turn only."})
	return &domain.AcpRun{
		TaskState:      domain.TaskState[domain.AcpRunStatus]{ID: id, Status: status},
		AgentID:        "acp_1",
		AgentName:      "Codex",
		ConversationID: "conv_1",
		Workspace:      "/tmp/project",
		Prompt:         "private parent prompt that must not be repeated " + strings.Repeat("q", 2000),
		Transcript:     chunks,
	}
}

func TestSteerAcpRunReturnsCompactAckWithoutTranscript(t *testing.T) {
	run := bloatedSubagentRun("acprun_steer", domain.AcpRunRunning)
	rt := &steerStopAcpRuntime{run: run}
	app := &App{Acp: rt}

	output, err := app.SteerAcpRun(context.Background(), []byte(`{"id":"acprun_steer","text":"focus on tests"}`))
	if err != nil {
		t.Fatalf("SteerAcpRun: %v", err)
	}
	if len(output) >= 5000 {
		t.Fatalf("steer tool output too large: %d", len(output))
	}
	for _, want := range []string{
		"status: running",
		"id: acprun_steer",
		"workspace: /tmp/project",
		"Steer accepted.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("steer output missing %q:\n%s", want, output)
		}
	}
	for _, leaked := range []string{
		"transcript:",
		"prompt:",
		run.Prompt,
		"long private reasoning",
		"Intermediate progress.",
		"Progress chunk that must not leak.",
		"Last meaningful turn only.",
		"edit_file",
		"availablemodes",
		"queuedsteer:",
	} {
		if strings.Contains(output, leaked) {
			t.Errorf("steer output leaked %q:\n%s", leaked, output)
		}
	}
	if run.QueuedSteer != "focus on tests" {
		t.Fatalf("steer was not applied, queued=%q", run.QueuedSteer)
	}
}

func TestStopAcpRunReturnsCompletionShapeWithoutTranscript(t *testing.T) {
	run := bloatedSubagentRun("acprun_stop", domain.AcpRunRunning)
	storage := &waitAcpStorage{path: "/data/conversations/conv_1.acp/acprun_stop.json"}
	app := &App{
		Acp:           &steerStopAcpRuntime{run: run},
		AcpRunStorage: storage,
	}

	output, err := app.StopAcpRun(context.Background(), []byte(`{"id":"acprun_stop"}`))
	if err != nil {
		t.Fatalf("StopAcpRun: %v", err)
	}
	if len(output) >= 5000 {
		t.Fatalf("stop tool output too large: %d", len(output))
	}
	for _, want := range []string{
		"status: cancelled",
		"id: acprun_stop",
		"output_path: /data/conversations/conv_1.acp/acprun_stop.json",
		"Last meaningful turn only.",
		"[Subagent was cancelled.]",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("stop output missing %q:\n%s", want, output)
		}
	}
	for _, leaked := range []string{
		"transcript:",
		"prompt:",
		run.Prompt,
		"long private reasoning",
		"Intermediate progress.",
		"Progress chunk that must not leak.",
		"edit_file",
	} {
		if strings.Contains(output, leaked) {
			t.Errorf("stop output leaked %q:\n%s", leaked, output)
		}
	}
	if storage.record.ID != run.ID || len(storage.record.Transcript) != len(run.Transcript) {
		t.Fatalf("full run was not persisted: %+v", storage.record)
	}
}

func TestHandleAcpRunsSteerStillReturnsFullDTOForUI(t *testing.T) {
	run := bloatedSubagentRun("acprun_rpc", domain.AcpRunRunning)
	app := &App{Acp: &steerStopAcpRuntime{run: run}}

	res, rpcErr := app.handleAcpRunsSteer(contracts.AcpRunSteerRequest{ID: "acprun_rpc", Text: "keep going"})
	if rpcErr != nil {
		t.Fatalf("handleAcpRunsSteer: %+v", rpcErr)
	}
	dto, ok := res.(contracts.AcpRunDTO)
	if !ok {
		t.Fatalf("RPC result type %T, want AcpRunDTO", res)
	}
	if len(dto.Transcript) != len(run.Transcript) {
		t.Fatalf("UI DTO transcript len=%d, want %d", len(dto.Transcript), len(run.Transcript))
	}
	if dto.Prompt != run.Prompt {
		t.Fatalf("UI DTO dropped prompt")
	}
}
