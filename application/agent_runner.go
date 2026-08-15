package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func (a *App) handleTurnsStart(ctx context.Context, req contracts.TurnStartRequest) (any, *contracts.RPCError) {
	c, rpcErr := a.getConversation(req.ConversationID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	text := strings.TrimSpace(req.Text)
	attachments, rpcErr := attachmentsFromDTO(req.Attachments)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if text == "" && len(attachments) == 0 {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "message text is required"}
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "model is required"}
	}
	provider, bareModel, apiKey, rpcErr := a.resolveModel(model)
	if rpcErr != nil {
		return nil, rpcErr
	}
	// Store the qualified model ID (as sent by the UI) in the conversation
	// so the provider selection is preserved across restarts. The bare model
	// ID is used only for the API call.
	qualifiedModel := model

	a.startMu.Lock()
	defer a.startMu.Unlock()
	c, rpcErr = a.getConversation(req.ConversationID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if c.Status == "running" || a.activeRunForConversation(c.ID) != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
	}

	now := time.Now().UTC()
	userMsg := domain.Message{
		ID:          domain.NewID("msg"),
		Role:        domain.RoleUser,
		Content:     text,
		Attachments: attachments,
		CreatedAt:   now,
		Status:      domain.StatusDone,
	}
	asstMsg := domain.Message{
		ID:        domain.NewID("msg"),
		Role:      domain.RoleAssistant,
		CreatedAt: now,
	}
	c.AddMessage(userMsg)
	c.AddMessage(asstMsg)
	c.Model = qualifiedModel
	c.Effort = req.Effort
	c.Status = "running"
	if err := a.Conversations.Save(c); err != nil {
		return nil, rpcInternal(err)
	}

	// Turn goroutines outlive the HTTP request (fire-and-forget: the RPC
	// returns the RunID immediately while the turn streams via SSE/WS).
	// Derive from the request context but detach cancellation so the turn
	// is not killed when the HTTP response is sent. The turn is cancelled
	// explicitly via handleTurnsStop or server shutdown.
	turnCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	run := &TurnRun{ID: domain.NewID("run"), ConversationID: c.ID, MessageID: asstMsg.ID, Ctx: turnCtx, Cancel: cancel}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	go a.runTurn(run, provider, apiKey, bareModel, req.Effort, asstMsg.ID, false, modelSupportsVision(provider, bareModel))
	a.log("info", "agent", "turn started: %s (model %s)", run.ID, bareModel)
	return contracts.TurnStartResult{RunID: run.ID}, nil
}

// handleTurnsRetry re-runs the last failed assistant message with a different
// model picked by the user. When the failed message has partial content (and
// no tool calls), the partial is frozen as a completed step and the new model
// is asked to continue from where it stopped; otherwise the failed message is
// cleared and re-run from scratch.
func (a *App) handleTurnsRetry(ctx context.Context, req contracts.TurnRetryRequest) (any, *contracts.RPCError) {
	c, rpcErr := a.getConversation(req.ConversationID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "model is required"}
	}

	a.startMu.Lock()
	defer a.startMu.Unlock()
	c, rpcErr = a.getConversation(req.ConversationID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if c.Status == "running" || a.activeRunForConversation(c.ID) != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
	}

	failedIdx := -1
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == domain.RoleAssistant && c.Messages[i].Status == domain.StatusError {
			failedIdx = i
			break
		}
	}
	if failedIdx < 0 {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "no failed assistant turn to retry"}
	}

	provider, bareModel, apiKey, rpcErr := a.resolveModel(model)
	if rpcErr != nil {
		return nil, rpcErr
	}
	qualifiedModel := model

	failed := &c.Messages[failedIdx]
	continuation := shouldContinueFailedTurn(*failed)
	var targetMsgID string
	if continuation {
		failed.Status = domain.StatusDone
		failed.Error = ""
		next := domain.Message{ID: domain.NewID("msg"), Role: domain.RoleAssistant, CreatedAt: time.Now().UTC()}
		c.AddMessage(next)
		targetMsgID = next.ID
	} else {
		// Delete the failed message entirely and create a fresh one.
		// Wiping content in-place leaves a ghost "done" message with empty
		// content that shows up as a gap when the UI re-renders from server
		// state on turn completion.
		c.Messages = append(c.Messages[:failedIdx], c.Messages[failedIdx+1:]...)
		next := domain.Message{ID: domain.NewID("msg"), Role: domain.RoleAssistant, CreatedAt: time.Now().UTC()}
		c.AddMessage(next)
		targetMsgID = next.ID
	}
	c.Model = qualifiedModel
	c.Effort = req.Effort
	c.Status = "running"
	if err := a.Conversations.Save(c); err != nil {
		return nil, rpcInternal(err)
	}

	turnCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	run := &TurnRun{ID: domain.NewID("run"), ConversationID: c.ID, MessageID: targetMsgID, Ctx: turnCtx, Cancel: cancel}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	go a.runTurn(run, provider, apiKey, bareModel, req.Effort, targetMsgID, continuation, modelSupportsVision(provider, bareModel))
	a.log("info", "agent", "turn retried: %s (model %s)", run.ID, bareModel)
	return contracts.TurnStartResult{RunID: run.ID}, nil
}

func (a *App) handleTurnsStop(req contracts.TurnStopRequest) (any, *contracts.RPCError) {
	a.runsMu.Lock()
	run, ok := a.runs[req.RunID]
	a.runsMu.Unlock()
	if !ok {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "run not found or already finished"}
	}
	run.Cancel()
	a.log("info", "agent", "turn stopped: %s", run.ID)
	return map[string]bool{"ok": true}, nil
}

// activeRunForConversation returns the running TurnRun for the given
// conversation, or nil if none is active.
func (a *App) activeRunForConversation(convID string) *TurnRun {
	a.runsMu.Lock()
	defer a.runsMu.Unlock()
	for _, run := range a.runs {
		if run.ConversationID == convID {
			return run
		}
	}
	return nil
}

// handleTurnsActive returns the active run for a conversation, if any. Used by
// a refreshed frontend to re-attach its streaming UI and route new messages to
// steering instead of start when a turn is still running.
func (a *App) handleTurnsActive(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "id is required"}
	}
	run := a.activeRunForConversation(req.ID)
	if run == nil {
		return contracts.TurnActiveResult{Active: false}, nil
	}
	return contracts.TurnActiveResult{
		RunID:          run.ID,
		ConversationID: run.ConversationID,
		MessageID:      run.MessageID,
		Active:         true,
	}, nil
}

func (a *App) handleTurnsSteer(ctx context.Context, req contracts.TurnSteerRequest) (any, *contracts.RPCError) {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "steer text is required"}
	}
	run := a.activeRunForConversation(req.ConversationID)
	if run == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "no active turn for this conversation"}
	}
	attachments, rpcErr := attachmentsFromDTO(req.Attachments)
	if rpcErr != nil {
		return nil, rpcErr
	}
	now := time.Now().UTC()
	steerMsg := domain.Message{
		ID:          domain.NewID("msg"),
		Role:        domain.RoleUser,
		Content:     text,
		Attachments: attachments,
		CreatedAt:   now,
		Status:      domain.StatusDone,
		Steer:       true,
	}
	entry := &SteerEntry{
		ID:      domain.NewID("steer"),
		Text:    text,
		Status:  "queued",
		Message: steerMsg,
	}
	if !run.queueSteer(entry) {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "a steer is already queued for this turn"}
	}
	a.Bus.Emit(contracts.EventSteerQueued, contracts.SteerEvent{
		ConversationID: req.ConversationID, SteerID: entry.ID, Text: text, Status: "queued",
	})
	a.log("info", "agent", "steer queued for %s: %s", req.ConversationID, entry.ID)
	return map[string]any{"ok": true, "steer_id": entry.ID, "accepted": true}, nil
}

func (a *App) handleTurnsCancelSteer(req contracts.TurnCancelSteerRequest) (any, *contracts.RPCError) {
	run := a.activeRunForConversation(req.ConversationID)
	if run == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "no active turn for this conversation"}
	}
	if !run.cancelSteer() {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "no queued steer to cancel"}
	}
	a.Bus.Emit(contracts.EventSteerCancelled, contracts.SteerEvent{
		ConversationID: req.ConversationID, Status: "cancelled",
	})
	a.log("info", "agent", "steer cancelled for %s", req.ConversationID)
	return map[string]any{"ok": true, "accepted": true}, nil
}

func (a *App) runTurn(run *TurnRun, provider *domain.Provider, apiKey, model, effort, asstMsgID string, initialContinuation bool, supportsVision bool) {
	defer func() {
		run.Cancel()
		a.runsMu.Lock()
		delete(a.runs, run.ID)
		a.runsMu.Unlock()
	}()

	// Codex sticky account: pick the account bound to this conversation
	// so the Codex backend can reuse the prompt cache shard. If every
	// stored account is rate-limited or circuit-open, fail before the
	// first request instead of silently using the active (blocked) token.
	if provider.Kind == domain.ProviderCodex && a.CodexRouter != nil {
		accounts := a.listCodexAccountIDs(provider.ID)
		if len(accounts) > 0 {
			pick := a.CodexRouter.PickAccountDetailed(run.ConversationID, provider.ID, accounts)
			if pick.AccountID != "" {
				if token, has, _ := a.Credentials.Get(accountKey(provider.ID, pick.AccountID)); has {
					apiKey = token
				}
			} else if pick.AllRateLimited {
				a.failTurn(run, asstMsgID, allCodexAccountsLimitedError(pick.EarliestReset))
				return
			}
		}
	}

	adapter, conversation, settings, err := a.initializeTurn(run, provider, apiKey, model)
	if err != nil {
		a.failTurn(run, asstMsgID, err)
		return
	}
	// Vision fallback: when the active model does not support images but
	// the user attached images and configured a vision fallback model,
	// describe each image via the fallback and inject the description as
	// a text attachment on the latest user message. The original image is
	// preserved so a later switch to a vision model can still see it.
	if !supportsVision && settings.VisionProviderID != "" && settings.VisionModelID != "" {
		conversation = a.enrichWithVisionDescriptions(run.Ctx, conversation, asstMsgID, settings)
	}
	toolDefs := a.toolDefinitions()
	maxTokens := resolveMaxOutput(provider, model, settings)
	promptCache := buildPromptCachePolicy(settings, provider.ID, model, run.ConversationID)

	a.Bus.Emit(contracts.EventTurnStarted, contracts.TurnStartedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: asstMsgID, Round: 0,
	})

	var totalUsage ChatUsage
	currentMsgID := asstMsgID
	round := 0
	toolRounds := 0
	continuation := initialContinuation
	continuedPartialStream := initialContinuation
	// injectHydration is true on the first round of a turn (after the initial
	// user message or post-compaction) and whenever a steer is applied (a new
	// user message mid-turn). It is reset to false after the first round so
	// subsequent tool rounds do not re-inject the synthetic transcript.
	injectHydration := true
	for {
		round++
		toolsForRound := toolDefs
		if toolRounds >= settings.MaxToolRounds {
			// One final provider response after the last tool result lets the
			// model answer the user without being able to start another tool
			// round.
			toolsForRound = nil
		}
		a.Bus.Emit(contracts.EventTurnStarted, contracts.TurnStartedEvent{
			RunID: run.ID, ConversationID: run.ConversationID, MessageID: currentMsgID, Round: round,
		})
		roundResult, streamErr := a.streamTurnRound(run, adapter, conversation, currentMsgID, model, effort, toolsForRound, settings, continuation, maxTokens, injectHydration, promptCache, supportsVision)
		injectHydration = false // only the first round after a user message gets hydration
		continuation = false
		totalUsage = mergeUsage(totalUsage, roundResult.Response.Usage)
		if streamErr != nil {
			if run.Ctx.Err() != nil {
				a.interruptTurn(run, currentMsgID, roundResult, totalUsage, model)
				return
			}
			// Codex account failover with circuit breaker:
			// - Transient 429 (Retry-After \u2264 5min): MarkRateLimited (short cooldown)
			// - Usage exhausted (Retry-After > 5min): MarkCircuitOpen (long block until reset)
			// Then try a different account. If all accounts are blocked, fail with
			// a clear error so the user knows when to retry.
			if provider.Kind == domain.ProviderCodex && a.CodexRouter != nil && isRateLimitError(streamErr) {
				currentAccount := a.CodexRouter.StickyAccount(run.ConversationID)
				cooldown := rateLimitCooldown(streamErr)
				if cooldown > retryAfterCutoff {
					// Usage quota exhausted — open circuit until reset
					a.CodexRouter.MarkCircuitOpen(currentAccount, time.Now().Add(cooldown))
					a.log("warn", "ai", "codex circuit open: account %s usage exhausted for %s (conversation %s)",
						currentAccount, cooldown.Round(time.Minute), run.ConversationID)
					// Async fetch exact reset time from usage API.
					// Derive from the turn context so the goroutine exits
					// early if the turn is cancelled.
					go a.refreshCodexCircuit(context.WithoutCancel(run.Ctx), provider.ID, currentAccount)
				} else {
					a.CodexRouter.MarkRateLimited(currentAccount, cooldown)
				}
				accounts := a.listCodexAccountIDs(provider.ID)
				pickResult := a.CodexRouter.PickAccountDetailed(run.ConversationID, provider.ID, accounts)
				newAccount := pickResult.AccountID
				if newAccount != "" && newAccount != currentAccount {
					if newToken, has, _ := a.Credentials.Get(accountKey(provider.ID, newAccount)); has {
						newAdapter, buildErr := a.Factory(run.Ctx, provider, newToken)
						if buildErr == nil {
							adapter = newAdapter
							apiKey = newToken
							a.log("info", "ai", "codex failover: account %s \u2192 %s for conversation %s",
								currentAccount, newAccount, run.ConversationID)
							round--
							continue
						}
					}
				}
				if pickResult.AllRateLimited {
					streamErr = allCodexAccountsLimitedError(pickResult.EarliestReset)
				}
			}
			if !continuedPartialStream && canContinuePartialStream(streamErr, roundResult) {
				if err := a.persistPartialTurnRound(run.ConversationID, currentMsgID, model, roundResult); err != nil {
					a.failTurn(run, currentMsgID, err)
					return
				}
				a.log("warn", "ai", "continuing partial provider stream for turn %s", run.ID)
				conversation, currentMsgID, err = a.appendTurnAssistant(run.ConversationID)
				if err != nil {
					a.failTurn(run, currentMsgID, err)
					return
				}
				continuation = true
				continuedPartialStream = true
				// This replaces the interrupted provider attempt; it is not an
				// additional tool round and must not reduce the user's tool budget.
				round--
				continue
			} else {
				a.failStreamTurn(run, currentMsgID, model, roundResult, streamErr)
			}
			return
		}

		if toolRounds >= settings.MaxToolRounds && len(roundResult.Response.ToolCalls) > 0 {
			a.log("warn", "agent", "turn %s requested a tool after reaching the %d-round limit", run.ID, settings.MaxToolRounds)
			roundResult.Response.ToolCalls = nil
		}
		if err := a.persistTurnRound(run.ConversationID, currentMsgID, model, roundResult); err != nil {
			a.failTurn(run, currentMsgID, err)
			return
		}

		if len(roundResult.Response.ToolCalls) == 0 {
			// The model finished without requesting tools. Before exiting the
			// turn, drain any queued steer — if one is pending, inject it and
			// continue the loop so the model gets a new round to respond to it
			// instead of silently dropping the steer.
			applied, steerConv, steerErr := a.applyQueuedSteer(run, currentMsgID)
			if steerErr != nil {
				a.failTurn(run, currentMsgID, steerErr)
				return
			}
			if applied {
				conversation = steerConv
				conversation, currentMsgID, err = a.appendTurnAssistant(run.ConversationID)
				if err != nil {
					a.failTurn(run, currentMsgID, err)
					return
				}
				injectHydration = true // steer is a new user message — re-hydrate
				continue
			}
			break
		}

		if err := a.executeTurnTools(run, currentMsgID, roundResult.Response.ToolCalls, supportsVision, settings); err != nil {
			if run.Ctx.Err() != nil {
				a.interruptTurn(run, currentMsgID, roundResult, totalUsage, model)
				return
			}
			a.failTurn(run, currentMsgID, err)
			return
		}
		toolRounds++

		// Drain any queued steer message at this safe boundary (between tool
		// completion and the next provider round). The steer is appended as a
		// real user message so the provider sees it in the next round's context.
		applied, steerConv, steerErr := a.applyQueuedSteer(run, currentMsgID)
		if steerErr != nil {
			a.failTurn(run, currentMsgID, steerErr)
			return
		}
		if applied {
			conversation = steerConv
			injectHydration = true // steer is a new user message — re-hydrate
		}

		conversation, currentMsgID, err = a.appendTurnAssistant(run.ConversationID)
		if err != nil {
			a.failTurn(run, currentMsgID, err)
			return
		}
	}

	if err := a.finishTurn(run, asstMsgID, model, totalUsage); err != nil {
		a.failTurn(run, asstMsgID, err)
	}
	a.discardQueuedSteer(run)
}

// applyQueuedSteer drains a queued steer and appends it as a real user message
// at the current safe boundary. Returns (true, updatedConversation, nil) when a
// steer was applied, (false, nil, nil) when no steer was queued.
func (a *App) applyQueuedSteer(run *TurnRun, currentMsgID string) (bool, *domain.Conversation, error) {
	entry := run.drainSteer()
	if entry == nil {
		return false, nil, nil
	}
	c, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return false, nil, err
	}
	c.AddMessage(entry.Message)
	if err := a.Conversations.Save(c); err != nil {
		return false, nil, err
	}
	a.Bus.Emit(contracts.EventSteerApplied, contracts.SteerEvent{
		ConversationID: run.ConversationID, SteerID: entry.ID, Text: entry.Text, Status: "applied",
	})
	a.log("info", "agent", "steer applied for %s: %s", run.ConversationID, entry.ID)
	return true, c, nil
}

func canContinuePartialStream(err error, round streamedTurnRound) bool {
	return isRetryableProviderError(err) && len(round.Response.ToolCalls) == 0 && (round.Content != "" || round.Reasoning != "")
}

// shouldContinueFailedTurn reports whether a retry should freeze partial
// output and ask the new model to continue. Tool-bearing failures are always
// restarted from scratch so a leftover continuation flag cannot skip tool
// work or consume the mid-stream continuation budget.
func shouldContinueFailedTurn(failed domain.Message) bool {
	return (failed.Content != "" || failed.Reasoning != "") && len(failed.ToolCalls) == 0
}

// compactionTriggerTokens is the estimated-token watermark that starts
// compaction: the configured threshold, capped at 80% of the model window so
// a high threshold cannot wait until the next turn already overflows.
func compactionTriggerTokens(contextWindow int, settings domain.Settings) int {
	trigger := settings.CompactionThreshold
	if trigger < 1000 {
		trigger = domain.DefaultSettings().CompactionThreshold
	}
	windowCap := contextWindow * 4 / 5
	if windowCap > 0 && windowCap < trigger {
		return windowCap
	}
	return trigger
}

// resolveMaxOutput picks the per-turn completion token ceiling. The model's
// advertised max output is used when known, but capped by the global settings
// default — the setting acts as a ceiling, not just a fallback. This prevents
// sending absurdly high max_tokens values (e.g. 1M for models that advertise
// it) which cause credit/balance rejections on gateways like OpenRouter.
func resolveMaxOutput(provider *domain.Provider, model string, settings domain.Settings) int {
	cap := settings.MaxOutputTokens
	if cap <= 0 {
		cap = 65536
	}
	for _, m := range provider.Models {
		if m.ID == model && m.MaxOutput > 0 {
			if m.MaxOutput < cap {
				return m.MaxOutput
			}
			return cap
		}
	}
	return cap
}

// resolveContextWindow picks the effective context window for compaction
// decisions: the model's advertised context when known, otherwise the global
// max_input_tokens fallback.
func resolveContextWindow(provider *domain.Provider, model string, settings domain.Settings) int {
	for _, m := range provider.Models {
		if m.ID == model && m.Context > 0 {
			return m.Context
		}
	}
	return settings.MaxInputTokens
}

// compactConversation summarizes the conversation history via multi-pass
// rolling compaction so that conversations larger than the model's context
// window are still fully summarized without dropping any messages.
//
// The conversation is split into chunks that fit within the model's context
// window. Each chunk is summarized together with the running summary from the
// previous pass, producing a progressively folded summary that preserves all
// prior context. The most recent messages are kept intact.
//
// When the adapter implements ServerCompactor (e.g. Codex), the call is
// delegated to the server-side compact endpoint first. If that fails (e.g.
// 404 for free accounts, network error), the function falls back to the
// client-side multi-pass summarization below.
func (a *App) compactConversation(ctx context.Context, adapter AIProvider, c *domain.Conversation, model string, contextWindow int) (string, error) {
	const (
		keepTokenBudget = 64000 // retained recent messages token budget
		summaryMaxOut   = 800
		systemReserve   = 300  // system prompt + framing overhead
		summaryReserve  = 2000 // running summary from previous pass
	)

	if len(c.Messages) <= 1 {
		return "", nil
	}

	// Edge case: if the adapter supports server-side compaction, try it
	// first. On any error, fall back to client-side compaction so the
	// conversation still gets summarized.
	if compactor, ok := adapter.(ServerCompactor); ok {
		summary, err := compactor.CompactServer(ctx, c, model, contextWindow)
		if err == nil {
			// Server-side compaction succeeded. Archive the dropped
			// messages and apply the summary the same way client-side
			// compaction does, so the conversation shape stays consistent.
			effectiveKeepBudget := keepTokenBudget
			if cap := contextWindow * 3 / 10; cap < effectiveKeepBudget {
				effectiveKeepBudget = cap
			}
			if effectiveKeepBudget < 1000 {
				effectiveKeepBudget = 1000
			}
			toArchive := c.ArchiveMessages(effectiveKeepBudget)
			if len(toArchive) > 0 {
				idx, archErr := a.Conversations.ArchiveChunk(c.ID, toArchive)
				if archErr != nil {
					a.log("warn", "agent", "failed to archive chunk for %s: %v", c.ID, archErr)
				} else {
					c.ChunkCount = idx + 1
				}
			}
			c.Summary = ""
			c.Compact(summary, effectiveKeepBudget)
			return summary, a.Conversations.Save(c)
		}
		a.log("warn", "agent", "server-side compaction failed for %s, falling back to client-side: %v", c.ID, err)
	}

	// Cap the keep budget to 30% of the context window so that compaction
	// always has something to summarize and the retained messages leave room
	// for the next turn's output.
	effectiveKeepBudget := keepTokenBudget
	if cap := contextWindow * 3 / 10; cap < effectiveKeepBudget {
		effectiveKeepBudget = cap
	}
	if effectiveKeepBudget < 1000 {
		effectiveKeepBudget = 1000
	}

	// Calculate the split point: iterate backward from the most recent message,
	// counting tokens of stripped messages until the keep budget is exhausted.
	// Everything before the split point gets summarized; everything after is
	// retained by Compact.
	remaining := effectiveKeepBudget
	splitIdx := 0
	for i := len(c.Messages) - 1; i >= 0; i-- {
		// Match Compact/ArchiveMessages: stripped token count (no tool
		// calls/reasoning/steps/usage) is what will actually be retained.
		tokens := domain.StripForRetention(c.Messages[i]).EstimateTokens()
		if tokens > remaining {
			splitIdx = i + 1
			break
		}
		remaining -= tokens
		splitIdx = i
	}
	if splitIdx < 0 {
		splitIdx = 0
	}

	toCompact := c.Messages[:splitIdx]
	runningSummary := c.Summary

	// Available token budget per pass for message content.
	available := contextWindow - systemReserve - summaryReserve - summaryMaxOut
	if available < 1000 {
		available = 1000
	}

	// Split messages into chunks that fit the per-pass budget. System markers
	// are skipped — their content is already captured in the running summary.
	var chunks [][]domain.Message
	var current []domain.Message
	currentTokens := 0
	for _, m := range toCompact {
		if m.Role == domain.RoleSystem {
			continue
		}
		mt := m.EstimateTokens()
		if currentTokens+mt > available && len(current) > 0 {
			chunks = append(chunks, current)
			current = nil
			currentTokens = 0
		}
		current = append(current, m)
		currentTokens += mt
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}

	// If there's nothing to compact (e.g. only system markers), bail out.
	if len(chunks) == 0 {
		return "", nil
	}

	systemPrompt := "Create a concise handoff checkpoint for the next LLM. Reply with the summary " +
		"only; do not call tools.\n\n" +
		"Capture the user's goal, completed work and decisions, remaining steps and TODO " +
		"status, durable tool effects (what changed and identifying args), relevant " +
		"absolute paths, and any confirmed root cause or constraint. Keep only evidence " +
		"needed to continue safely. Do not copy raw tool output or restate the full " +
		"conversation."

	for _, chunk := range chunks {
		var msgs []ChatMessage
		if runningSummary != "" {
			msgs = append(msgs, ChatMessage{
				Role:    "user",
				Content: "Previous summary of earlier conversation:\n" + runningSummary,
			})
		}
		for _, m := range chunk {
			switch m.Role {
			case domain.RoleUser:
				msgs = append(msgs, ChatMessage{Role: "user", Content: m.Content, Attachments: m.Attachments})
			case domain.RoleAssistant:
				if m.Content == "" && len(m.ToolCalls) == 0 {
					continue
				}
				msgs = append(msgs, ChatMessage{Role: "assistant", Content: m.Content, Reasoning: m.Reasoning, ToolCalls: m.ToolCalls})
				for _, tc := range m.ToolCalls {
					msgs = append(msgs, ChatMessage{Role: "tool", ToolResult: &ToolResult{
						ToolCallID: tc.ID, Name: tc.Name, Content: wrapToolOutput(tc.Name, tc.Output),
						Attachments: tc.OutputAttachments,
					}})
				}
			}
		}
		resp, err := a.completeWithRetry(ctx, adapter, ChatRequest{
			Model:     model,
			System:    systemPrompt,
			Messages:  msgs,
			MaxTokens: summaryMaxOut,
		})
		if err != nil {
			return "", err
		}
		runningSummary = resp.Content
	}

	// Archive the messages that will be dropped by compaction, then compact.
	// Archived chunks preserve full message content (tool calls, reasoning,
	// steps) for scroll-back retrieval in the UI.
	toArchive := c.ArchiveMessages(effectiveKeepBudget)
	if len(toArchive) > 0 {
		idx, err := a.Conversations.ArchiveChunk(c.ID, toArchive)
		if err != nil {
			a.log("warn", "agent", "failed to archive chunk for %s: %v", c.ID, err)
		} else {
			c.ChunkCount = idx + 1
		}
	}
	// Replace the conversation with the final summary marker + recent messages.
	// Clear the old summary first so Compact sets rather than appends.
	c.Summary = ""
	c.Compact(runningSummary, effectiveKeepBudget)
	return runningSummary, a.Conversations.Save(c)
}

func (a *App) updateMessage(c *domain.Conversation, msgID string, fn func(*domain.Message)) {
	for i := range c.Messages {
		if c.Messages[i].ID == msgID {
			fn(&c.Messages[i])
			return
		}
	}
}

func (a *App) hasToolCall(c *domain.Conversation, msgID, callID string) bool {
	for i := range c.Messages {
		if c.Messages[i].ID != msgID {
			continue
		}
		for _, tc := range c.Messages[i].ToolCalls {
			if tc.ID == callID {
				return true
			}
		}
	}
	return false
}

func (a *App) appendToolCall(c *domain.Conversation, msgID string, tc domain.ToolCall) *domain.Conversation {
	for i := range c.Messages {
		if c.Messages[i].ID == msgID {
			c.Messages[i].ToolCalls = append(c.Messages[i].ToolCalls, tc)
			return c
		}
	}
	return c
}

func (a *App) updateToolResult(c *domain.Conversation, msgID, callID string, status domain.ToolCallStatus, output string, outputAttachments []domain.Attachment) *domain.Conversation {
	for i := range c.Messages {
		if c.Messages[i].ID != msgID {
			continue
		}
		updated := false
		for j := range c.Messages[i].ToolCalls {
			if c.Messages[i].ToolCalls[j].ID == callID {
				c.Messages[i].ToolCalls[j].Status = status
				c.Messages[i].ToolCalls[j].Output = output
				c.Messages[i].ToolCalls[j].OutputAttachments = outputAttachments
				updated = true
			}
		}
		// Steps are the durable, chronological rendering source. Keep the
		// corresponding call in sync so a reloaded conversation preserves the
		// completed terminal output and status shown during streaming.
		for stepIndex := range c.Messages[i].Steps {
			if c.Messages[i].Steps[stepIndex].Type != domain.StepToolCalls {
				continue
			}
			for callIndex := range c.Messages[i].Steps[stepIndex].ToolCalls {
				if c.Messages[i].Steps[stepIndex].ToolCalls[callIndex].ID != callID {
					continue
				}
				c.Messages[i].Steps[stepIndex].ToolCalls[callIndex].Status = status
				c.Messages[i].Steps[stepIndex].ToolCalls[callIndex].Output = output
				c.Messages[i].Steps[stepIndex].ToolCalls[callIndex].OutputAttachments = outputAttachments
				updated = true
			}
		}
		if updated {
			return c
		}
	}
	return c
}

func allCodexAccountsLimitedError(reset time.Time) error {
	if reset.IsZero() {
		return fmt.Errorf("all Codex accounts are rate-limited")
	}
	return fmt.Errorf("all Codex accounts are rate-limited. Earliest reset at %s", reset.Format(time.RFC3339))
}

func (a *App) failTurn(run *TurnRun, msgID string, err error) {
	a.log("error", "agent", "turn failed: %s: %v", run.ID, err)
	if c, e := a.Conversations.Get(run.ConversationID); e == nil {
		a.updateMessage(c, msgID, func(m *domain.Message) {
			m.Status = domain.StatusError
			m.Error = err.Error()
		})
		c.Status = "idle"
		_ = a.Conversations.Save(c)
	}
	a.discardQueuedSteer(run)
	a.Bus.Emit(contracts.EventTurnError, contracts.TurnErrorEvent{
		RunID: run.ID, ConversationID: run.ConversationID, Message: err.Error(),
	})
}

func (a *App) failStreamTurn(run *TurnRun, msgID, model string, round streamedTurnRound, err error) {
	a.log("error", "agent", "turn failed: %s: %v", run.ID, err)
	if c, getErr := a.Conversations.Get(run.ConversationID); getErr == nil {
		a.updateMessage(c, msgID, func(message *domain.Message) {
			applyStreamRound(message, model, round)
			message.Status = domain.StatusError
			message.Error = err.Error()
		})
		c.Status = "idle"
		_ = a.Conversations.Save(c)
	}
	a.discardQueuedSteer(run)
	a.Bus.Emit(contracts.EventTurnError, contracts.TurnErrorEvent{
		RunID: run.ID, ConversationID: run.ConversationID, Message: err.Error(),
	})
}

func (a *App) interruptTurn(run *TurnRun, msgID string, round streamedTurnRound, usage ChatUsage, model string) {
	a.log("warn", "agent", "turn interrupted: %s", run.ID)
	if c, e := a.Conversations.Get(run.ConversationID); e == nil {
		a.updateMessage(c, msgID, func(m *domain.Message) {
			if m.Content == "" && m.Reasoning == "" && len(m.Steps) == 0 {
				applyStreamRound(m, model, round)
			} else if model != "" {
				m.Model = model
			}
			m.Status = domain.StatusInterrupted
			if usage != (ChatUsage{}) {
				m.Usage = toDomainUsage(usage)
			}
		})
		c.Status = "idle"
		_ = a.Conversations.Save(c)
	}
	a.discardQueuedSteer(run)
	a.Bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: msgID, Model: model,
		Usage: &contracts.UsageDTO{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens},
	})
}

func (a *App) discardQueuedSteer(run *TurnRun) {
	entry := run.cancelSteerEntry()
	if entry == nil {
		return
	}
	a.Bus.Emit(contracts.EventSteerCancelled, contracts.SteerEvent{
		ConversationID: run.ConversationID, Status: "cancelled", Text: entry.Text,
	})
}

func mergeUsage(a, b ChatUsage) ChatUsage {
	return ChatUsage{
		InputTokens:  a.InputTokens + b.InputTokens,
		OutputTokens: a.OutputTokens + b.OutputTokens,
		CacheRead:    a.CacheRead + b.CacheRead,
		CacheWrite:   a.CacheWrite + b.CacheWrite,
	}
}

func toDomainUsage(u ChatUsage) *domain.Usage {
	if u == (ChatUsage{}) {
		return nil
	}
	return &domain.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CacheRead:    u.CacheRead,
		CacheWrite:   u.CacheWrite,
	}
}

// chatMessages flattens history into the neutral provider shape. The
// current turn's placeholder (asstMsgID) is skipped while still empty.
// modelSupportsVision reports whether the given model on the given provider
// supports image input. Returns true when the model metadata is unknown
// (not in catalog) to preserve backward compatibility — providers will
// reject the image if unsupported, and the reactive error path handles that.
func modelSupportsVision(provider *domain.Provider, model string) bool {
	if provider == nil {
		return true
	}
	m := provider.FindModel(model)
	if m == nil {
		return true
	}
	return m.Vision
}

func chatMessages(c *domain.Conversation, pendingMsgID string, supportsVision bool) []ChatMessage {
	var out []ChatMessage
	for _, m := range c.Messages {
		switch m.Role {
		case domain.RoleUser:
			content := m.Content
			attachments := m.Attachments
			if !supportsVision && hasImageAttachment(attachments) {
				attachments = stripImageAttachments(attachments)
				if content == "" {
					content = imageOmittedPlaceholder
				} else if !containsImageOmissionNote(content) {
					content = content + "\n\n" + imageOmittedPlaceholder
				}
			}
			out = append(out, ChatMessage{Role: "user", Content: content, Attachments: attachments})
		case domain.RoleAssistant:
			if m.ID == pendingMsgID && m.Content == "" && m.Reasoning == "" && len(m.ToolCalls) == 0 {
				continue
			}
			if m.Content == "" && m.Reasoning == "" && len(m.ToolCalls) == 0 {
				continue
			}
			cm := ChatMessage{Role: "assistant", Content: m.Content, Reasoning: m.Reasoning, ToolCalls: m.ToolCalls}
			out = append(out, cm)
			for _, tc := range m.ToolCalls {
				out = append(out, ChatMessage{Role: "tool", ToolResult: &ToolResult{
					ToolCallID: tc.ID, Name: tc.Name, Content: wrapToolOutput(tc.Name, tc.Output),
					Attachments: tc.OutputAttachments,
				}})
			}
		case domain.RoleSystem:
			// folded into the system prompt by buildSystemPrompt
		}
	}
	return out
}

const imageOmittedPlaceholder = "[image content omitted — this model does not support image input]"

func hasImageAttachment(atts []domain.Attachment) bool {
	for _, a := range atts {
		if a.Type == "image" {
			return true
		}
	}
	return false
}

func stripImageAttachments(atts []domain.Attachment) []domain.Attachment {
	filtered := make([]domain.Attachment, 0, len(atts))
	for _, a := range atts {
		if a.Type != "image" {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

func containsImageOmissionNote(content string) bool {
	return strings.Contains(content, "image content omitted")
}
