package application

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

// fakeAcpRuntime satisfies AcpRuntime via embedding; only List is used by
// handleAcpRunsList.
type fakeAcpRuntime struct {
	AcpRuntime
	runs []*domain.AcpRun
}

func (f *fakeAcpRuntime) List(conversationID string) []*domain.AcpRun {
	if conversationID == "" {
		return f.runs
	}
	var out []*domain.AcpRun
	for _, r := range f.runs {
		if r.ConversationID == conversationID {
			out = append(out, r)
		}
	}
	return out
}

// fakeAcpRunStorage implements domain.AcpRunStorage for list-merge tests.
type fakeAcpRunStorage struct {
	recs []domain.AcpRunRecord
}

func (f *fakeAcpRunStorage) Save(record domain.AcpRunRecord) error {
	f.recs = append(f.recs, record)
	return nil
}
func (f *fakeAcpRunStorage) Load(runID string) (domain.AcpRunRecord, bool) {
	for _, r := range f.recs {
		if r.ID == runID {
			return r, true
		}
	}
	return domain.AcpRunRecord{}, false
}
func (f *fakeAcpRunStorage) List(conversationID string) []domain.AcpRunRecord {
	var out []domain.AcpRunRecord
	for _, r := range f.recs {
		if conversationID == "" || r.ConversationID == conversationID {
			out = append(out, r)
		}
	}
	return out
}
func (f *fakeAcpRunStorage) Path(conversationID, runID string) string { return "" }

func TestHandleAcpRunsListMergesSettledStorageRecords(t *testing.T) {
	app := &App{
		Bus: NewBus(),
		Acp: &fakeAcpRuntime{runs: []*domain.AcpRun{
			{ID: "acprun_live", ConversationID: "conv_1", Status: domain.AcpRunRunning, AgentName: "Live Agent"},
		}},
		AcpRunStorage: &fakeAcpRunStorage{recs: []domain.AcpRunRecord{
			{
				ID: "acprun_settled", ConversationID: "conv_1", Status: domain.AcpRunCompleted,
				AgentName: "Settled Agent", Workspace: "/tmp/ws",
				Transcript: []domain.AcpTranscriptChunk{{Kind: "text", Text: "all done"}},
			},
		}},
	}

	resp, rpcErr := app.handleAcpRunsList(contracts.AcpRunsListRequest{ConversationID: "conv_1"})
	if rpcErr != nil {
		t.Fatalf("runs.list: %v", rpcErr)
	}
	out, ok := resp.(contracts.AcpRunsListResult)
	if !ok || len(out.Runs) != 2 {
		t.Fatalf("expected 2 merged runs, got %+v", resp)
	}
	if out.Runs[0].ID != "acprun_live" || out.Runs[1].ID != "acprun_settled" {
		t.Fatalf("live runs must come first: %+v", out.Runs)
	}
	settled := out.Runs[1]
	if settled.Status != string(domain.AcpRunCompleted) {
		t.Fatalf("settled status = %q", settled.Status)
	}
	if len(settled.Transcript) != 1 || settled.Transcript[0].Text != "all done" {
		t.Fatalf("settled transcript not carried into DTO: %+v", settled.Transcript)
	}
}

func TestHandleAcpRunsListDedupesLiveAndStorage(t *testing.T) {
	app := &App{
		Bus: NewBus(),
		Acp: &fakeAcpRuntime{runs: []*domain.AcpRun{
			{ID: "acprun_shared", ConversationID: "conv_1", Status: domain.AcpRunRunning},
		}},
		AcpRunStorage: &fakeAcpRunStorage{recs: []domain.AcpRunRecord{
			{ID: "acprun_shared", ConversationID: "conv_1", Status: domain.AcpRunCompleted},
		}},
	}

	resp, rpcErr := app.handleAcpRunsList(contracts.AcpRunsListRequest{ConversationID: "conv_1"})
	if rpcErr != nil {
		t.Fatalf("runs.list: %v", rpcErr)
	}
	out := resp.(contracts.AcpRunsListResult)
	if len(out.Runs) != 1 {
		t.Fatalf("same run id in live + storage must appear once, got %+v", out.Runs)
	}
	if out.Runs[0].Status != string(domain.AcpRunRunning) {
		t.Fatalf("live status must win over stored copy, got %q", out.Runs[0].Status)
	}
}

func TestHandleAcpRunsListScopesToConversation(t *testing.T) {
	app := &App{
		Bus: NewBus(),
		Acp: &fakeAcpRuntime{runs: []*domain.AcpRun{
			{ID: "acprun_a", ConversationID: "conv_a", Status: domain.AcpRunRunning},
			{ID: "acprun_b", ConversationID: "conv_b", Status: domain.AcpRunRunning},
		}},
		AcpRunStorage: &fakeAcpRunStorage{recs: []domain.AcpRunRecord{
			{ID: "acprun_old", ConversationID: "conv_a", Status: domain.AcpRunCompleted},
			{ID: "acprun_other", ConversationID: "conv_b", Status: domain.AcpRunCompleted},
		}},
	}

	resp, rpcErr := app.handleAcpRunsList(contracts.AcpRunsListRequest{ConversationID: "conv_a"})
	if rpcErr != nil {
		t.Fatalf("runs.list: %v", rpcErr)
	}
	out := resp.(contracts.AcpRunsListResult)
	if len(out.Runs) != 2 {
		t.Fatalf("conv_a should see only its own runs, got %+v", out.Runs)
	}
	for _, r := range out.Runs {
		if r.ConversationID != "conv_a" {
			t.Fatalf("leaked run from another conversation: %+v", r)
		}
	}
}
