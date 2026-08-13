package application

import (
	"context"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func (a *App) handleTurnsStart(req contracts.TurnStartRequest) (any, *contracts.RPCError) {
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
	if c.Status == "running" {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
	}
	provider, apiKey, rpcErr := a.resolveModel(model)
	if rpcErr != nil {
		return nil, rpcErr
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
	c.Model = model
	c.Effort = req.Effort
	c.Status = "running"
	if err := a.Conversations.Save(c); err != nil {
		return nil, rpcInternal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{ID: domain.NewID("run"), ConversationID: c.ID, MessageID: asstMsg.ID, Ctx: ctx, Cancel: cancel}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	go a.runTurn(run, provider, apiKey, model, req.Effort, asstMsg.ID, false)
	a.log("info", "agent", "turn started: %s (model %s)", run.ID, model)
	return contracts.TurnStartResult{RunID: run.ID}, nil
}

// handleTurnsRetry re-runs the last failed assistant message with a different
// model picked by the user. When the failed message has partial content (and
// no tool calls), the partial is frozen as a completed step and the new model
// is asked to continue from where it stopped; otherwise the failed message is
// cleared and re-run from scratch.
func (a *App) handleTurnsRetry(req contracts.TurnRetryRequest) (any, *contracts.RPCError) {
	c, rpcErr := a.getConversation(req.ConversationID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "model is required"}
	}
	if c.Status == "running" {
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

	provider, apiKey, rpcErr := a.resolveModel(model)
	if rpcErr != nil {
		return nil, rpcErr
	}

	failed := &c.Messages[failedIdx]
	continuation := failed.Content != "" || failed.Reasoning != ""
	var targetMsgID string
	if continuation && len(failed.ToolCalls) == 0 {
		failed.Status = domain.StatusDone
		failed.Error = ""
		next := domain.Message{ID: domain.NewID("msg"), Role: domain.RoleAssistant, CreatedAt: time.Now().UTC()}
		c.AddMessage(next)
		targetMsgID = next.ID
	} else {
		failed.Status = domain.StatusDone
		failed.Error = ""
		failed.Content = ""
		failed.Reasoning = ""
		failed.Steps = nil
		failed.ToolCalls = nil
		failed.Usage = nil
		targetMsgID = failed.ID
	}
	c.Model = model
	c.Effort = req.Effort
	c.Status = "running"
	if err := a.Conversations.Save(c); err != nil {
		return nil, rpcInternal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{ID: domain.NewID("run"), ConversationID: c.ID, MessageID: targetMsgID, Ctx: ctx, Cancel: cancel}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	go a.runTurn(run, provider, apiKey, model, req.Effort, targetMsgID, continuation)
	a.log("info", "agent", "turn retried: %s (model %s)", run.ID, model)
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

func (a *App) handleTurnsSteer(req contracts.TurnSteerRequest) (any, *contracts.RPCError) {
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

func (a *App) runTurn(run *TurnRun, provider *domain.Provider, apiKey, model, effort, asstMsgID string, initialContinuation bool) {
	defer func() {
		a.runsMu.Lock()
		delete(a.runs, run.ID)
		a.runsMu.Unlock()
	}()

	adapter, conversation, settings, err := a.initializeTurn(run, provider, apiKey, model)
	if err != nil {
		a.failTurn(run, asstMsgID, err)
		return
	}
	toolDefs := a.toolDefinitions()
	maxTokens := resolveMaxOutput(provider, model, settings)

	a.Bus.Emit(contracts.EventTurnStarted, contracts.TurnStartedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: asstMsgID, Round: 0,
	})

	var totalUsage ChatUsage
	currentMsgID := asstMsgID
	round := 0
	toolRounds := 0
	continuation := initialContinuation
	continuedPartialStream := initialContinuation
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
		roundResult, streamErr := a.streamTurnRound(run, adapter, conversation, currentMsgID, model, effort, toolsForRound, settings, continuation, maxTokens)
		continuation = false
		totalUsage = mergeUsage(totalUsage, roundResult.Response.Usage)
		if streamErr != nil {
			if run.Ctx.Err() != nil {
				a.interruptTurn(run, currentMsgID, roundResult.Content, totalUsage, model)
			} else if !continuedPartialStream && canContinuePartialStream(streamErr, roundResult) {
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
			break
		}

		if err := a.executeTurnTools(run, currentMsgID, roundResult.Response.ToolCalls); err != nil {
			a.failTurn(run, currentMsgID, err)
			return
		}
		toolRounds++

		// Drain any queued steer message at this safe boundary (between tool
		// completion and the next provider round). The steer is appended as a
		// real user message so the provider sees it in the next round's context.
		if entry := run.drainSteer(); entry != nil {
			c, steerErr := a.Conversations.Get(run.ConversationID)
			if steerErr != nil {
				a.failTurn(run, currentMsgID, steerErr)
				return
			}
			c.AddMessage(entry.Message)
			if err := a.Conversations.Save(c); err != nil {
				a.failTurn(run, currentMsgID, err)
				return
			}
			a.Bus.Emit(contracts.EventSteerApplied, contracts.SteerEvent{
				ConversationID: run.ConversationID, SteerID: entry.ID, Text: entry.Text, Status: "applied",
			})
			a.log("info", "agent", "steer applied for %s: %s", run.ConversationID, entry.ID)
			conversation = c
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
	// If a steer was queued but never applied (turn ended without a tool round
	// boundary), cancel it so the frontend clears the queued steer card.
	if run.queuedSteer() != nil {
		run.cancelSteer()
		a.Bus.Emit(contracts.EventSteerCancelled, contracts.SteerEvent{
			ConversationID: run.ConversationID, Status: "cancelled",
		})
	}
}

func canContinuePartialStream(err error, round streamedTurnRound) bool {
	return isRetryableProviderError(err) && len(round.Response.ToolCalls) == 0 && (round.Content != "" || round.Reasoning != "")
}

// resolveMaxOutput picks the per-turn completion token ceiling: the model's
// advertised max output when known, otherwise the global settings default.
func resolveMaxOutput(provider *domain.Provider, model string, settings domain.Settings) int {
	for _, m := range provider.Models {
		if m.ID == model && m.MaxOutput > 0 {
			return m.MaxOutput
		}
	}
	return settings.MaxOutputTokens
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
		m := c.Messages[i]
		// Use stripped token count (no tool calls/reasoning) to match what
		// Compact will actually retain.
		tokens := domain.EstimateTokens(m.Content)
		for _, att := range m.Attachments {
			tokens += domain.EstimateTokens(att.Name) + domain.EstimateTokens(att.Content) + domain.EstimateTokens(att.DataURL)
		}
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
						ToolCallID: tc.ID, Name: tc.Name, Content: tc.Output,
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

func (a *App) updateToolResult(c *domain.Conversation, msgID, callID string, status domain.ToolCallStatus, output string) *domain.Conversation {
	for i := range c.Messages {
		if c.Messages[i].ID != msgID {
			continue
		}
		updated := false
		for j := range c.Messages[i].ToolCalls {
			if c.Messages[i].ToolCalls[j].ID == callID {
				c.Messages[i].ToolCalls[j].Status = status
				c.Messages[i].ToolCalls[j].Output = output
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
				updated = true
			}
		}
		if updated {
			return c
		}
	}
	return c
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
	a.Bus.Emit(contracts.EventTurnError, contracts.TurnErrorEvent{
		RunID: run.ID, ConversationID: run.ConversationID, Message: err.Error(),
	})
}

func (a *App) interruptTurn(run *TurnRun, msgID, content string, usage ChatUsage, model string) {
	a.log("warn", "agent", "turn interrupted: %s", run.ID)
	if c, e := a.Conversations.Get(run.ConversationID); e == nil {
		a.updateMessage(c, msgID, func(m *domain.Message) {
			m.Content = content
			m.Model = model
			m.Status = domain.StatusInterrupted
			if usage != (ChatUsage{}) {
				m.Usage = toDomainUsage(usage)
			}
		})
		c.Status = "idle"
		_ = a.Conversations.Save(c)
	}
	a.Bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: msgID, Model: model,
		Usage: &contracts.UsageDTO{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens},
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
func chatMessages(c *domain.Conversation, pendingMsgID string) []ChatMessage {
	var out []ChatMessage
	for _, m := range c.Messages {
		switch m.Role {
		case domain.RoleUser:
			out = append(out, ChatMessage{Role: "user", Content: m.Content, Attachments: m.Attachments})
		case domain.RoleAssistant:
			if m.ID == pendingMsgID && m.Content == "" && len(m.ToolCalls) == 0 {
				continue
			}
			if m.Content == "" && len(m.ToolCalls) == 0 {
				continue
			}
			cm := ChatMessage{Role: "assistant", Content: m.Content, Reasoning: m.Reasoning, ToolCalls: m.ToolCalls}
			out = append(out, cm)
			for _, tc := range m.ToolCalls {
				out = append(out, ChatMessage{Role: "tool", ToolResult: &ToolResult{
					ToolCallID: tc.ID, Name: tc.Name, Content: tc.Output,
				}})
			}
		case domain.RoleSystem:
			// folded into the system prompt by buildSystemPrompt
		}
	}
	return out
}
