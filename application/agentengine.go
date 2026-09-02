package application

import (
	"context"

	"nusashell/domain"
)

// AgentEngine is the single round-loop skeleton shared by every agent
// (conversation, automation, review, compaction, and future kinds). Each
// agent is one AgentRules value; the engine owns the loop, rules own
// input, tool handling, and output. The authoritative contract is
// docs/decisions/003-agent-engine.md.
type AgentEngine struct{}

// RoundState is the engine-owned scratch space for one run: the message
// list grows across rounds; rules read and write it via hooks.
type RoundState struct {
	Messages []ChatMessage
	Round    int
}

// ToolOutcome is the result of one executed tool call, in call order.
type ToolOutcome struct {
	Status domain.ToolCallStatus
	Output string
	Atts   []domain.Attachment
}

// AgentRules parameterizes the round-loop engine. Nil fields mean the
// rule is not used by that agent — skip, not a confusing no-op.
type AgentRules struct {
	// Stream performs one provider request per round. Rules choose the
	// transport: conversation/review stream (delta callbacks), compaction
	// completes. Provider retry (backoff) lives inside Stream — the engine
	// never retries on its own.
	Stream func(ctx context.Context, req ChatRequest) (ChatResponse, error)

	// BuildRequest assembles the next round's provider request.
	BuildRequest func(st *RoundState) ChatRequest

	// Terminal decides whether the response ends the run.
	Terminal func(st *RoundState, resp ChatResponse) bool

	// Execute handles one round's tool calls, with the round's response
	// (an agent may need to persist the round before executing its tools).
	// An error fails the run. nil = the agent never executes tools
	// (compaction extracts summary() args instead).
	Execute func(st *RoundState, resp ChatResponse, calls []domain.ToolCall) ([]ToolOutcome, error)

	// BeforeRound runs before every stream: proactive compaction,
	// steer/subagent drains, cross-round budget adjustments.
	BeforeRound func(st *RoundState) error

	// OnStreamErr may recover a failed stream (emergency compaction,
	// learned-param adaptation). Returning true advances the run to the
	// next round.
	OnStreamErr func(st *RoundState, err error) bool

	// OnRound runs after Execute — and also on the terminal round —
	// to persist, emit events, fold summaries, or collect mutations.
	OnRound func(st *RoundState, resp ChatResponse, outcomes []ToolOutcome) error

	// AfterRound runs after OnRound and may continue the run (return
	// true) even when Terminal said done — conversation drains queued
	// steer/subagent results at this boundary and starts a new round
	// when anything was injected. nil = terminal is final.
	AfterRound func(st *RoundState, resp ChatResponse, outcomes []ToolOutcome) (bool, error)
}

// Run executes the rules' rounds: at most maxRounds (0 = unlimited —
// conversation relies on drain-bounded rounds, never a hard cap).
// The terminal round still passes through OnRound/AfterRound before the
// run returns.
func (e *AgentEngine) Run(ctx context.Context, rules AgentRules, maxRounds int) (RoundState, error) {
	var st RoundState
	for {
		if maxRounds > 0 && st.Round >= maxRounds {
			break
		}
		if err := ctx.Err(); err != nil {
			return st, err
		}
		if rules.BeforeRound != nil {
			if err := rules.BeforeRound(&st); err != nil {
				return st, err
			}
		}
		var req ChatRequest
		if rules.BuildRequest != nil {
			req = rules.BuildRequest(&st)
		}
		resp, err := rules.Stream(ctx, req)
		if err != nil {
			if rules.OnStreamErr != nil && rules.OnStreamErr(&st, err) {
				st.Round++
				continue
			}
			return st, err
		}
		if rules.Terminal(&st, resp) {
			if rules.OnRound != nil {
				if err := rules.OnRound(&st, resp, nil); err != nil {
					return st, err
				}
			}
			if rules.AfterRound != nil {
				cont, err := rules.AfterRound(&st, resp, nil)
				if err != nil {
					return st, err
				}
				if cont {
					st.Round++
					continue
				}
			}
			return st, nil
		}
		var outcomes []ToolOutcome
		if rules.Execute != nil {
			var execErr error
			outcomes, execErr = rules.Execute(&st, resp, resp.ToolCalls)
			if execErr != nil {
				return st, execErr
			}
		}
		if rules.OnRound != nil {
			if err := rules.OnRound(&st, resp, outcomes); err != nil {
				return st, err
			}
		}
		if rules.AfterRound != nil {
			cont, err := rules.AfterRound(&st, resp, outcomes)
			if err != nil {
				return st, err
			}
			if cont {
				st.Round++
				continue
			}
		}
		st.Round++
	}
	return st, nil
}
