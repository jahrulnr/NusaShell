package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/application/internal/service/tooloutput"
	"nusashell/contracts"
	"nusashell/domain"
)

type waitAcpRuntime struct {
	AcpRuntime
	run     *domain.AcpRun
	waitErr error
}

func (f *waitAcpRuntime) Get(runID string) (*domain.AcpRun, bool) {
	return f.run, f.run != nil && f.run.ID == runID
}

func (f *waitAcpRuntime) Wait(context.Context, string) (*domain.AcpRun, error) {
	return f.run, f.waitErr
}

type waitAcpStorage struct {
	record domain.AcpRunRecord
	path   string
}

func (f *waitAcpStorage) Save(record domain.AcpRunRecord) error {
	f.record = record
	return nil
}

func (f *waitAcpStorage) Load(string) (domain.AcpRunRecord, bool) {
	return domain.AcpRunRecord{}, false
}

func (f *waitAcpStorage) List(string) []domain.AcpRunRecord {
	return nil
}

func (f *waitAcpStorage) Path(string, string) string {
	return f.path
}

func TestWaitAcpRunReturnsPersistedPathAndLastTurnOnly(t *testing.T) {
	run := &domain.AcpRun{
		ID:             "acprun_1",
		AgentID:        "acp_1",
		ConversationID: "conv_1",
		Workspace:      "/tmp/project",
		Prompt:         "private parent prompt that must not be repeated",
		Status:         domain.AcpRunCompleted,
		Transcript: []domain.AcpTranscriptChunk{
			{Kind: "thought", Text: "long private reasoning"},
			{Kind: "text", Text: "Intermediate progress."},
			{Kind: "tool", ToolID: "tool-1", ToolTitle: "edit_file", ToolStatus: "completed"},
			{Kind: "text", Text: "Finished the fix.\n\n---\nAll focused tests pass."},
		},
	}
	storage := &waitAcpStorage{path: "/data/conversations/conv_1.acp/acprun_1.json"}
	app := &App{
		Acp:           &waitAcpRuntime{run: run, waitErr: context.DeadlineExceeded},
		AcpRunStorage: storage,
	}

	output, err := app.WaitAcpRun(context.Background(), []byte(`{"id":"acprun_1","timeout_ms":100}`))
	if err != nil {
		t.Fatalf("WaitAcpRun: %v", err)
	}
	for _, want := range []string{
		"status: completed",
		"id: acprun_1",
		"output_path: /data/conversations/conv_1.acp/acprun_1.json",
		"Finished the fix.",
		"All focused tests pass.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
	for _, leaked := range []string{
		run.Prompt,
		"long private reasoning",
		"Intermediate progress.",
		"edit_file",
		"transcript:",
	} {
		if strings.Contains(output, leaked) {
			t.Errorf("compact wait output leaked %q:\n%s", leaked, output)
		}
	}
	if storage.record.ID != run.ID || len(storage.record.Transcript) != len(run.Transcript) {
		t.Fatalf("full run was not persisted: %+v", storage.record)
	}

	providerOutput := tooloutput.ProviderToolContent("subagent_wait", output)
	if !strings.Contains(providerOutput, "Finished the fix.") ||
		!strings.Contains(providerOutput, storage.path) {
		t.Fatalf("provider summary lost compact result:\n%s", providerOutput)
	}
	if strings.Contains(providerOutput, run.Prompt) || strings.Contains(providerOutput, "long private reasoning") {
		t.Fatalf("provider summary leaked full run:\n%s", providerOutput)
	}
}

func TestWaitAcpRunDoesNotPersistRunningTimeoutSnapshot(t *testing.T) {
	run := &domain.AcpRun{
		ID:             "acprun_live",
		ConversationID: "conv_1",
		Status:         domain.AcpRunRunning,
		Transcript:     []domain.AcpTranscriptChunk{{Kind: "text", Text: "Still working."}},
	}
	storage := &waitAcpStorage{path: "/data/conversations/conv_1.acp/acprun_live.json"}
	app := &App{
		Acp:           &waitAcpRuntime{run: run},
		AcpRunStorage: storage,
	}

	output, err := app.WaitAcpRun(context.Background(), []byte(`{"id":"acprun_live","timeout_ms":1}`))
	if err != nil {
		t.Fatalf("WaitAcpRun: %v", err)
	}
	if strings.Contains(output, "output_path:") {
		t.Fatalf("running snapshot must not advertise a persisted path:\n%s", output)
	}
	if storage.record.ID != "" {
		t.Fatalf("running snapshot must not be persisted: %+v", storage.record)
	}
}

func TestWaitAcpRunReturnsParentCancellation(t *testing.T) {
	run := &domain.AcpRun{
		ID:             "acprun_live",
		ConversationID: "conv_1",
		Status:         domain.AcpRunRunning,
	}
	app := &App{
		Acp: &waitAcpRuntime{run: run, waitErr: context.Canceled},
	}
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	_, rpcErr := app.waitAcpRun(parent, contracts.AcpRunWaitRequest{ID: run.ID, TimeoutMS: 100})
	if rpcErr == nil {
		t.Fatal("parent cancellation must not return a successful running snapshot")
	}
	if rpcErr.Code != contracts.CodeInternal || !strings.Contains(rpcErr.Message, context.Canceled.Error()) {
		t.Fatalf("parent cancellation error = %+v", rpcErr)
	}
}
