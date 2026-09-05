package application

import (
	"nusashell/application/service/toolpresentation"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/text"
	clock "nusashell/pkg/time"
)

func (a *App) persistTurnRound(conversationID, messageID, model string, round streamedTurnRound) error {
	repo, err := a.loadRepo(conversationID)
	if err != nil {
		return err
	}
	conversation := repo.Conversation()

	a.updateMessage(conversation, messageID, func(message *domain.Message) {
		applyStreamRound(message, model, round)
		message.Status = domain.StatusDone
	})

	newToolCalls := make([]domain.ToolCall, 0, len(round.Response.ToolCalls))
	for _, toolCall := range round.Response.ToolCalls {
		found := false
		for i := range conversation.Messages {
			if conversation.Messages[i].ID != messageID {
				continue
			}
			for _, tc := range conversation.Messages[i].ToolCalls {
				if tc.ID == toolCall.ID {
					found = true
					break
				}
			}
		}
		if found {
			continue
		}
		for i := range conversation.Messages {
			if conversation.Messages[i].ID == messageID {
				conversation.Messages[i].ToolCalls = append(conversation.Messages[i].ToolCalls, toolCall)
				break
			}
		}
		newToolCalls = append(newToolCalls, toolCall)
	}
	if len(newToolCalls) > 0 {
		a.updateMessage(conversation, messageID, func(message *domain.Message) {
			message.Steps = append(message.Steps, domain.MessageStep{Type: domain.StepToolCalls, ToolCalls: newToolCalls})
		})
	}
	return repo.Save()
}

func applyStreamRound(message *domain.Message, model string, round streamedTurnRound) {
	if round.Reasoning != "" {
		message.Steps = append(message.Steps, domain.MessageStep{Type: domain.StepReasoning, Content: round.Reasoning})
		message.Reasoning = round.Reasoning
	}
	if content := text.Persistable(round.Content); content != "" {
		message.Steps = append(message.Steps, domain.MessageStep{Type: domain.StepText, Content: content})
		message.Content = content
	}
	message.Model = model
	message.Usage = toDomainUsage(round.Response.Usage)
}

// publishRoundDelta stages a live round delta into the in-memory round
// stream registry (SSE /stream). The registry is process-local and
// drop-tolerant: consumers re-open with after=<seq> to replay missed frames.
func (a *App) publishRoundDelta(runID, messageID string, round int, kind, toolCallID, name, text string) {
	if a.RoundStreams != nil {
		a.RoundStreams.Publish(runID, messageID, round, kind, toolCallID, name, text)
	}
}

func (a *App) publishRoundActivity(runID, messageID string, round int, toolCallID, name, activity string) {
	if a.RoundStreams != nil {
		a.RoundStreams.PublishActivity(runID, messageID, round, toolCallID, name, activity)
	}
}

func (a *App) publishRoundToolStart(runID, messageID string, round int, toolCallID, name, args string, presentation *contracts.ToolPresentationDTO) {
	if a.RoundStreams != nil {
		a.RoundStreams.PublishWithArgsAndPresentation(runID, messageID, round, contracts.RoundDeltaTool, toolCallID, name, "", toolpresentation.ToolArgsRaw(args), presentation)
	}
}

// toolExecResult is one tool's outcome from the concurrent execution phase,
// held until results are persisted in tool-call order.
func (a *App) appendTurnAssistant(conversationID string) (*domain.Conversation, string, error) {
	repo, err := a.loadRepo(conversationID)
	if err != nil {
		return nil, "", err
	}
	next := domain.Message{ID: domain.NewID(domain.IDPrefixMsg), Role: domain.RoleAssistant, CreatedAt: clock.NewTime().Time()}
	if err := repo.Add(domain.RoleAssistant, next); err != nil {
		return nil, "", err
	}
	if err := repo.Save(); err != nil {
		return nil, "", err
	}
	return repo.Conversation(), next.ID, nil
}

func (a *App) finishTurn(run *TurnRun, messageID, model string, usage ChatUsage, contextTokens, autoContinueIndex int) error {
	repo, err := a.loadRepo(run.ConversationID)
	if err != nil {
		return err
	}
	conversation := repo.Conversation()
	conversation.Status = "idle"
	// Record the authoritative provider-measured context fill (last round's
	// input + cached input + output) as the source of truth for the idle
	// badge. Providers that report no usage leave it at zero, and the UI
	// falls back to the heuristic EstimatedTokens.
	if contextTokens > 0 {
		conversation.ContextTokens = int64(contextTokens)
	}
	conversation.Touch()
	if err := repo.Save(); err != nil {
		return err
	}

	// Compute the outer multi-turn auto-continue decision. Only attached
	// when a todo port is configured; failed/cancelled paths omit it.
	var autoContinue *contracts.AutoContinueDTO
	if a.Todos != nil {
		items := a.Todos.Get(run.ConversationID)
		decision := domain.DecideAutoContinue(domain.AutoContinueInput{
			Items:             items,
			AutoContinueIndex: autoContinueIndex,
			MaxAutoContinues:  a.Settings.Get().MaxAutoContinues,
			TurnOK:            true,
			HasConversation:   true,
			HasBackgroundJobs: a.hasPendingRuns(run.ConversationID),
		})
		autoContinue = &contracts.AutoContinueDTO{
			ShouldContinue:   decision.ShouldContinue,
			OpenTodoCount:    decision.OpenTodoCount,
			ContinuesUsed:    decision.ContinuesUsed,
			MaxAutoContinues: decision.MaxAutoContinues,
			Reason:           string(decision.Reason),
		}
	}

	a.Bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: messageID, Model: model,
		Usage: &contracts.UsageDTO{
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite,
		},
		ContextTokens: contextTokens,
		AutoContinue:  autoContinue,
	})
	a.emitFinalTurnDiff(run)
	a.log("info", "agent", "turn finished: %s (in %d / out %d)", run.ID, usage.InputTokens, usage.OutputTokens)

	// Pipeline agent steps are unattended automation, not user rooms.
	if !run.Headless {
		a.recordExperience(conversation, false)
		a.maybeAnnounceTaskMemory(run.ConversationID, conversation)
	}

	return nil
}

// truncateToolError prevents oversized error messages from wasting tokens
// when a provider returns an error that embeds base64 media data (e.g.
// "Failed to load image from data:audio/mpeg;base64,//NkxAAAA..."). Such
// errors can be 1.5MB+ and pollute the conversation history, causing every
// subsequent request to fail with HTTP 400. The error is truncated to a
// reasonable length while preserving the diagnostic prefix.
const maxToolErrorLen = 2000

func truncateToolError(msg string) string {
	return text.TruncateWithNote(msg, maxToolErrorLen)
}
