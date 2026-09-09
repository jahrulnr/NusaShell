package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"nusashell/application/tools"
	"nusashell/contracts"
	"nusashell/domain"
)

type lifecycleConvStore struct {
	mu   sync.Mutex
	byID map[string]*domain.Conversation
}

func (s *lifecycleConvStore) List() []*domain.Conversation { return nil }
func (s *lifecycleConvStore) Get(id string) (*domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.byID[id]
	if c == nil {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}
func (s *lifecycleConvStore) Save(c *domain.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]*domain.Conversation{}
	}
	cp := *c
	s.byID[c.ID] = &cp
	return nil
}
func (s *lifecycleConvStore) Delete(string) error { return nil }
func (s *lifecycleConvStore) ArchiveChunk(string, []domain.Message) (int, error) {
	return 0, nil
}
func (s *lifecycleConvStore) GetChunk(string, int) ([]domain.Message, error) {
	return nil, nil
}

func TestLifecycleListenerReceivesToolAndReasoning(t *testing.T) {
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{}}
	svc := New(Deps{Conversations: store})
	var mu sync.Mutex
	var events []AgentLifecycleEvent
	svc.AddAgentLifecycleListener(func(ev AgentLifecycleEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	run := &TurnRun{
		ID:             "run_life",
		ConversationID: "conv_life",
		Headless:       true,
		ToolKind:       tools.AgentAutomation,
		Ctx:            context.Background(),
	}
	svc.runsMu.Lock()
	svc.runs[run.ID] = run
	svc.runsMu.Unlock()

	svc.notifyLifecycleStepStart(run)
	svc.notifyLifecycleToolStart(run.ID, 1, "file_read", `{"path":"/tmp/x"}`)
	svc.notifyLifecycleDelta(run.ID, "msg", 1, contracts.RoundDeltaReasoning, "", "", "first thought")
	svc.notifyLifecycleDelta(run.ID, "msg", 1, contracts.RoundDeltaReasoning, "", "", "second thought ignored")
	svc.notifyLifecycleToolEnd(run, 1, "file_read", "ok", "hello world")
	svc.notifyLifecycleStepEnd(run, "success", "")

	mu.Lock()
	defer mu.Unlock()
	types := make([]string, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
		if e.ConversationID != "conv_life" || e.RunID != "run_life" {
			t.Fatalf("identity missing on %+v", e)
		}
	}
	want := []string{LifecycleStepStart, LifecycleToolStart, LifecycleReasoning, LifecycleToolEnd, LifecycleStepEnd}
	if len(types) != len(want) {
		t.Fatalf("types = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("types[%d] = %q, want %q (all %v)", i, types[i], want[i], types)
		}
	}
	// reasoning throttled to one per round
	var reasoning int
	for _, e := range events {
		if e.Type == LifecycleReasoning {
			reasoning++
			if e.Detail != "first thought" {
				t.Fatalf("reasoning detail = %q", e.Detail)
			}
		}
		if e.Type == LifecycleToolStart && !strings.Contains(e.Detail, "/tmp/x") {
			t.Fatalf("tool start detail = %q", e.Detail)
		}
	}
	if reasoning != 1 {
		t.Fatalf("reasoning count = %d, want 1", reasoning)
	}
}
