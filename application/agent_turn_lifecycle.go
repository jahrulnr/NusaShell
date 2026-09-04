package application

import (
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/text"
)

func (a *App) updateMessage(c *domain.Conversation, msgID string, fn func(*domain.Message)) {
	for i := range c.Messages {
		if c.Messages[i].ID == msgID {
			fn(&c.Messages[i])
			return
		}
	}
}

func (a *App) updateToolResult(c *domain.Conversation, msgID, callID string, status domain.ToolCallStatus, output string, outputAttachments []domain.Attachment) *domain.Conversation {
	for i := range c.Messages {
		// When msgID is empty, search all messages (used by the async
		// subagent completion path which only knows the tool call ID).
		if msgID != "" && c.Messages[i].ID != msgID {
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

func (a *App) failTurn(run *TurnRun, msgID string, err error) {
	if msgID == "" {
		msgID = run.currentMessageID()
	}
	a.log("error", "agent", "turn failed: %s: %v", run.ID, err)
	if repo, e := a.loadRepo(run.ConversationID); e == nil {
		c := repo.Conversation()
		a.updateMessage(c, msgID, func(m *domain.Message) {
			m.Status = domain.StatusError
			m.Error = err.Error()
		})
		c.Status = "idle"
		_ = repo.Save()
	}
	a.discardQueuedSteer(run)
	a.sealRound(run, msgID, 0, contracts.RoundStateError, nil, nil, err.Error())
	a.Bus.Emit(contracts.EventTurnError, contracts.TurnErrorEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: msgID, Message: err.Error(),
	})
}

func (a *App) failStreamTurn(run *TurnRun, msgID, model string, round streamedTurnRound, err error) {
	if msgID == "" {
		msgID = run.currentMessageID()
	}
	a.log("error", "agent", "turn failed: %s: %v", run.ID, err)
	if repo, getErr := a.loadRepo(run.ConversationID); getErr == nil {
		c := repo.Conversation()
		a.updateMessage(c, msgID, func(message *domain.Message) {
			applyStreamRound(message, model, round)
			message.Status = domain.StatusError
			message.Error = err.Error()
		})
		c.Status = "idle"
		// The provider-measured context fill was not refreshed by this turn
		// (the request failed before usage came back), so it is stale and can
		// massively understate the real conversation size — e.g. 401k shown
		// while the history actually holds ~2M tokens. Clear it so the UI
		// falls back to the server-side EstimatedTokens heuristic instead of
		// showing a misleading number.
		c.ContextTokens = 0
		_ = repo.Save()
	}
	a.discardQueuedSteer(run)
	a.sealRound(run, msgID, 0, contracts.RoundStateError, nil, nil, err.Error())
	a.Bus.Emit(contracts.EventTurnError, contracts.TurnErrorEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: msgID, Message: err.Error(),
	})
}

func (a *App) interruptTurn(run *TurnRun, msgID string, round streamedTurnRound, usage ChatUsage, contextTokens int, model string) {
	a.log("warn", "agent", "turn interrupted: %s", run.ID)
	if repo, e := a.loadRepo(run.ConversationID); e == nil {
		c := repo.Conversation()
		a.updateMessage(c, msgID, func(m *domain.Message) {
			if text.Visible(m.Content) == "" && text.Visible(m.Reasoning) == "" && len(m.Steps) == 0 {
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
		if contextTokens > 0 {
			c.ContextTokens = int64(contextTokens)
		}
		_ = repo.Save()
	}
	a.sealRound(run, msgID, 0, contracts.RoundStateInterrupted, nil, usageDTO(usage), "")
	a.discardQueuedSteer(run)
	a.Bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: msgID, Model: model,
		Usage:         &contracts.UsageDTO{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens},
		ContextTokens: contextTokens,
	})
	a.emitFinalTurnDiff(run)
}

func (a *App) discardQueuedSteer(run *TurnRun) {
	entry := run.cancelSteerEntry()
	if entry == nil {
		return
	}
	a.Bus.Emit(contracts.EventSteerCancelled, contracts.SteerEvent{
		ConversationID: run.ConversationID, SteerID: entry.ID, Text: entry.Text, Status: "cancelled", Reason: contracts.SteerCancelReasonDiscarded,
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

// ModelCapabilities describes which input modalities the active chat model
// accepts. It is resolved once per turn from the provider's model metadata
// and threaded through the turn chain so tool dispatch (read_media)
// and chatMessages can behave correctly without
