package application

import (
	"testing"

	"nusashell/domain"
)

func TestAskQuestionService_PendingForConversation(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{Question: "Q?", Options: []domain.AskQuestionOption{{ID: "a", Label: "A"}}}
	_, _ = s.Ask("run-1", "call-1", "conv-1", req)
	_, _ = s.Ask("run-1", "call-2", "conv-1", req)
	_, _ = s.Ask("run-2", "call-3", "conv-2", req)

	got := s.PendingForConversation("conv-1")
	if len(got) != 2 {
		t.Fatalf("PendingForConversation(conv-1) = %d asks, want 2", len(got))
	}
	for _, p := range got {
		if p.ConversationID != "conv-1" || p.Req.Question != "Q?" {
			t.Fatalf("unexpected pending ask: %+v", p)
		}
	}
	// Answering removes it from the pending list.
	if _, err := s.Answer("run-1", "call-1", domain.AskQuestionAnswer{Via: domain.AskAnswerViaOption, OptionIDs: []string{"a"}}); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if len(s.PendingForConversation("conv-1")) != 1 {
		t.Fatalf("after answer, conv-1 should have 1 pending ask")
	}
	if len(s.PendingForConversation("conv-2")) != 1 {
		t.Fatalf("conv-2 should still have 1 pending ask")
	}
	if len(s.PendingForConversation("missing")) != 0 {
		t.Fatalf("unknown conversation should have 0 pending asks")
	}
}

func TestAskQuestionService_Answer(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{
		Question:      "Which option?",
		Options:       []domain.AskQuestionOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
		AllowFreeText: true,
	}
	ch, err := s.Ask("run-1", "call-1", "conv-1", req)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if !s.HasPending("run-1", "call-1") {
		t.Fatal("expected pending ask")
	}
	result, err := s.Answer("run-1", "call-1", domain.AskQuestionAnswer{
		Via:       domain.AskAnswerViaOption,
		OptionIDs: []string{"a"},
	})
	if err != nil {
		t.Fatalf("Answer failed: %v", err)
	}
	if result.Answer != "A" {
		t.Fatalf("answer = %q, want %q", result.Answer, "A")
	}
	got := <-ch
	if !got.OK || got.Answer != "A" {
		t.Fatalf("channel result = %+v, want OK=true Answer=A", got)
	}
	if s.HasPending("run-1", "call-1") {
		t.Fatal("expected no pending ask after answer")
	}
}

func TestAskQuestionService_Cancel(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{
		Question: "Q?",
		Options:  []domain.AskQuestionOption{{ID: "a", Label: "A"}},
	}
	ch, _ := s.Ask("run-1", "call-1", "conv-1", req)
	s.Cancel("run-1", "call-1", "user cancelled")
	got := <-ch
	if got.OK {
		t.Fatalf("expected OK=false after cancel, got %+v", got)
	}
}

func TestAskQuestionService_RejectRun(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{
		Question: "Q?",
		Options:  []domain.AskQuestionOption{{ID: "a", Label: "A"}},
	}
	ch1, _ := s.Ask("run-1", "call-1", "conv-1", req)
	ch2, _ := s.Ask("run-1", "call-2", "conv-1", req)
	_, _ = s.Ask("run-2", "call-3", "conv-2", req)
	s.RejectRun("run-1", "turn ended")
	got1 := <-ch1
	got2 := <-ch2
	if got1.OK || got2.OK {
		t.Fatalf("expected both rejected, got %+v and %+v", got1, got2)
	}
	if !s.HasPending("run-2", "call-3") {
		t.Fatal("run-2 ask should still be pending")
	}
}

func TestAskQuestionService_DuplicateAsk(t *testing.T) {
	s := NewAskQuestionService()
	req := domain.AskQuestionRequest{
		Question: "Q?",
		Options:  []domain.AskQuestionOption{{ID: "a", Label: "A"}},
	}
	_, err := s.Ask("run-1", "call-1", "conv-1", req)
	if err != nil {
		t.Fatalf("first Ask failed: %v", err)
	}
	_, err = s.Ask("run-1", "call-1", "conv-1", req)
	if err == nil {
		t.Fatal("expected error on duplicate Ask")
	}
}

func TestAskQuestionService_AnswerNotFound(t *testing.T) {
	s := NewAskQuestionService()
	_, err := s.Answer("no-run", "no-call", domain.AskQuestionAnswer{})
	if err == nil {
		t.Fatal("expected error for unknown answer")
	}
}

func TestAskQuestionService_OnAsk(t *testing.T) {
	s := NewAskQuestionService()
	var gotRun, gotCall, gotConv string
	var gotReq domain.AskQuestionRequest
	s.SetOnAsk(func(runID, callID, convID string, req domain.AskQuestionRequest) {
		gotRun = runID
		gotCall = callID
		gotConv = convID
		gotReq = req
	})
	req := domain.AskQuestionRequest{
		Question: "Q?",
		Options:  []domain.AskQuestionOption{{ID: "a", Label: "A"}},
	}
	_, _ = s.Ask("run-1", "call-1", "conv-1", req)
	if gotRun != "run-1" || gotCall != "call-1" || gotConv != "conv-1" {
		t.Fatalf("callback got run=%s call=%s conv=%s", gotRun, gotCall, gotConv)
	}
	if gotReq.Question != "Q?" {
		t.Fatalf("callback got question=%q", gotReq.Question)
	}
}
