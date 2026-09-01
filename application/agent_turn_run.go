package application

import (
	"nusashell/contracts"
	"nusashell/domain"
)

func (a *App) runTurn(run *TurnRun, provider *domain.Provider, apiKey, model, effort, asstMsgID string, initialContinuation bool, caps ModelCapabilities) {
	turnLock := a.conversationTurnLock(run.ConversationID)
	turnLock.Lock()

	// Hydration is appended in addTurnMessages on a fresh room (and inside
	// persistCompactedConversation after compaction). The turn loop never
	// splices it. Leading checkpoints from older empty-room workspace picks
	// are relocated only in chatMessages so the provider sees user →
	// hydration without rewriting formed message IDs.
	convID := run.ConversationID
	flushed := false
	defer func() {
		defer func() {
			turnLock.Unlock()
			if flushed {
				a.triggerBackgroundCompletionTurn(convID)
			}
		}()
		run.Cancel()
		// Reject any pending ask_question calls for this run so the
		// tool handler unblocks instead of hanging forever.
		if a.AskQuestions != nil {
			a.AskQuestions.RejectRun(run.ID, "Agent turn ended")
		}
		a.runsMu.Lock()
		leftovers := run.drainRunDone()
		delete(a.runs, run.ID)
		a.runsMu.Unlock()
		if len(leftovers) > 0 {
			if _, err := a.applyRunDoneList(convID, leftovers); err != nil {
				a.log("error", "acp", "flush queued subagent results for %s: %v", convID, err)
			} else {
				flushed = true
			}
		}
		// Finalize any conversation still marked "running" after the turn
		// exited without an explicit terminal state (panic recovered by
		// goSafe, or an early return that skipped failTurn/interruptTurn).
		// Without this the conversation is permanently stuck and the user
		// gets a 409 "conversation is busy" on every new message.
		a.recoverOrphanedTurn(run)
		// Compress the conversation's live journal now that the turn is
		// done, keeping journal.jsonl bounded across long sessions.
		a.archiveJournal(run.ConversationID)
	}()
	a.runTurnChain(run, provider, apiKey, model, effort, asstMsgID, initialContinuation, caps, 0)
}

// runTurnChain executes the turn and, on success, checks the auto-continue
// policy. If open todos remain and the chain budget has not been exhausted,
// it starts the next turn without a user message, injecting the continue
// prompt as a system prompt suffix. The chain stops when:
//   - the turn fails or is interrupted
//   - no open todos remain
//   - background jobs (subagents) are still running
//   - the chain budget is exhausted
//   - the user stops the turn, sends a message, or switches conversations
//     (detected via run.Ctx cancellation)
//
// A plain-text question in the assistant reply does not pause the chain.
// The only user-decision gate is the ask_question tool, which blocks the
// turn until the user answers.
func (a *App) runTurnChain(run *TurnRun, provider *domain.Provider, apiKey, model, effort, asstMsgID string, initialContinuation bool, caps ModelCapabilities, autoContinueIndex int) {
	chainModel := model
	chainEffort := effort
	chainAsstMsgID := asstMsgID
	chainContinuation := initialContinuation
	chainIndex := autoContinueIndex
	for {
		shouldContinue, nextAsstMsgID := a.runSingleTurn(run, provider, apiKey, chainModel, chainEffort, chainAsstMsgID, chainContinuation, caps, chainIndex)
		if !shouldContinue {
			return
		}
		// Abort the chain if the turn was cancelled (user Stop, server
		// shutdown, or conversation switch). The single turn already
		// handled the interrupt; we just exit the chain.
		if run.Ctx.Err() != nil {
			return
		}
		// Abort if a new user turn was started for this conversation
		// (the user sent a message while the chain was between turns).
		if a.activeRunForConversation(run.ConversationID) != nil && a.activeRunForConversation(run.ConversationID).ID != run.ID {
			return
		}
		chainIndex++
		chainAsstMsgID = nextAsstMsgID
		chainContinuation = false
		a.log("info", "agent", "auto-continue chain: starting turn %d for %s (open todos remain)", chainIndex, run.ConversationID)
	}
}

// runSingleTurn executes one complete turn (all tool rounds) and returns
// (shouldAutoContinue, nextAssistantMessageID). The shouldAutoContinue flag
// is true only when the turn succeeded and the auto-continue policy says
// the chain should continue.
func (a *App) runSingleTurn(run *TurnRun, provider *domain.Provider, apiKey, model, effort, asstMsgID string, initialContinuation bool, caps ModelCapabilities, autoContinueIndex int) (bool, string) {

	adapter, conversation, settings, err := a.initializeTurn(run, provider, apiKey, model)
	if err != nil {
		a.failTurn(run, asstMsgID, err)
		return false, ""
	}
	// Vision fallback: when the active model does not support images but
	// the user attached images and configured a vision fallback model,
	// describe each image via the fallback and inject the description as
	// a text attachment on the latest user message. The original image is
	// preserved so a later switch to a vision model can still see it.
	if !caps.Vision && settings.VisionProviderID != "" && settings.VisionModelID != "" {
		conversation = a.enrichWithVisionDescriptions(run.Ctx, conversation, asstMsgID, settings)
	}
	// Audio fallback: same proactive enrichment for audio attachments.
	if !caps.Audio && settings.AudioProviderID != "" && settings.AudioModelID != "" {
		conversation = a.enrichWithAudioDescriptions(run.Ctx, conversation, asstMsgID, settings)
	}
	// Video fallback: same proactive enrichment for video attachments.
	if !caps.Video && settings.VideoProviderID != "" && settings.VideoModelID != "" {
		conversation = a.enrichWithVideoDescriptions(run.Ctx, conversation, asstMsgID, settings)
	}
	toolDefs := a.turnToolDefs(run)
	maxTokens := domain.ResolveMaxOutput(provider, model, settings)
	promptCache := buildPromptCachePolicy(settings, provider, model, run.ConversationID, promptCachePrefixForRun(run))

	// The round loop (stream → persist → execute tools → drain at the
	// boundary → repeat) is the AgentEngine with the AgentConversation
	// rule set (conversation_agent_rules.go): proactive/emergency
	// compaction, partial-stream continuation, steer/subagent drains,
	// repeated-tool guard, and usage accounting all live in the rules.
	pr := a.newConversationRules(run, adapter, conversation, settings, provider, model, effort, asstMsgID, caps, toolDefs, maxTokens, promptCache, initialContinuation)
	if _, runErr := (&AgentEngine{}).Run(run.Ctx, pr.rules(), 0); runErr != nil {
		if !pr.turnEnded {
			a.failTurn(run, pr.messageID(), runErr)
		}
		return false, ""
	}
	if err := a.finishTurn(run, pr.messageID(), model, pr.totalUsageTokens(), pr.contextTokens(), autoContinueIndex); err != nil {
		a.failTurn(run, pr.messageID(), err)
		return false, ""
	}
	a.discardQueuedSteer(run)

	// Compute the auto-continue decision. finishTurn already emitted
	// TurnDone with the decision attached, but we need the raw decision
	// here to decide whether to start the next turn.
	if a.Todos == nil {
		a.sealRound(run, pr.messageID(), pr.roundNumber(), "done", nil, usageDTO(pr.totalUsageTokens()), "")
		return false, ""
	}
	items := a.Todos.Get(run.ConversationID)
	decision := domain.DecideAutoContinue(domain.AutoContinueInput{
		Items:             items,
		AutoContinueIndex: autoContinueIndex,
		MaxAutoContinues:  a.Settings.Get().MaxAutoContinues,
		TurnOK:            true,
		HasConversation:   true,
		HasBackgroundJobs: a.hasPendingRuns(run.ConversationID),
	})
	if !decision.ShouldContinue {
		a.log("info", "agent", "auto-continue chain stopped: %s (open todos: %d)", decision.Reason, decision.OpenTodoCount)
		a.sealRound(run, pr.messageID(), pr.roundNumber(), "done", nil, usageDTO(pr.totalUsageTokens()), "")
		return false, ""
	}
	// Emit an event so the UI can show "Continuing tasks… (N/M)" and insert
	// the auto-continue announcement tool card into the transcript.
	a.Bus.Emit(contracts.EventAutoContinue, contracts.AutoContinueEvent{
		ConversationID: run.ConversationID,
		RunID:          run.ID,
		Decision: contracts.AutoContinueDTO{
			ShouldContinue:   decision.ShouldContinue,
			OpenTodoCount:    decision.OpenTodoCount,
			ContinuesUsed:    decision.ContinuesUsed,
			MaxAutoContinues: decision.MaxAutoContinues,
			Reason:           string(decision.Reason),
		},
		ContinueText: continuePrompt,
	})
	// Append the auto-continue notice on the shared `announcement` tool
	// channel (same mechanism as restart announcements) so the model
	// receives it as harness runtime state, never as user speech — models
	// attribute synthetic user messages to the human regardless of prompt
	// wording. The call is persisted with its result pre-filled, so the
	// model sees it in this turn and in later turns, and the UI renders it
	// as a normal tool card.
	notice := a.autoContinueAnnouncement(decision)
	conv, convErr := a.loadRepo(run.ConversationID)
	if convErr != nil {
		a.log("error", "agent", "auto-continue: failed to get conversation: %v", convErr)
		return false, ""
	}
	if err := conv.Add(domain.RoleAssistant, notice); err != nil {
		a.log("error", "agent", "auto-continue: failed to add announcement: %v", err)
		return false, ""
	}
	if saveErr := conv.Save(); saveErr != nil {
		a.log("error", "agent", "auto-continue: failed to save announcement: %v", saveErr)
		return false, ""
	}
	// Append a fresh assistant message for the next turn.
	nextConv, nextMsgID, err := a.appendTurnAssistant(run.ConversationID)
	if err != nil {
		a.log("error", "agent", "auto-continue: failed to append assistant message: %v", err)
		return false, ""
	}
	_ = nextConv
	run.setMessageID(nextMsgID)
	// Seal the final round of this turn, chaining to the first round of the
	// auto-continue turn so SSE consumers keep streaming without waiting for
	// the next agent.turn.started only.
	a.sealRound(run, pr.messageID(), pr.roundNumber(), "done", &contracts.RoundRef{RunID: run.ID, MessageID: nextMsgID, Round: 1}, usageDTO(pr.totalUsageTokens()), "")
	return true, nextMsgID
}

// usageDTO renders turn usage for the round.done frame.
func usageDTO(u ChatUsage) *contracts.UsageDTO {
	return &contracts.UsageDTO{
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
	}
}

// applyQueuedSteer drains a queued steer and appends it as a real user message
// at the current safe boundary. Returns true when a steer was applied.
func (a *App) applyQueuedSteer(run *TurnRun) (bool, error) {
	entry := run.drainSteer()
	if entry == nil {
		return false, nil
	}
	repo, err := a.loadRepo(run.ConversationID)
	if err != nil {
		run.requeueSteer(entry)
		return false, err
	}
	if err := repo.Add(entry.Message.Role, entry.Message); err != nil {
		run.requeueSteer(entry)
		return false, err
	}
	if err := repo.Save(); err != nil {
		run.requeueSteer(entry)
		return false, err
	}
	a.Bus.Emit(contracts.EventSteerApplied, contracts.SteerEvent{
		ConversationID: run.ConversationID, SteerID: entry.ID, Text: entry.Text, Status: "applied",
	})
	a.log("info", "agent", "steer applied for %s: %s", run.ConversationID, entry.ID)
	return true, nil
}

// applyQueuedRunResults drains finished background runs queued on this
// turn and injects them as synthetic result messages, same boundary as
// steer. Returns true when at least one result was injected. The only
// producer today is ACP subagents (subagent_result); the queue itself is
// the shared push-completion path for any async tool.
func (a *App) applyQueuedRunResults(run *TurnRun) (bool, error) {
	pending := run.drainRunDone()
	if len(pending) == 0 {
		return false, nil
	}
	applied, err := a.applyRunDoneList(run.ConversationID, pending)
	if err != nil {
		run.requeueRunDone(pending[applied:])
		return applied > 0, err
	}
	return applied > 0, nil
}

func (a *App) applyRunDoneList(conversationID string, pending []pendingRunDone) (int, error) {
	applied := 0
	for _, p := range pending {
		if p.Complete == nil {
			applied++
			continue
		}
		if err := p.Complete(conversationID); err != nil {
			return applied, err
		}
		a.untrackPendingRun(conversationID, p.RunID)
		applied++
	}
	return applied, nil
}

// resolveCompactionAdapter returns the adapter, model, and context window to
// use for compaction summarization. When settings.CompactionModel is empty,
// the current chat adapter+model are used as-is. When set, a separate adapter
// is built for the configured model (e.g. a cheaper/faster model). On any
// resolution or factory error, it falls back to the default adapter+model so
// compaction still runs instead of silently skipping.
