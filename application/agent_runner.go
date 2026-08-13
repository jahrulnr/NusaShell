package application

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

const maxToolRounds = 8

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
	c.Status = "running"
	if err := a.Conversations.Save(c); err != nil {
		return nil, rpcInternal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{ID: domain.NewID("run"), ConversationID: c.ID, MessageID: asstMsg.ID, Ctx: ctx, Cancel: cancel}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	go a.runTurn(run, provider, apiKey, model, userMsg.ID, asstMsg.ID)
	a.log("info", "agent", "turn started: %s (model %s)", run.ID, model)
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

func (a *App) runTurn(run *TurnRun, provider *domain.Provider, apiKey, model, userMsgID, asstMsgID string) {
	defer func() {
		a.runsMu.Lock()
		delete(a.runs, run.ID)
		a.runsMu.Unlock()
	}()

	adapter, err := a.Factory(run.Ctx, provider, apiKey)
	if err != nil {
		a.failTurn(run, asstMsgID, err)
		return
	}

	c, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		a.failTurn(run, asstMsgID, err)
		return
	}

	settings := a.Settings.Get()
	if settings.CompactionEnabled && c.EstimateTokens() > settings.CompactionThreshold {
		summary, compErr := a.compactConversation(run.Ctx, adapter, c, model)
		if compErr != nil {
			a.log("warn", "agent", "compaction failed for %s: %v", c.ID, compErr)
		} else {
			a.Bus.Emit(contracts.EventCompacted, contracts.CompactedEvent{ConversationID: c.ID, Summary: summary})
			a.log("info", "agent", "compacted conversation %s", c.ID)
		}
		c, _ = a.Conversations.Get(run.ConversationID)
	}

	tools := a.Toolbox.ListTools()
	toolDefs := make([]ToolDef, 0, len(tools))
	for _, t := range tools {
		toolDefs = append(toolDefs, ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}

	a.Bus.Emit(contracts.EventTurnStarted, contracts.TurnStartedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: asstMsgID, Round: 0,
	})

	var totalUsage ChatUsage
	currentMsgID := asstMsgID
	round := 0
	for {
		round++
		a.Bus.Emit(contracts.EventTurnStarted, contracts.TurnStartedEvent{
			RunID: run.ID, ConversationID: run.ConversationID, MessageID: currentMsgID, Round: round,
		})
		var content strings.Builder
		var reasoning strings.Builder
		req := ChatRequest{
			Model:         model,
			System:        buildSystemPrompt(c),
			Messages:      chatMessages(c, currentMsgID),
			Tools:         toolDefs,
			PromptCaching: settings.PromptCaching,
			MaxTokens:     4096,
		}
		resp, streamErr := adapter.Stream(run.Ctx, req,
			func(delta string) {
				content.WriteString(delta)
				a.Bus.Emit(contracts.EventMessageDelta, contracts.MessageDeltaEvent{
					RunID: run.ID, ConversationID: run.ConversationID, MessageID: currentMsgID, Text: delta,
				})
			},
			func(delta string) {
				reasoning.WriteString(delta)
				a.Bus.Emit(contracts.EventReasoningDelta, contracts.ReasoningDeltaEvent{
					RunID: run.ID, ConversationID: run.ConversationID, MessageID: currentMsgID, Text: delta,
				})
			},
		)
		totalUsage = mergeUsage(totalUsage, resp.Usage)
		if streamErr != nil {
			if run.Ctx.Err() != nil {
				a.interruptTurn(run, currentMsgID, content.String(), totalUsage, model)
			} else {
				a.failTurn(run, currentMsgID, streamErr)
			}
			return
		}

		c, _ = a.Conversations.Get(run.ConversationID)
		// DEBUG: check steps loaded from store
		for i := range c.Messages {
			if c.Messages[i].ID == currentMsgID {
			}
		}
		// append steps in temporal order: reasoning → text → tool_calls
		a.updateMessage(c, currentMsgID, func(m *domain.Message) {
			if reasoning.Len() > 0 {
				m.Steps = append(m.Steps, domain.MessageStep{
					Type:    domain.StepReasoning,
					Content: reasoning.String(),
				})
				m.Reasoning = reasoning.String()
			}
			if content.Len() > 0 {
				m.Steps = append(m.Steps, domain.MessageStep{
					Type:    domain.StepText,
					Content: content.String(),
				})
				m.Content = content.String()
			}
			m.Model = model
			m.Usage = toDomainUsage(resp.Usage)
			m.Status = domain.StatusDone
		})
		var roundToolCalls []domain.ToolCall
		for _, tc := range resp.ToolCalls {
			if !a.hasToolCall(c, currentMsgID, tc.ID) {
				c = a.appendToolCall(c, currentMsgID, tc)
				roundToolCalls = append(roundToolCalls, tc)
			}
		}
		if len(roundToolCalls) > 0 {
			a.updateMessage(c, currentMsgID, func(m *domain.Message) {
				m.Steps = append(m.Steps, domain.MessageStep{
					Type:      domain.StepToolCalls,
					ToolCalls: roundToolCalls,
				})
			})
		}
		if err := a.Conversations.Save(c); err != nil {
			a.failTurn(run, currentMsgID, err)
			return
		}
		c2, _ := a.Conversations.Get(run.ConversationID)
		for i := range c2.Messages {
			if c2.Messages[i].ID == currentMsgID {
			}
		}
		for i := range c.Messages {
			if c.Messages[i].ID == currentMsgID {
				for j, s := range c.Messages[i].Steps {
					fmt.Fprintf(os.Stderr, "  step %d: type=%s content=%q toolCalls=%d\n", j, s.Type, s.Content, len(s.ToolCalls))
				}
			}
		}

		if len(resp.ToolCalls) == 0 {
			break
		}
		if round >= maxToolRounds {
			a.log("warn", "agent", "turn %s reached %d tool rounds; stopping", run.ID, maxToolRounds)
			break
		}

		for _, tc := range resp.ToolCalls {
			a.Bus.Emit(contracts.EventToolStarted, contracts.ToolStartedEvent{
				RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: tc.ID, Name: tc.Name, Args: []byte(tc.Args),
			})
			a.log("info", "tools", "tool call: %s", tc.Name)
			output, toolErr := a.Toolbox.Execute(run.Ctx, tc.Name, []byte(tc.Args))
			status := domain.ToolOK
			if toolErr != nil {
				status = domain.ToolFailed
				output = "error: " + toolErr.Error()
			}
			a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
				RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: tc.ID,
				Name: tc.Name, Status: string(status), Output: output,
			})
			c, _ = a.Conversations.Get(run.ConversationID)
			c = a.updateToolResult(c, currentMsgID, tc.ID, status, output)
			if err := a.Conversations.Save(c); err != nil {
				a.failTurn(run, currentMsgID, err)
				return
			}
		}

		// next round streams into a fresh assistant message
		next := domain.Message{
			ID:        domain.NewID("msg"),
			Role:      domain.RoleAssistant,
			CreatedAt: time.Now().UTC(),
		}
		c, _ = a.Conversations.Get(run.ConversationID)
		c.AddMessage(next)
		if err := a.Conversations.Save(c); err != nil {
			a.failTurn(run, currentMsgID, err)
			return
		}
		currentMsgID = next.ID
	}

	c, _ = a.Conversations.Get(run.ConversationID)
	c.Status = "idle"
	c.Touch()
	if err := a.Conversations.Save(c); err != nil {
		a.failTurn(run, asstMsgID, err)
		return
	}

	usage := &contracts.UsageDTO{
		InputTokens:  totalUsage.InputTokens,
		OutputTokens: totalUsage.OutputTokens,
		CacheRead:    totalUsage.CacheRead,
		CacheWrite:   totalUsage.CacheWrite,
	}
	a.Bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: asstMsgID, Model: model, Usage: usage,
	})
	a.log("info", "agent", "turn finished: %s (in %d / out %d)", run.ID, totalUsage.InputTokens, totalUsage.OutputTokens)
}

// compactConversation asks the provider to summarize the history and folds it
// into the conversation, keeping the most recent messages intact.
func (a *App) compactConversation(ctx context.Context, adapter AIProvider, c *domain.Conversation, model string) (string, error) {
	summaryReq := ChatRequest{
		Model: model,
		System: "You summarize chat history for a local AI agent. " +
			"Write a dense bullet summary preserving decisions, facts, code context and open tasks. Keep it under 400 words.",
		Messages:  chatMessages(c, ""),
		MaxTokens: 800,
	}
	resp, err := adapter.Complete(ctx, summaryReq)
	if err != nil {
		return "", err
	}
	c.Compact(resp.Content, 6)
	return resp.Content, a.Conversations.Save(c)
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
