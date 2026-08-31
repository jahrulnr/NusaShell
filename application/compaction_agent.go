package application

import (
	"context"
	"fmt"
	"strings"
)

// compactionPass is the AgentCompaction rule set for ONE chunk pass: the
// summary() tool is forced via ToolChoice, the tool call is never
// executed (its args ARE the output), and failed attempts double the
// token budget until the summary is long enough or the budget is
// exhausted. The chunk loop (takeCompactionChunk) lives in the caller;
// the engine runs one pass.
type compactionPass struct {
	app       *App
	adapter   ProviderContext
	model     string
	system    string
	msgs      []ChatMessage
	budget    int
	maxBudget int
	minChars  int
	convID    string
	lastLen   int
	lastErr   error
	summary   string // valid summary from the terminal round
}

// run executes the pass through the AgentEngine and reports whether a
// valid summary was produced.
func (p *compactionPass) run(ctx context.Context) bool {
	_, _ = (&AgentEngine{}).Run(ctx, p.rules(), compactionSummaryMaxRetries+1)
	return p.summary != ""
}

func (p *compactionPass) rules() AgentRules {
	return AgentRules{
		Stream: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			return p.app.completeWithRetry(ctx, p.adapter, req)
		},
		BuildRequest: func(st *RoundState) ChatRequest {
			return ChatRequest{
				Model:      p.model,
				System:     p.system,
				Messages:   p.msgs,
				Tools:      toolFactoryFor(p.app).Get(AgentCompaction, ""),
				ToolChoice: compactionToolChoice(p.adapter.Kind),
				MaxTokens:  p.budget,
			}
		},
		// Terminal: the summary() tool call (or content fallback) is
		// valid when it is long enough and does not echo the assistant.
		Terminal: func(st *RoundState, resp ChatResponse) bool {
			summary := extractCompactionSummary(resp)
			if compactionSummaryEchoesAssistant(summary, p.msgs) {
				summary = ""
			}
			p.lastLen = len(strings.TrimSpace(summary))
			if p.lastLen < p.minChars {
				return false
			}
			p.summary = summary
			return true
		},
		// A stream error that survived completeWithRetry advances to the
		// next attempt with the same budget, mirroring the pre-engine
		// `continue` on error.
		OnStreamErr: func(st *RoundState, err error) bool {
			p.lastErr = err
			p.app.log("warn", "agent", "compaction pass %d failed for %s: %v", st.Round+1, p.convID, err)
			return true
		},
		// Non-terminal round: double the budget for the retry, clamped to
		// the context window.
		OnRound: func(st *RoundState, resp ChatResponse, outcomes []ToolOutcome) error {
			next := p.budget * 2
			if next > p.maxBudget {
				next = p.maxBudget
			}
			p.app.log("warn", "agent", "compaction pass %d produced short summary (%d chars, min %d) for %s, retrying with budget %d",
				st.Round+1, p.lastLen, p.minChars, p.convID, next)
			p.budget = next
			p.lastErr = fmt.Errorf("compaction summary too short (%d chars, min %d) after %d attempts",
				p.lastLen, p.minChars, st.Round+1)
			return nil
		},
	}
}
