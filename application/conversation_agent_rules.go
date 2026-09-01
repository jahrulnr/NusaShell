package application

import (
	"context"

	"nusashell/contracts"
	"nusashell/domain"
)

// conversationRules is the AgentConversation rule set: the richest agent.
// It owns the persisted conversation, per-round events, proactive and
// emergency compaction, partial-stream continuation, steer/subagent
// drains at round boundaries, the repeated-tool guard, and usage
// accounting. The engine owns the loop; this struct owns every
// conversation-specific decision. See docs/decisions/003-agent-engine.md.
type conversationRules struct {
	a           *App
	run         *TurnRun
	adapter     ProviderContext
	conv        *domain.Conversation
	settings    domain.Settings
	provider    *domain.Provider
	model       string
	effort      string
	asstMsgID   string
	caps        ModelCapabilities
	toolDefs    []ToolDef
	maxTokens   int
	promptCache *PromptCachePolicy

	// Per-turn mutable state (mirrors the pre-engine locals).
	round                  int
	toolRounds             int
	currentMsgID           string
	continuation           bool
	continuedPartialStream bool
	prevMsgID, prevState   string
	prevRound              int
	compactionAttempts     int
	totalUsage             ChatUsage
	lastUsage              ChatUsage
	lastRound              streamedTurnRound
	repeatedGuard          *repeatedToolGuard
	turnEnded              bool // interrupt/fail already emitted by a hook
}

func (a *App) newConversationRules(run *TurnRun, adapter ProviderContext, conversation *domain.Conversation, settings domain.Settings, provider *domain.Provider, model, effort, asstMsgID string, caps ModelCapabilities, toolDefs []ToolDef, maxTokens int, promptCache *PromptCachePolicy, initialContinuation bool) *conversationRules {
	return &conversationRules{
		a: a, run: run, adapter: adapter, conv: conversation, settings: settings,
		provider: provider, model: model, effort: effort, asstMsgID: asstMsgID, caps: caps,
		toolDefs: toolDefs, maxTokens: maxTokens, promptCache: promptCache,
		currentMsgID: asstMsgID, continuation: initialContinuation,
		continuedPartialStream: initialContinuation,
		repeatedGuard:          &repeatedToolGuard{limit: settings.RepeatedToolLimit},
	}
}

func (p *conversationRules) rules() AgentRules {
	return AgentRules{
		Stream: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			rr, err := p.a.streamTurnRound(p.run, p.adapter, p.conv, p.currentMsgID, p.model, p.effort, p.toolsForRound(), p.settings, p.continuation, p.maxTokens, p.promptCache, p.caps, p.round)
			p.continuation = false
			resp := ChatResponse{
				Content:    rr.Content,
				Reasoning:  rr.Reasoning,
				ToolCalls:  rr.Response.ToolCalls,
				Usage:      rr.Response.Usage,
				StopReason: rr.Response.StopReason,
				Warnings:   rr.Response.Warnings,
			}
			p.totalUsage = mergeUsage(p.totalUsage, rr.Response.Usage)
			if rr.Response.Usage.ContextTokens() > 0 {
				p.lastUsage = rr.Response.Usage
			}
			p.lastRound = rr
			// One final provider response after the last tool result: the
			// model answers without being able to start another tool round.
			if p.toolRounds >= p.settings.MaxToolRounds && len(resp.ToolCalls) > 0 {
				p.a.log("warn", "agent", "turn %s requested a tool after reaching the %d-round limit", p.run.ID, p.settings.MaxToolRounds)
				resp.ToolCalls = nil
			}
			return resp, err
		},
		// BuildRequest is nil: conversation assembles its request inside
		// Stream via streamTurnRound (hydration, learned params, context
		// estimate events, retry), so the request never builds twice.
		Terminal: func(st *RoundState, resp ChatResponse) bool {
			return len(resp.ToolCalls) == 0
		},
		BeforeRound: func(st *RoundState) error {
			p.round++
			// Pre-API proactive compaction check (see the pre-engine
			// comment in runSingleTurn): between rounds, tool results
			// grow the context.
			if p.settings.CompactionEnabled && p.round > 1 && p.compactionAttempts < 3 {
				cw := p.a.resolveContextWindow(p.provider, p.model, p.settings)
				trigger := domain.CompactionTriggerTokens(cw, domain.ResolveMaxOutput(p.provider, p.model, p.settings), p.settings)
				if est := p.conv.EstimateTokens(); est > trigger {
					p.compactionAttempts++
					p.a.log("info", "agent", "mid-turn compaction for %s round %d: est=%d trigger=%d window=%d",
						p.run.ID, p.round, est, trigger, cw)
					compAdapter, compModel, compWindow := p.a.resolveCompactionAdapter(p.run.Ctx, p.adapter, p.model, cw, p.settings)
					p.a.emitCompactionStarted(p.run, p.conv.ID)
					summary, compErr := p.a.compactConversation(p.run.Ctx, compAdapter, p.conv, compModel, compWindow, p.settings)
					if compErr == nil {
						p.a.Bus.Emit(contracts.EventCompacted, contracts.CompactedEvent{RunID: p.run.ID, ConversationID: p.conv.ID, Summary: summary})
						refreshed, getErr := p.a.Conversations.Get(p.run.ConversationID)
						if getErr != nil {
							return getErr
						}
						p.conv = refreshed
						p.a.log("info", "agent", "mid-turn compaction done for %s round %d: before=%d after=%d (msgs=%d)",
							p.run.ID, p.round, est, p.conv.EstimateTokens(), len(p.conv.Messages))
					} else {
						p.a.log("warn", "agent", "mid-turn compaction failed for %s round %d: %v", p.run.ID, p.round, compErr)
						p.a.Bus.Emit(contracts.EventCompactionFailed, contracts.CompactionFailedEvent{RunID: p.run.ID, ConversationID: p.conv.ID, Error: compErr.Error()})
					}
				}
			}
			// Seal the previous round's stream so SSE consumers chain to
			// the next stream. Partial continuation rounds seal as
			// "partial"; the persisted first half continues in the new
			// message.
			if p.prevMsgID != "" && p.prevMsgID != p.currentMsgID {
				p.a.sealRound(p.run, p.prevMsgID, p.prevRound, p.prevState, &contracts.RoundRef{RunID: p.run.ID, MessageID: p.currentMsgID, Round: p.round}, nil, "")
			}
			p.prevMsgID, p.prevRound, p.prevState = p.currentMsgID, p.round, "done"
			p.a.Bus.Emit(contracts.EventTurnStarted, contracts.TurnStartedEvent{
				RunID: p.run.ID, ConversationID: p.run.ConversationID, MessageID: p.currentMsgID, Round: p.round,
			})
			return nil
		},
		OnStreamErr: func(st *RoundState, err error) bool {
			if p.run.Ctx.Err() != nil {
				p.a.interruptTurn(p.run, p.currentMsgID, p.lastRound, p.totalUsage, p.lastUsage.ContextTokens(), p.model)
				p.turnEnded = true
				return false
			}
			// Capture the raw error before decoration: decorateRateLimitError
			// drops the UpstreamError type the overflow/TPM classifiers need.
			rawStreamErr := err
			err = p.a.decorateRateLimitError(p.provider.ID, err)
			if !p.continuedPartialStream && isRetryableProviderError(err) && len(p.lastRound.Response.ToolCalls) == 0 && (visibleText(p.lastRound.Content) != "" || visibleText(p.lastRound.Reasoning) != "") {
				// A partial stream must never carry an unconfirmed tool call
				// into the next continuation request.
				p.lastRound.Response.ToolCalls = nil
				if perr := p.a.persistTurnRound(p.run.ConversationID, p.currentMsgID, p.model, p.lastRound); perr != nil {
					p.a.failTurn(p.run, p.currentMsgID, perr)
					p.turnEnded = true
					return false
				}
				p.a.log("warn", "ai", "continuing partial provider stream for turn %s", p.run.ID)
				conv, msgID, aerr := p.a.appendTurnAssistant(p.run.ConversationID)
				if aerr != nil {
					p.a.failTurn(p.run, p.currentMsgID, aerr)
					p.turnEnded = true
					return false
				}
				p.conv, p.currentMsgID = conv, msgID
				p.run.setMessageID(p.currentMsgID)
				p.continuation = true
				p.continuedPartialStream = true
				// Not an additional tool round; the interrupted stream is
				// sealed as "partial" when the next round starts.
				p.prevState = "partial"
				p.round--
				return true
			}
			if p.compactionAttempts < 3 && (isContextOverflowError(rawStreamErr) || isTPMOverflowError(rawStreamErr)) {
				cw := p.a.resolveContextWindow(p.provider, p.model, p.settings)
				trigger := domain.CompactionTriggerTokens(cw, domain.ResolveMaxOutput(p.provider, p.model, p.settings), p.settings)
				preEmg := p.conv.EstimateTokens()
				if !shouldEmergencyCompact(rawStreamErr, preEmg, trigger) {
					p.a.log("warn", "agent", "overflow-like 400 for turn %s but est=%d <= trigger=%d; skipping emergency compaction", p.run.ID, preEmg, trigger)
					p.a.failStreamTurn(p.run, p.currentMsgID, p.model, p.lastRound, err)
					p.turnEnded = true
					return false
				}
				p.compactionAttempts++
				p.a.log("warn", "agent", "request too large for turn %s (est=%d trigger=%d), forcing emergency compaction", p.run.ID, preEmg, trigger)
				compAdapter, compModel, compWindow := p.a.resolveCompactionAdapter(p.run.Ctx, p.adapter, p.model, cw, p.settings)
				p.a.emitCompactionStarted(p.run, p.conv.ID)
				summary, compErr := p.a.compactConversation(p.run.Ctx, compAdapter, p.conv, compModel, compWindow, p.settings)
				if compErr == nil {
					p.a.Bus.Emit(contracts.EventCompacted, contracts.CompactedEvent{RunID: p.run.ID, ConversationID: p.conv.ID, Summary: summary})
					refreshed, getErr := p.a.Conversations.Get(p.run.ConversationID)
					if getErr != nil {
						p.a.failStreamTurn(p.run, p.currentMsgID, p.model, p.lastRound, getErr)
						p.turnEnded = true
						return false
					}
					p.conv = refreshed
					p.a.log("info", "agent", "emergency compaction done for %s: before=%d after=%d (msgs=%d)",
						p.conv.ID, preEmg, p.conv.EstimateTokens(), len(p.conv.Messages))
					p.round--
					return true
				}
				p.a.log("warn", "agent", "emergency compaction failed for %s: %v", p.conv.ID, compErr)
				p.a.Bus.Emit(contracts.EventCompactionFailed, contracts.CompactionFailedEvent{RunID: p.run.ID, ConversationID: p.conv.ID, Error: compErr.Error()})
			}
			p.a.failStreamTurn(p.run, p.currentMsgID, p.model, p.lastRound, err)
			p.turnEnded = true
			return false
		},
		// Persist the round first (the message placeholder must carry the
		// tool calls before their results are patched in), then run the
		// tools concurrently.
		Execute: func(st *RoundState, resp ChatResponse, calls []domain.ToolCall) ([]ToolOutcome, error) {
			rr := streamedTurnRound{Content: resp.Content, Reasoning: resp.Reasoning, Response: resp}
			if err := p.a.persistTurnRound(p.run.ConversationID, p.currentMsgID, p.model, rr); err != nil {
				return nil, err
			}
			if err := p.a.executeTurnTools(p.run, p.currentMsgID, calls, p.caps, p.settings, p.round); err != nil {
				if p.run.Ctx.Err() != nil {
					p.a.interruptTurn(p.run, p.currentMsgID, rr, p.totalUsage, p.lastUsage.ContextTokens(), p.model)
					p.turnEnded = true
				}
				return nil, err
			}
			p.toolRounds++
			return nil, nil
		},
		// Terminal rounds skip Execute, so the final text is persisted
		// here instead. (Non-terminal rounds were persisted by Execute.)
		OnRound: func(st *RoundState, resp ChatResponse, outcomes []ToolOutcome) error {
			if len(resp.ToolCalls) != 0 {
				return nil
			}
			rr := streamedTurnRound{Content: resp.Content, Reasoning: resp.Reasoning, Response: resp}
			return p.a.persistTurnRound(p.run.ConversationID, p.currentMsgID, p.model, rr)
		},
		// Round boundary: repeated-tool guard, then drain queued
		// steer/subagent results. A tool round always continues with a
		// fresh assistant message; a terminal round continues only when a
		// drain injected something the model must see.
		AfterRound: func(st *RoundState, resp ChatResponse, outcomes []ToolOutcome) (bool, error) {
			if len(resp.ToolCalls) > 0 && p.repeatedGuard.check(resp.ToolCalls, resp.Content) {
				p.a.log("warn", "agent", "turn %s: detected repeated tool round (%dx identical set), forcing text-only round", p.run.ID, p.repeatedGuard.limit)
				p.toolRounds = p.settings.MaxToolRounds
			}
			appliedSteer, steerErr := p.a.applyQueuedSteer(p.run)
			if steerErr != nil {
				return false, steerErr
			}
			appliedSub, subErr := p.a.applyQueuedRunResults(p.run)
			if subErr != nil {
				return false, subErr
			}
			if len(resp.ToolCalls) == 0 && !appliedSteer && !appliedSub {
				return false, nil // terminal
			}
			conv, msgID, err := p.a.appendTurnAssistant(p.run.ConversationID)
			if err != nil {
				return false, err
			}
			p.conv, p.currentMsgID = conv, msgID
			p.run.setMessageID(p.currentMsgID)
			return true, nil
		},
	}
}

// toolsForRound strips the tool list once the round budget is exhausted:
// the final provider response answers without starting another tool round.
func (p *conversationRules) toolsForRound() []ToolDef {
	if p.toolRounds >= p.settings.MaxToolRounds {
		return nil
	}
	return p.toolDefs
}

// totalUsageTokens is the sum of per-round usage (↑/↓ display tags).
func (p *conversationRules) totalUsageTokens() ChatUsage { return p.totalUsage }

// contextTokens is the last round's authoritative context fill.
func (p *conversationRules) contextTokens() int { return p.lastUsage.ContextTokens() }

// roundNumber is the current round for event/stream bookkeeping.
func (p *conversationRules) roundNumber() int { return p.round }

// messageID is the current assistant message being written.
func (p *conversationRules) messageID() string { return p.currentMsgID }
