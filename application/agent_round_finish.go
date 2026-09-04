package application

import (
	"context"

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

	// Threshold-based learning review: accumulate turns since the last
	// review and only fire when the threshold is reached. This avoids
	// burning extraction cycles on short "hi/thanks" exchanges. The
	// compaction hook (subscribeCompactionReview) fires independently
	// when a conversation is compacted, so learning still happens for
	// long conversations that compact before reaching the threshold.
	// Pipeline agent steps are unattended automation, not user rooms.
	if !run.Headless {
		a.incrementTurnCounter(conversation.ID)
		// Task-memory announcements: surface fragments relevant to this
		// conversation that are new or changed since they were last
		// delivered, via the shared announcement channel.
		a.maybeAnnounceTaskMemory(run.ConversationID, conversation)
	}

	return nil
}

// incrementTurnCounter bumps the per-conversation turn counter and
// triggers a learning review if the threshold is reached. The threshold
// is read from settings (default 10, 0 disables turn-based review).
// Counters are persisted to disk so they survive server restarts.
func (a *App) incrementTurnCounter(conversationID string) {
	if a.ReviewAgent == nil {
		return
	}
	threshold := a.Settings.Get().LearningReviewThreshold
	if threshold <= 0 {
		return // turn-based review disabled
	}
	a.learningMu.Lock()
	a.turnsSinceReview[conversationID]++
	count := a.turnsSinceReview[conversationID]
	a.learningMu.Unlock()
	// Persist so the counter survives restarts.
	a.saveTurnCounters()
	if count < threshold {
		return
	}
	a.flushLearningReview(conversationID, "threshold")
}

// incrementToolCallCounter bumps the per-conversation tool-call counter and
// triggers a learning review if the skill-nudge threshold is reached. This
// catches skill-worthy patterns in tool-heavy but user-turn-light coding
// sessions that would never reach the turn threshold. The threshold is read
// from settings (default 15, 0 disables tool-based review). Counters are
// persisted to disk so they survive server restarts.
func (a *App) incrementToolCallCounter(conversationID string) {
	if a.ReviewAgent == nil {
		return
	}
	threshold := a.Settings.Get().SkillNudgeInterval
	if threshold <= 0 {
		return // tool-based review disabled
	}
	a.learningMu.Lock()
	a.toolCallsSinceReview[conversationID]++
	count := a.toolCallsSinceReview[conversationID]
	a.learningMu.Unlock()
	a.saveTurnCounters()
	if count < threshold {
		return
	}
	a.flushLearningReview(conversationID, "skill_nudge")
}

// flushLearningReview resets the turn counter for a conversation and
// fires a background LLM review over the recent transcript. Called when
// the threshold is reached or when a compaction event fires (whichever
// comes first). The review uses the conversation's configured model
// (the "global LLM") with a restricted toolset and the review
// prompt. It is fire-and-forget — it never blocks or fails the parent
// turn.
func (a *App) flushLearningReview(conversationID string, reason string) {
	if a.ReviewAgent == nil {
		return
	}
	// Reset the counters on every trigger attempt, including deferred ones.
	// The counters measure "activity since the last trigger attempt"; once an
	// attempt is made (even if rejected by cooldown/in-flight), the activity
	// is acknowledged. Without this reset the counters stay at/above the
	// threshold so every subsequent tool call/turn re-enters this function,
	// flooding the learning log with "review triggered" events for the whole
	// cooldown window. A deferred trigger still leaves the pending flag set
	// inside acquireReview, so activity that arrived while a review was
	// running is picked up by the coalesced follow-up after release.
	a.learningMu.Lock()
	a.turnsSinceReview[conversationID] = 0
	a.toolCallsSinceReview[conversationID] = 0
	a.learningMu.Unlock()
	a.saveTurnCounters()
	// Reserve synchronously before launching the worker. Rejected triggers
	// log "review deferred" (coalesced) inside reserveReviewWithReason; only a
	// trigger that wins the slot logs "review triggered" and launches work.
	if !a.ReviewAgent.reserveReview(conversationID) {
		return
	}
	a.log("info", "learning", "review triggered: conv=%s reason=%s", conversationID, reason)
	a.goSafe("learning", func() {
		if a.Bus != nil {
			a.Bus.Emit(contracts.EventLearningReviewStarted, contracts.LearningReviewEvent{
				ConversationID: conversationID,
				Status:         "started",
				Reason:         reason,
			})
		}
		err := a.ReviewAgent.runReservedReview(context.Background(), conversationID)
		if err != nil {
			a.ReviewAgent.recordReviewError(conversationID, err.Error())
			if a.Bus != nil {
				a.Bus.Emit(contracts.EventLearningReviewError, contracts.LearningReviewEvent{
					ConversationID: conversationID,
					Status:         "error",
				})
			}
			return
		}
		if a.Bus != nil {
			a.Bus.Emit(contracts.EventLearningReviewDone, contracts.LearningReviewEvent{
				ConversationID: conversationID,
				Status:         "done",
				Reason:         reason,
			})
		}
	})
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
