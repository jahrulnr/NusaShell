package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nusashell/domain"
)

// engineStreamStub records the requests it streams and returns scripted
// responses.
type engineStreamStub struct {
	responses []ChatResponse
	errs      []error
	reqs      []ChatRequest
}

func (s *engineStreamStub) stream(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	s.reqs = append(s.reqs, req)
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		if err != nil {
			return ChatResponse{}, err
		}
	}
	resp := ChatResponse{}
	if len(s.responses) > 0 {
		resp = s.responses[0]
		s.responses = s.responses[1:]
	}
	return resp, nil
}

func TestAgentEngineRunsUntilTerminal(t *testing.T) {
	stub := &engineStreamStub{
		responses: []ChatResponse{
			{ToolCalls: []domain.ToolCall{{ID: "c1", Name: "exec"}}}, // round 0: tools
			{Content: "done"}, // round 1: terminal
		},
	}
	var rounds []int
	var outcomes [][]ToolOutcome
	var terminalRounds []int

	_, err := (&AgentEngine{}).Run(context.Background(), AgentRules{
		Stream: stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest {
			return ChatRequest{Messages: st.Messages}
		},
		Terminal: func(st *RoundState, resp ChatResponse) bool {
			terminalRounds = append(terminalRounds, st.Round)
			return len(resp.ToolCalls) == 0
		},
		Execute: func(st *RoundState, resp ChatResponse, calls []domain.ToolCall) ([]ToolOutcome, error) {
			rounds = append(rounds, st.Round)
			out := make([]ToolOutcome, len(calls))
			for i := range calls {
				out[i] = ToolOutcome{Status: domain.ToolOK, Output: "out"}
			}
			return out, nil
		},
		OnRound: func(st *RoundState, resp ChatResponse, out []ToolOutcome) error {
			outcomes = append(outcomes, out)
			st.Messages = append(st.Messages, ChatMessage{Role: "tool"})
			return nil
		},
	}, 5)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(stub.reqs) != 2 {
		t.Fatalf("streamed %d rounds, want 2", len(stub.reqs))
	}
	if len(rounds) != 1 || rounds[0] != 0 {
		t.Fatalf("Execute ran on rounds %v, want [0]", rounds)
	}
	if len(outcomes) != 2 {
		t.Fatalf("OnRound ran %d times, want 2 (tool round + terminal round)", len(outcomes))
	}
	if len(terminalRounds) != 2 {
		t.Fatalf("Terminal evaluated %d times, want 2", len(terminalRounds))
	}
}

func TestAgentEngineStopsAtMaxRounds(t *testing.T) {
	stub := &engineStreamStub{
		responses: []ChatResponse{{ToolCalls: []domain.ToolCall{{ID: "c1", Name: "exec"}}}},
	}
	streams := 0
	_, err := (&AgentEngine{}).Run(context.Background(), AgentRules{
		Stream: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			streams++
			return stub.stream(ctx, req)
		},
		BuildRequest: func(st *RoundState) ChatRequest { return ChatRequest{} },
		Terminal:     func(st *RoundState, resp ChatResponse) bool { return false },
	}, 3)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if streams != 3 {
		t.Fatalf("streamed %d rounds, want 3 (maxRounds)", streams)
	}
}

func TestAgentEngineStreamErrorRecoversViaHook(t *testing.T) {
	stub := &engineStreamStub{
		errs: []error{errors.New("boom")},
		responses: []ChatResponse{
			{Content: "recovered"},
		},
	}
	recovered := 0
	_, err := (&AgentEngine{}).Run(context.Background(), AgentRules{
		Stream: stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest {
			return ChatRequest{}
		},
		Terminal: func(st *RoundState, resp ChatResponse) bool { return true },
		OnStreamErr: func(st *RoundState, err error) bool {
			recovered++
			return true
		},
	}, 3)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("OnStreamErr ran %d times, want 1", recovered)
	}
	if len(stub.reqs) != 2 {
		t.Fatalf("streamed %d rounds, want 2 (failed + recovered)", len(stub.reqs))
	}
}

func TestAgentEngineStreamErrorUnrecoveredFails(t *testing.T) {
	stub := &engineStreamStub{errs: []error{errors.New("boom")}}
	_, err := (&AgentEngine{}).Run(context.Background(), AgentRules{
		Stream:       stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest { return ChatRequest{} },
		Terminal:     func(st *RoundState, resp ChatResponse) bool { return false },
	}, 3)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Run err = %v, want boom", err)
	}
}

func TestAgentEngineBeforeRoundAndCancellation(t *testing.T) {
	stub := &engineStreamStub{
		responses: []ChatResponse{{Content: "done"}},
	}
	beforeRounds := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := (&AgentEngine{}).Run(ctx, AgentRules{
		Stream: stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest {
			return ChatRequest{}
		},
		Terminal: func(st *RoundState, resp ChatResponse) bool { return true },
		BeforeRound: func(st *RoundState) error {
			beforeRounds++
			return nil
		},
	}, 3)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if beforeRounds != 1 {
		t.Fatalf("BeforeRound ran %d times, want 1", beforeRounds)
	}

	// Cancelled context fails before streaming.
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := (&AgentEngine{}).Run(ctx2, AgentRules{
		Stream:       stub.stream,
		BuildRequest: func(st *RoundState) ChatRequest { return ChatRequest{} },
		Terminal:     func(st *RoundState, resp ChatResponse) bool { return true },
	}, 3); err == nil {
		t.Fatal("Run with cancelled context must fail")
	}
}
