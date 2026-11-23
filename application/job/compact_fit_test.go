package job

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	toolsummary "github.com/jahrulnr/nusashell/infrastructure/service/tool/summary"
)

// fatHistory is an earlier checkpoint followed by n tool steps whose
// outputs are 20 KB each.
func fatHistory(t *testing.T, n int) []entity.Message {
	t.Helper()
	head, _ := entity.NewMessage(enum.RoleUser, "CHECKPOINT: before")
	head.SetUserType(enum.LLMUserTypeSystem)
	out := []entity.Message{head}
	for i := range n {
		m, _ := entity.NewMessage(enum.RoleAssistant, fmt.Sprintf("step %d", i))
		tc, err := entity.NewToolCall(fmt.Sprintf("c%d", i), "exec", `{}`, enum.TooltypeOrigin)
		if err != nil {
			t.Fatal(err)
		}
		tc.SetOutput(strings.Repeat("y", 20_000))
		if err := m.AddToolCall(tc); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func sizeOf(h []entity.Message) int {
	n := 0
	for _, m := range h {
		n += len(m.Content())
		for _, tc := range m.ToolCalls() {
			n += len(tc.OutputWithUntrustedTag())
		}
	}
	return n
}

func TestFitCompactionInput(t *testing.T) {
	h := fatHistory(t, 6)
	under := func(limit int) func([]entity.Message) bool {
		return func(m []entity.Message) bool { return sizeOf(m) <= limit }
	}
	if got, _ := fitCompactionInput(h, under(1<<30)); len(got) != 7 || got[1].ToolCalls()[0].OutputBudget() != 32000 {
		t.Fatal("a history that fits goes whole")
	}
	// Clipping each output to 8000 bytes is enough here.
	got, err := fitCompactionInput(h, under(60_000))
	if err != nil || len(got) != 7 || got[1].ToolCalls()[0].OutputBudget() != 8000 {
		t.Fatalf("clipped: %d msgs, budget %d, %v", len(got), got[1].ToolCalls()[0].OutputBudget(), err)
	}
	if h[1].ToolCalls()[0].OutputBudget() != 32000 {
		t.Fatal("fitting changed the history it was handed")
	}
	// Too small even at the tightest clip: the oldest steps go, the
	// earlier checkpoint stays first, and a note says what is missing.
	got, err = fitCompactionInput(h, under(1_500))
	if err != nil || len(got) < 3 || got[0].Content() != "CHECKPOINT: before" ||
		!strings.Contains(got[1].Content(), "earlier messages are left out") || got[len(got)-1].Content() != "step 5" {
		t.Fatalf("dropped: %+v %v", got, err)
	}
	if sizeOf(got) > 1_500 {
		t.Fatalf("still %d bytes", sizeOf(got))
	}
}

// longTurnSvc asks for "push" on its first ten real calls, then answers;
// it serves every handoff and counts them.
type longTurnSvc struct {
	real, handoffs atomic.Int32
	failHandoff    int32 // handoff number to refuse with a 413; 0 = never
}

func (s *longTurnSvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	for _, tool := range conv.Tools() {
		if tool.Name() == toolsummary.ToolName {
			if s.handoffs.Add(1) == s.failHandoff {
				return apperror.New(413, "context overflow", nil)
			}
			m, _ := entity.NewMessage(enum.RoleAssistant, "")
			tc, _ := entity.NewToolCall(fmt.Sprintf("sum_%d", s.handoffs.Load()), toolsummary.ToolName, `{"text":"CHECKPOINT"}`, enum.TooltypeOrigin)
			m.AddToolCall(tc)
			m.SetFinishReason(enum.FinishReasonToolCalls)
			conv.Append(m)
			return nil
		}
	}
	n := s.real.Add(1)
	if n > 10 {
		m, _ := entity.NewMessage(enum.RoleAssistant, "done")
		m.SetFinishReason(enum.FinishReasonStop)
		conv.Append(m)
		return nil
	}
	m, _ := entity.NewMessage(enum.RoleAssistant, "")
	tc, _ := entity.NewToolCall(fmt.Sprintf("call_%d", n), "push", `{}`, enum.TooltypeOrigin)
	m.AddToolCall(tc)
	m.SetFinishReason(enum.FinishReasonToolCalls)
	conv.Append(m)
	return nil
}

func longTurn(t *testing.T, svc *longTurnSvc) *aggregate.Conversation {
	t.Helper()
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 64, CompactBudgetPct: 100, CompactKeepTail: 2},
	}
	conv := seedRoom(t, d.Convs, "room", "go")
	// A 500-token input allowance: every fat tool result fills it.
	model := entity.Model{}
	model.SetContextWindow(entity.DefaultUnknownMaxOutputTokens + 500)
	schema, _ := valueobject.NewToolSchema("push", "test tool", nil, true, enum.ToolPermissionAgent)
	driver := EnvironmentBundle{Tools: map[string]dservice.Tool{"push": fatTool{schema: schema}}}
	if err := d.runTurn(t.Context(), "room", conv, driver, nil, model, svc, nil, nil); err != nil {
		t.Fatal(err)
	}
	return conv
}

// A turn that fills the window again after folding folds again — over
// the history it added since — instead of running into the provider's
// limit; the folds a turn pays for stay bounded.
func TestRunTurnFoldsAgainInALongTurn(t *testing.T) {
	svc := &longTurnSvc{}
	conv := longTurn(t, svc)
	if n := svc.handoffs.Load(); n < 2 || n > 8 {
		t.Fatalf("folds = %d, want more than one and at most eight", n)
	}
	if conv.Last().Content() != "done" {
		t.Fatalf("the turn must finish: %+v", conv.Last())
	}
}

// A handoff the provider refuses as too large is sent again, smaller,
// instead of failing the turn.
func TestCompactionRetriesASmallerHandoffOn413(t *testing.T) {
	svc := &longTurnSvc{failHandoff: 1}
	conv := longTurn(t, svc)
	if conv.Last().Content() != "done" || conv.Messages()[0].UserType() != enum.LLMUserTypeSystem {
		t.Fatalf("the refused handoff must be retried: %+v", conv.Messages())
	}
}
