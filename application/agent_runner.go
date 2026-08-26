package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	// Save image/file attachments to disk so file-based tools can access
	// them by absolute path. The path is stored on the attachment and
	// surfaced to the model in the placeholder and read_media result.
	a.saveAttachmentsToDisk(req.ConversationID, attachments)
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
	if c.Status == "running" {
		if a.activeRunForConversation(c.ID) != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
		}
		a.healOrphanedRunningConversation(c)
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
		ID:         domain.NewID("msg"),
		Role:       domain.RoleAssistant,
		CreatedAt:  now,
		ProviderID: provider.ID,
	}
	a.addTurnMessages(c, userMsg, asstMsg)
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
	run := &TurnRun{ID: domain.NewID("run"), ConversationID: c.ID, MessageID: asstMsg.ID, Ctx: turnCtx, Cancel: cancel, ProviderID: provider.ID}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	a.goSafe("agent", func() {
		a.runTurn(run, provider, apiKey, bareModel, req.Effort, asstMsg.ID, false, modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams))
	})
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
	if c.Status == "running" {
		if a.activeRunForConversation(c.ID) != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
		}
		a.healOrphanedRunningConversation(c)
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
		next := domain.Message{ID: domain.NewID("msg"), Role: domain.RoleAssistant, CreatedAt: time.Now().UTC(), ProviderID: provider.ID}
		c.AddMessage(next)
		targetMsgID = next.ID
	} else {
		// Delete the failed message entirely and create a fresh one.
		// Wiping content in-place leaves a ghost "done" message with empty
		// content that shows up as a gap when the UI re-renders from server
		// state on turn completion.
		c.Messages = append(c.Messages[:failedIdx], c.Messages[failedIdx+1:]...)
		next := domain.Message{ID: domain.NewID("msg"), Role: domain.RoleAssistant, CreatedAt: time.Now().UTC(), ProviderID: provider.ID}
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
	run := &TurnRun{ID: domain.NewID("run"), ConversationID: c.ID, MessageID: targetMsgID, Ctx: turnCtx, Cancel: cancel, ProviderID: provider.ID}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	a.goSafe("agent", func() {
		a.runTurn(run, provider, apiKey, bareModel, req.Effort, targetMsgID, continuation, modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams))
	})
	a.log("info", "agent", "turn retried: %s (model %s)", run.ID, bareModel)
	return contracts.TurnStartResult{RunID: run.ID}, nil
}

// shouldAnnounceRestart reports whether a persisted conversation predates
// the current process (was used before the backend restarted) and therefore
// gets a one-shot restart announcement on its next user message. Fresh
// conversations (created after startup) and empty conversations never
// qualify; a zero startedAt (tests, unusual composition) disables it.
func shouldAnnounceRestart(c *domain.Conversation, startedAt time.Time) bool {
	return !startedAt.IsZero() && startedAt.After(c.UpdatedAt) && len(c.Messages) > 0
}

// addTurnMessages appends the user message, an optional restart
// announcement, and the assistant turn message, in that order. The
// announcement decision is taken BEFORE AddMessage because AddMessage
// touches UpdatedAt past startedAt — deciding after would defeat the
// predates-restart check and break the one-shot-per-restart guarantee.
func (a *App) addTurnMessages(c *domain.Conversation, userMsg, asstMsg domain.Message) {
	announce := shouldAnnounceRestart(c, a.startedAt)
	c.AddMessage(userMsg)
	if announce {
		c.AddMessage(a.restartAnnouncement())
	}
	c.AddMessage(asstMsg)
}

// restartAnnouncement builds the synthetic assistant message carrying the
// `announcement` tool call with its result pre-filled. It is persisted
// into the conversation so the model sees it in this turn and in later
// turns (auto-continue), and the UI renders it as a normal tool card.
func (a *App) restartAnnouncement() domain.Message {
	return domain.Message{
		ID:        domain.NewID("msg"),
		Role:      domain.RoleAssistant,
		CreatedAt: time.Now().UTC(),
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.AnnouncementToolCallPrefix + randomNonce(),
			Name:   domain.AnnouncementToolName,
			Args:   "{}",
			Status: domain.ToolOK,
			Output: domain.AnnouncementMessage,
		}},
	}
}

func (a *App) handleTurnsStop(req contracts.TurnStopRequest) (any, *contracts.RPCError) {
	a.runsMu.Lock()
	run, ok := a.runs[req.RunID]
	a.runsMu.Unlock()
	if !ok {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "run not found or already finished"}
	}
	run.Cancel()
	// Reject pending ask_question calls so the tool handler unblocks
	// immediately rather than waiting for the turn-end defer.
	if a.AskQuestions != nil {
		a.AskQuestions.RejectRun(run.ID, "Agent turn interrupted by the user")
	}
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

// healOrphanedRunningConversation checks if a conversation is marked "running"
// but has no active run (orphaned by a crash, panic, or early exit). If so, it
// recovers the conversation to idle so the user is not permanently blocked by
// a 409 "conversation is busy". Returns true when the conversation was healed.
func (a *App) healOrphanedRunningConversation(c *domain.Conversation) bool {
	if c.Status != "running" {
		return false
	}
	if a.activeRunForConversation(c.ID) != nil {
		return false
	}
	if !c.RecoverAbandonedTurn() {
		return false
	}
	a.log("warn", "agent", "healed orphaned running conversation %s (no active run found)", c.ID)
	_ = a.Conversations.Save(c)
	return true
}

// recoverOrphanedTurn finalizes a conversation that is still marked "running"
// after a turn exited without an explicit terminal state (e.g. panic recovered
// by goSafe, or an early return that skipped failTurn/interruptTurn). It
// resets the conversation to idle, marks in-flight assistant messages as
// interrupted, logs the recovery, and emits a turn-error event so the UI
// shows the user something went wrong instead of hanging silently.
func (a *App) recoverOrphanedTurn(run *TurnRun) {
	defer func() {
		if r := recover(); r != nil {
			a.log("error", "agent", "orphan recovery panic for run %s: %v", run.ID, r)
		}
	}()
	c, err := a.Conversations.Get(run.ConversationID)
	if err != nil || c.Status != "running" {
		return
	}
	if !c.RecoverOrphanedTurn(domain.OrphanedTurnError) {
		return
	}
	a.log("error", "agent", "turn %s exited without terminal state, recovered conversation %s", run.ID, c.ID)
	_ = a.Conversations.Save(c)
	a.Bus.Emit(contracts.EventTurnError, contracts.TurnErrorEvent{
		RunID:          run.ID,
		ConversationID: run.ConversationID,
		Message:        domain.OrphanedTurnError,
	})
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

// askPendingEvent maps a domain ask request to the wire event shape shared by
// the live EventAskPending and the agent.ask.pending list RPC.
func askPendingEvent(conversationID, runID, callID string, req domain.AskQuestionRequest) contracts.AskPendingEvent {
	opts := make([]contracts.AskOptionDTO, len(req.Options))
	for i, o := range req.Options {
		opts[i] = contracts.AskOptionDTO{
			ID: o.ID, Label: o.Label, Description: o.Description,
			Default: o.Default, Icon: o.Icon, Image: o.Image,
		}
	}
	return contracts.AskPendingEvent{
		ConversationID: conversationID,
		RunID:          runID,
		ToolCallID:     callID,
		Question:       req.Question,
		Options:        opts,
		AllowFreeText:  req.AllowFreeText,
		MultiSelect:    req.MultiSelect,
	}
}

// handleAskPendingList returns the in-flight ask_question calls for a
// conversation so the UI can rebuild interactive cards after a room switch or
// page reload (the original EventAskPending was missed).
func (a *App) handleAskPendingList(req contracts.AskPendingListRequest) (any, *contracts.RPCError) {
	if a.AskQuestions == nil {
		return contracts.AskPendingListResult{Asks: []contracts.AskPendingEvent{}}, nil
	}
	pending := a.AskQuestions.PendingForConversation(req.ConversationID)
	asks := make([]contracts.AskPendingEvent, 0, len(pending))
	for _, p := range pending {
		asks = append(asks, askPendingEvent(p.ConversationID, p.RunID, p.CallID, p.Req))
	}
	return contracts.AskPendingListResult{Asks: asks}, nil
}

// handleAskAnswer resolves a pending ask_question with the user's answer.
func (a *App) handleAskAnswer(req contracts.AskAnswerRequest) (any, *contracts.RPCError) {
	if a.AskQuestions == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ask_question is not available"}
	}
	if req.RunID == "" || req.ToolCallID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "run_id and tool_call_id are required"}
	}
	answer := domain.AskQuestionAnswer{
		Via:       domain.AskAnswerVia(req.Via),
		OptionIDs: req.OptionIDs,
		Text:      req.Text,
	}
	result, err := a.AskQuestions.Answer(req.RunID, req.ToolCallID, answer)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	// Emit answered event so other UI surfaces can update.
	convID := a.runConversationID(req.RunID)
	a.Bus.Emit(contracts.EventAskAnswered, contracts.AskAnsweredEvent{
		ConversationID: convID,
		RunID:          req.RunID,
		ToolCallID:     req.ToolCallID,
		Answer:         result.Answer,
		Via:            result.Via,
		OptionIDs:      result.OptionIDs,
		Text:           result.Text,
	})
	return contracts.AskAnswerResult{
		OK:        result.OK,
		Answer:    result.Answer,
		Via:       result.Via,
		OptionIDs: result.OptionIDs,
	}, nil
}

// handleAskCancel cancels a pending ask_question (user clicked Cancel).
func (a *App) handleAskCancel(req contracts.AskCancelRequest) (any, *contracts.RPCError) {
	if a.AskQuestions == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ask_question is not available"}
	}
	if req.RunID == "" || req.ToolCallID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "run_id and tool_call_id are required"}
	}
	a.AskQuestions.Cancel(req.RunID, req.ToolCallID, req.Reason)
	convID := a.runConversationID(req.RunID)
	a.Bus.Emit(contracts.EventAskCancelled, contracts.AskCancelledEvent{
		ConversationID: convID,
		RunID:          req.RunID,
		ToolCallID:     req.ToolCallID,
		Reason:         req.Reason,
	})
	return map[string]bool{"ok": true}, nil
}

// runConversationID returns the conversation id for a run id, or "" if the
// run is not found.
func (a *App) runConversationID(runID string) string {
	a.runsMu.Lock()
	defer a.runsMu.Unlock()
	if run, ok := a.runs[runID]; ok {
		return run.ConversationID
	}
	return ""
}

func (a *App) handleTurnsSteer(_ context.Context, req contracts.TurnSteerRequest) (any, *contracts.RPCError) {
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
	a.saveAttachmentsToDisk(req.ConversationID, attachments)
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

func (a *App) runTurn(run *TurnRun, provider *domain.Provider, apiKey, model, effort, asstMsgID string, initialContinuation bool, caps ModelCapabilities) {
	defer func() {
		run.Cancel()
		// Reject any pending ask_question calls for this run so the
		// tool handler unblocks instead of hanging forever.
		if a.AskQuestions != nil {
			a.AskQuestions.RejectRun(run.ID, "Agent turn ended")
		}
		a.runsMu.Lock()
		delete(a.runs, run.ID)
		a.runsMu.Unlock()
		// Finalize any conversation still marked "running" after the turn
		// exited without an explicit terminal state (panic recovered by
		// goSafe, or an early return that skipped failTurn/interruptTurn).
		// Without this the conversation is permanently stuck and the user
		// gets a 409 "conversation is busy" on every new message.
		a.recoverOrphanedTurn(run)
	}()
	a.runTurnChain(run, provider, apiKey, model, effort, asstMsgID, initialContinuation, caps, 0)
}

// runTurnChain executes the turn and, on success, checks the auto-continue
// policy. If open todos remain and the chain budget has not been exhausted,
// it starts the next turn without a user message, injecting the continue
// prompt as a system prompt suffix. The chain stops when:
//   - the turn fails or is interrupted
//   - no open todos remain
//   - the last assistant text ends with a question
//   - the chain budget is exhausted
//   - the user stops the turn, sends a message, or switches conversations
//     (detected via run.Ctx cancellation)
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
	tools := append(a.Toolbox.ListTools(), DispatcherToolInfos()...)
	toolDefs := make([]ToolDef, 0, len(tools))
	for _, tool := range tools {
		toolDefs = append(toolDefs, ToolDef{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	if run.Headless {
		toolDefs = filterACPToolDefs(toolDefs)
	}
	maxTokens := resolveMaxOutput(provider, model, settings)
	promptCache := buildPromptCachePolicy(settings, provider.ID, model, run.ConversationID)

	a.Bus.Emit(contracts.EventTurnStarted, contracts.TurnStartedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: asstMsgID, Round: 0,
	})

	var totalUsage ChatUsage
	// lastUsage holds the most recent round's provider usage. Its
	// ContextTokens() is the authoritative context fill for the whole turn
	// (the final request re-sends the full history), unlike totalUsage which
	// sums per-round tokens for the ↑/↓ display tags.
	var lastUsage ChatUsage
	currentMsgID := asstMsgID
	round := 0
	toolRounds := 0
	continuation := initialContinuation
	continuedPartialStream := initialContinuation
	// Safety net for context overflow. Unlike a bool, a counter lets the
	// loop retry emergency compaction a bounded number of times in one
	// turn, so a compaction that fails to shrink the transcript below the
	// window does not permanently lock this turn out of retrying. Reset
	// per turn (this function is the per-turn entry).
	compactionAttempts := 0
	repeatedGuard := &repeatedToolGuard{limit: settings.RepeatedToolLimit}
	// injectHydration lets the first round create a checkpoint when the current
	// history epoch has none, normally initially or after compaction. Existing
	// checkpoints are reused across normal user messages and steers. The flag is
	// reset after one round so later tool rounds do not repeat the check.
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
		// Pre-API proactive compaction check (mirrors Hermes pre-API pressure
		// check). Between rounds, tool results grow the context. Without this
		// check, context can exceed the window mid-turn and only the emergency
		// compaction (reactive, after a 400 overflow) would fire — too late,
		// too lossy, and the agent's mid-thought work is lost. This check fires
		// proactively before each API call (round > 1; round 1 is covered by
		// initializeTurn), so compaction happens while context is still
		// manageable. Shares the compactionAttempts budget with emergency
		// compaction so the combined per-turn backstop is bounded.
		if settings.CompactionEnabled && round > 1 && compactionAttempts < 3 {
			cw := a.resolveContextWindow(provider, model, settings)
			trigger := compactionTriggerTokens(cw, resolveMaxOutput(provider, model, settings), settings)
			if est := conversation.EstimateTokens(); est > trigger {
				compactionAttempts++
				a.log("info", "agent", "mid-turn compaction for %s round %d: est=%d trigger=%d window=%d",
					run.ID, round, est, trigger, cw)
				compAdapter, compModel, compWindow := a.resolveCompactionAdapter(run.Ctx, adapter, model, cw, settings)
				summary, compErr := a.compactConversation(run.Ctx, compAdapter, conversation, compModel, compWindow, settings)
				if compErr == nil {
					a.Bus.Emit(contracts.EventCompacted, contracts.CompactedEvent{ConversationID: conversation.ID, Summary: summary})
					conversation, _ = a.Conversations.Get(run.ConversationID)
					postEst := conversation.EstimateTokens()
					a.log("info", "agent", "mid-turn compaction done for %s round %d: before=%d after=%d (msgs=%d)",
						run.ID, round, est, postEst, len(conversation.Messages))
					injectHydration = true
				} else {
					a.log("warn", "agent", "mid-turn compaction failed for %s round %d: %v", run.ID, round, compErr)
					a.Bus.Emit(contracts.EventCompactionFailed, contracts.CompactionFailedEvent{ConversationID: conversation.ID, Error: compErr.Error()})
					// Don't fail the turn — let the stream attempt proceed.
					// If it overflows, emergency compaction is the fallback.
				}
			}
		}
		a.Bus.Emit(contracts.EventTurnStarted, contracts.TurnStartedEvent{
			RunID: run.ID, ConversationID: run.ConversationID, MessageID: currentMsgID, Round: round,
		})
		roundResult, streamErr := a.streamTurnRound(run, adapter, conversation, currentMsgID, model, effort, toolsForRound, settings, continuation, maxTokens, injectHydration, promptCache, caps)
		injectHydration = false // only the first round may create a missing checkpoint
		continuation = false
		totalUsage = mergeUsage(totalUsage, roundResult.Response.Usage)
		if roundResult.Response.Usage.ContextTokens() > 0 {
			lastUsage = roundResult.Response.Usage
		}
		if streamErr != nil {
			if run.Ctx.Err() != nil {
				a.interruptTurn(run, currentMsgID, roundResult, totalUsage, lastUsage.ContextTokens(), model)
				return false, ""
			}
			streamErr = a.decorateRateLimitError(provider.ID, streamErr)
			if !continuedPartialStream && isRetryableProviderError(streamErr) && len(roundResult.Response.ToolCalls) == 0 && (roundResult.Content != "" || roundResult.Reasoning != "") {
				// A partial stream must never carry an unconfirmed tool call into the next
				// continuation request. Tools run only after a fully completed round.
				roundResult.Response.ToolCalls = nil
				err = a.persistTurnRound(run.ConversationID, currentMsgID, model, roundResult)
				if err != nil {
					a.failTurn(run, currentMsgID, err)
					return false, ""
				}
				a.log("warn", "ai", "continuing partial provider stream for turn %s", run.ID)
				conversation, currentMsgID, err = a.appendTurnAssistant(run.ConversationID)
				if err != nil {
					a.failTurn(run, currentMsgID, err)
					return false, ""
				}
				continuation = true
				continuedPartialStream = true
				// This replaces the interrupted provider attempt; it is not an
				// additional tool round and must not reduce the user's tool budget.
				round--
				continue
			} else if compactionAttempts < 3 && isContextOverflowError(streamErr) {
				// Emergency compaction safety net: the conversation exceeded the
				// model's context window (input + max_output > window). This can
				// happen when the estimate is inaccurate or compaction was
				// disabled. Force compaction once, then retry the round.
				cw := a.resolveContextWindow(provider, model, settings)
				trigger := compactionTriggerTokens(cw, resolveMaxOutput(provider, model, settings), settings)
				preEmg := conversation.EstimateTokens()
				if !shouldEmergencyCompact(streamErr, preEmg, trigger) {
					a.log("warn", "agent", "overflow-like 400 for turn %s but est=%d <= trigger=%d; skipping emergency compaction", run.ID, preEmg, trigger)
					a.failStreamTurn(run, currentMsgID, model, roundResult, streamErr)
					return false, ""
				}
				compactionAttempts++
				a.log("warn", "agent", "context overflow for turn %s (est=%d trigger=%d), forcing emergency compaction", run.ID, preEmg, trigger)
				compAdapter, compModel, compWindow := a.resolveCompactionAdapter(run.Ctx, adapter, model, cw, settings)
				summary, compErr := a.compactConversation(run.Ctx, compAdapter, conversation, compModel, compWindow, settings)
				if compErr == nil {
					a.Bus.Emit(contracts.EventCompacted, contracts.CompactedEvent{ConversationID: conversation.ID, Summary: summary})
					conversation, _ = a.Conversations.Get(run.ConversationID)
					postEmg := conversation.EstimateTokens()
					a.log("info", "agent", "emergency compaction done for %s: before=%d after=%d (msgs=%d)",
						conversation.ID, preEmg, postEmg, len(conversation.Messages))
					injectHydration = true // compaction strips the checkpoint; recreate it
					round--
					continue
				}
				a.log("warn", "agent", "emergency compaction failed for %s: %v", conversation.ID, compErr)
				a.Bus.Emit(contracts.EventCompactionFailed, contracts.CompactionFailedEvent{ConversationID: conversation.ID, Error: compErr.Error()})
				a.failStreamTurn(run, currentMsgID, model, roundResult, streamErr)
			} else {
				a.failStreamTurn(run, currentMsgID, model, roundResult, streamErr)
			}
			return false, ""
		}

		if toolRounds >= settings.MaxToolRounds && len(roundResult.Response.ToolCalls) > 0 {
			a.log("warn", "agent", "turn %s requested a tool after reaching the %d-round limit", run.ID, settings.MaxToolRounds)
			roundResult.Response.ToolCalls = nil
		}
		if err := a.persistTurnRound(run.ConversationID, currentMsgID, model, roundResult); err != nil {
			a.failTurn(run, currentMsgID, err)
			return false, ""
		}

		if len(roundResult.Response.ToolCalls) == 0 {
			// The model finished without requesting tools. Before exiting the
			// turn, drain any queued steer — if one is pending, inject it and
			// continue the loop so the model gets a new round to respond to it
			// instead of silently dropping the steer.
			applied, steerConv, steerErr := a.applyQueuedSteer(run, currentMsgID)
			if steerErr != nil {
				a.failTurn(run, currentMsgID, steerErr)
				return false, ""
			}
			if applied {
				conversation = steerConv
				conversation, currentMsgID, err = a.appendTurnAssistant(run.ConversationID)
				if err != nil {
					a.failTurn(run, currentMsgID, err)
					return false, ""
				}
				injectHydration = true // allow a missing post-compaction checkpoint
				continue
			}
			break
		}

		if err := a.executeTurnTools(run, currentMsgID, roundResult.Response.ToolCalls, caps, settings); err != nil {
			if run.Ctx.Err() != nil {
				a.interruptTurn(run, currentMsgID, roundResult, totalUsage, lastUsage.ContextTokens(), model)
				return false, ""
			}
			a.failTurn(run, currentMsgID, err)
			return false, ""
		}
		toolRounds++

		// Repeated-tool-call guard: if the model calls the same set of tools
		// with the same arguments multiple rounds in a row without producing
		// any text, it is stuck in a loop. This catches both single-tool and
		// parallel-tool loops (e.g. GPT-5.6 Luna re-enabling 6 MCP plugins
		// every round). Break the cycle by stripping tools for the next round
		// so the model is forced to respond with text.
		// RepeatedToolLimit = 0 disables the guard.
		if repeatedGuard.check(roundResult.Response.ToolCalls, roundResult.Content) {
			a.log("warn", "agent", "turn %s: detected repeated tool round (%dx identical set), forcing text-only round", run.ID, repeatedGuard.limit)
			toolRounds = settings.MaxToolRounds // strip tools for next round
		}

		// Drain any queued steer message at this safe boundary (between tool
		// completion and the next provider round). The steer is appended as a
		// real user message so the provider sees it in the next round's context.
		applied, steerConv, steerErr := a.applyQueuedSteer(run, currentMsgID)
		if steerErr != nil {
			a.failTurn(run, currentMsgID, steerErr)
			return false, ""
		}
		if applied {
			conversation = steerConv
			injectHydration = true // allow a missing post-compaction checkpoint
		}

		conversation, currentMsgID, err = a.appendTurnAssistant(run.ConversationID)
		if err != nil {
			a.failTurn(run, currentMsgID, err)
			return false, ""
		}
	}

	if err := a.finishTurn(run, asstMsgID, model, totalUsage, lastUsage.ContextTokens(), autoContinueIndex); err != nil {
		a.failTurn(run, asstMsgID, err)
		return false, ""
	}
	a.discardQueuedSteer(run)

	// Compute the auto-continue decision. finishTurn already emitted
	// TurnDone with the decision attached, but we need the raw decision
	// here to decide whether to start the next turn.
	if a.Todos == nil {
		return false, ""
	}
	items := a.Todos.Get(run.ConversationID)
	conv, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return false, ""
	}
	lastText := lastAssistantText(conv, asstMsgID)
	decision := domain.DecideAutoContinue(domain.AutoContinueInput{
		Items:             items,
		AutoContinueIndex: autoContinueIndex,
		MaxAutoContinues:  a.Settings.Get().MaxAutoContinues,
		TurnOK:            true,
		HasConversation:   true,
		TurnText:          lastText,
		HasBackgroundJobs: a.hasPendingSubagents(run.ConversationID),
	})
	if !decision.ShouldContinue {
		a.log("info", "agent", "auto-continue chain stopped: %s (open todos: %d)", decision.Reason, decision.OpenTodoCount)
		return false, ""
	}
	// Emit an event so the UI can show "Continuing tasks… (N/M)" and insert
	// the synthetic continue user message into the transcript.
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
	// Append the auto-continue prompt as a synthetic user message so the
	// model sees a user turn (not just a system prompt suffix). Without this,
	// after compaction the conversation history ends with an assistant message
	// and the model interprets the missing user message as an empty message
	// from the user. The message is marked AutoContinue so the UI can style
	// it differently from real user messages.
	continueMsg := domain.Message{
		ID:           domain.NewID("msg"),
		Role:         domain.RoleUser,
		Content:      continuePrompt,
		CreatedAt:    time.Now().UTC(),
		Status:       domain.StatusDone,
		AutoContinue: true,
	}
	conv, convErr := a.Conversations.Get(run.ConversationID)
	if convErr != nil {
		a.log("error", "agent", "auto-continue: failed to get conversation: %v", convErr)
		return false, ""
	}
	conv.AddMessage(continueMsg)
	if saveErr := a.Conversations.Save(conv); saveErr != nil {
		a.log("error", "agent", "auto-continue: failed to save user message: %v", saveErr)
		return false, ""
	}
	// Append a fresh assistant message for the next turn.
	nextConv, nextMsgID, err := a.appendTurnAssistant(run.ConversationID)
	if err != nil {
		a.log("error", "agent", "auto-continue: failed to append assistant message: %v", err)
		return false, ""
	}
	_ = nextConv
	return true, nextMsgID
}

// applyQueuedSteer drains a queued steer and appends it as a real user message
// at the current safe boundary. Returns (true, updatedConversation, nil) when a
// steer was applied, (false, nil, nil) when no steer was queued.
func (a *App) applyQueuedSteer(run *TurnRun, _ string) (bool, *domain.Conversation, error) {
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

// shouldContinueFailedTurn reports whether a retry should freeze partial
// output and ask the new model to continue. Tool-bearing failures are always
// restarted from scratch so a leftover continuation flag cannot skip tool
// work or consume the mid-stream continuation budget.
func shouldContinueFailedTurn(failed domain.Message) bool {
	return domain.ShouldContinueFailedTurn(failed)
}

// compactionTriggerTokens is the estimated-token watermark that starts
// compaction. When CompactionThreshold is 0 (auto, the default), compaction
// triggers at 80% of the model's available input budget (contextWindow minus
// maxOutput) — so a 256k model with 64k output compacts at ~122k input, not
// ~205k. When CompactionThreshold is non-zero, it is used as the trigger but
// still capped at 80% of the available budget so a high threshold cannot wait
// until the next turn already overflows.
func compactionTriggerTokens(contextWindow, maxOutput int, settings domain.Settings) int {
	return domain.CompactionTriggerTokens(contextWindow, maxOutput, settings)
}

// resolveCompactionAdapter returns the adapter, model, and context window to
// use for compaction summarization. When settings.CompactionModel is empty,
// the current chat adapter+model are used as-is. When set, a separate adapter
// is built for the configured model (e.g. a cheaper/faster model). On any
// resolution or factory error, it falls back to the default adapter+model so
// compaction still runs instead of silently skipping.
func (a *App) resolveCompactionAdapter(ctx context.Context, defaultAdapter ProviderContext, defaultModel string, defaultWindow int, settings domain.Settings) (ProviderContext, string, int) {
	compModel := strings.TrimSpace(settings.CompactionModel)
	if compModel == "" {
		return defaultAdapter, defaultModel, defaultWindow
	}
	provider, bareModel, apiKey, rpcErr := a.resolveModel(compModel)
	if rpcErr != nil || provider == nil {
		a.log("warn", "agent", "compaction model %q could not be resolved, falling back to chat model: %v", compModel, rpcErr)
		return defaultAdapter, defaultModel, defaultWindow
	}
	adapter, err := a.Factory(ctx, provider, apiKey)
	if err != nil {
		a.log("warn", "agent", "compaction model %q adapter build failed, falling back to chat model: %v", compModel, err)
		return defaultAdapter, defaultModel, defaultWindow
	}
	pc := NewProviderContext(provider, adapter)
	window := a.resolveContextWindow(provider, bareModel, settings)
	a.log("info", "agent", "compaction using override model %s (window=%d)", compModel, window)
	return pc, bareModel, window
}

// resolveMaxOutput picks the per-turn completion token ceiling. The model's
// advertised max output is used when known, but capped by the global settings
// default — the setting acts as a ceiling, not just a fallback. This prevents
// sending absurdly high max_tokens values (e.g. 1M for models that advertise
// it) which cause credit/balance rejections on gateways like OpenRouter.
func resolveMaxOutput(provider *domain.Provider, model string, settings domain.Settings) int {
	return domain.ResolveMaxOutput(provider, model, settings)
}

// effectiveContextWindow picks the window shown/used for a model: the
// model-advertised window wins (catalog value); the configured max_input_tokens
// is only a fallback for models that do not advertise one. Capping catalog
// models to the global setting confused users ("1M model, why 200k?").
func effectiveContextWindow(modelWindow, maxInputTokens int) int {
	return domain.EffectiveContextWindow(modelWindow, maxInputTokens)
}

// resolveContextWindow picks the effective context window for compaction
// decisions: min(model context, max_input_tokens) when both are known, or the
// configured max_input_tokens fallback when the model does not advertise one.
func (a *App) resolveContextWindow(provider *domain.Provider, model string, settings domain.Settings) int {
	return domain.ResolveContextWindow(provider, model, settings)
}

const (
	compactionKeepTokenBudget = 64000 // retained recent messages token budget
	compactionSummaryMaxOut   = 64000 // default max_output_tokens for compaction summarization
	compactionSystemReserve   = 300   // system prompt + framing overhead
	// compactionSummaryMinChars is the minimum summary length for the quality
	// guard. Summaries shorter than this are considered failed and retried.
	compactionSummaryMinChars = 200
	// compactionSummaryMaxRetries is the max number of retry attempts when the
	// summary is too short. Each retry doubles the max_output_tokens budget.
	compactionSummaryMaxRetries = 2
	// compactionMaxToolCallChars caps a single tool call's args/output when
	// building the compaction input. Tool results can be unbounded (grep over
	// huge lines, mcp_call, file_write content), and one oversized call must
	// still fit inside the compaction model's context window — otherwise the
	// summarization pass overflows and compaction fails (the turn then dies
	// with a context-overflow 400). Truncated payloads keep an omission marker
	// so the summary model knows content was dropped.
	compactionMaxToolCallChars = 200_000
)

// compactionSummaryToolName is the single tool advertised to the compaction
// model. Instead of relying on resp.Content (which competes with reasoning
// tokens on reasoning models), the model calls summary(text="...") and the
// summary is extracted from the tool call arguments. This decouples the
// summary from the reasoning budget: reasoning tokens are spent on thinking,
// the tool call argument carries the actual checkpoint text.
const compactionSummaryToolName = "summary"

// compactionSummaryToolDef is the tool definition for the summary() tool.
var compactionSummaryToolDef = ToolDef{
	Name:        compactionSummaryToolName,
	Description: "Submit the conversation handoff summary. Call this exactly once with the complete checkpoint text.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The complete handoff checkpoint summary for the next LLM.",
			},
		},
		"required": []string{"text"},
	},
}

// extractCompactionSummary extracts the summary from the model response. It
// prefers the summary() tool call (which carries the text in args, separate
// from reasoning tokens) and falls back to resp.Content for non-tool-calling
// models or when the model ignores the tool and replies as text.
func extractCompactionSummary(resp ChatResponse) string {
	for _, tc := range resp.ToolCalls {
		if tc.Name != compactionSummaryToolName {
			continue
		}
		var parsed struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(tc.Args), &parsed); err == nil && strings.TrimSpace(parsed.Text) != "" {
			return parsed.Text
		}
	}
	return resp.Content
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

func (a *App) compactConversation(ctx context.Context, adapter ProviderContext, c *domain.Conversation, model string, contextWindow int, settings domain.Settings) (string, error) {
	if len(c.Messages) <= 1 {
		return "", nil
	}

	effectiveKeepBudget := compactionKeepTokenBudget
	if cap := contextWindow * 3 / 10; cap < effectiveKeepBudget {
		effectiveKeepBudget = cap
	}
	if effectiveKeepBudget < 1000 {
		effectiveKeepBudget = 1000
	}

	// Calculate the split point: iterate backward from the most recent message,
	// counting tokens until the keep budget is exhausted. Everything before the
	// split point gets summarized; everything after is retained by Compact.
	// Token cost is computed from the original message (including tool calls)
	// so tool-heavy messages are not undercounted — matching Compact/ArchiveMessages.
	remaining := effectiveKeepBudget
	splitIdx := 0
	for i := len(c.Messages) - 1; i >= 0; i-- {
		tokens := c.Messages[i].EstimateTokens()
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

	toCompact := filterHydrationDomainMessages(c.Messages[:splitIdx])
	runningSummary := c.Summary

	remainingMsgs := make([]domain.Message, 0, len(toCompact))
	for _, m := range toCompact {
		if m.Role != domain.RoleSystem {
			remainingMsgs = append(remainingMsgs, m)
		}
	}
	if len(remainingMsgs) == 0 {
		return "", nil
	}

	systemPrompt := compactionPrompt
	if todoCtx := a.compactionTodoContext(c.ID); todoCtx != "" {
		systemPrompt += "\n\nCurrent state to preserve in the summary:\n" + todoCtx
	}

	summaryMaxOut := compactionSummaryMaxOut
	if settings.CompactionSummaryMaxTokens > 0 {
		summaryMaxOut = settings.CompactionSummaryMaxTokens
	}
	summaryMinChars := compactionSummaryMinChars
	if settings.CompactionSummaryMinChars > 0 {
		summaryMinChars = settings.CompactionSummaryMinChars
	}
	// Clamp the summary budget to the context window so the doubled retry
	// budget never exceeds what the model can accept. Reserve system overhead
	// and a minimum input floor so the model still has room for the chunk.
	maxBudget := contextWindow - compactionSystemReserve
	if maxBudget < 1000 {
		maxBudget = 1000
	}

	for len(remainingMsgs) > 0 {
		available := compactionPassAvailable(contextWindow, runningSummary, summaryMaxOut)
		chunk, rest := takeCompactionChunk(remainingMsgs, available)
		remainingMsgs = rest
		if len(chunk) == 0 {
			break
		}
		// Cap each tool call's args/output so a single oversized call still
		// fits the compaction model's window. The cap shrinks with the pass
		// budget so small-window compaction models stay safe too.
		toolCap := compactionMaxToolCallChars
		if a2 := available * 2; a2 < toolCap {
			toolCap = a2
		}
		var msgs []ChatMessage
		if runningSummary != "" {
			msgs = append(msgs, ChatMessage{
				Role:    "user",
				Content: compactionSummaryPrefix + runningSummary,
			})
		}
		for _, m := range chunk {
			switch m.Role {
			case domain.RoleUser:
				// Media/file attachments are stripped from the compaction
				// input and replaced with a text note. Compaction models are
				// often not vision/audio-capable, and providers reject the
				// request outright (e.g. OpenRouter HTTP 404 "No endpoints
				// found that support image input") — which made compaction
				// fail and the turn die with a context-overflow 400. The
				// summary only needs to know that content was attached.
				content := m.Content
				if note := compactionAttachmentNote(m); note != "" {
					if content == "" {
						content = note
					} else {
						content = content + "\n\n" + note
					}
				}
				msgs = append(msgs, ChatMessage{Role: "user", Content: content})
			case domain.RoleAssistant:
				if m.Content == "" && len(m.ToolCalls) == 0 {
					continue
				}
				calls := capCompactionToolCalls(m.ToolCalls, toolCap)
				msgs = append(msgs, ChatMessage{Role: "assistant", Content: m.Content, Reasoning: m.Reasoning, ToolCalls: calls})
				for _, tc := range calls {
					msgs = append(msgs, ChatMessage{Role: "tool", ToolResult: &ToolResult{
						ToolCallID: tc.ID, Name: tc.Name, Content: providerToolContent(tc.Name, tc.Output),
					}})
				}
			}
		}
		// Quality guard: retry up to compactionSummaryMaxRetries times when
		// the summary is too short. Each retry doubles the max_output_tokens
		// budget so a reasoning model has more room for content after
		// reasoning. The budget is clamped to the context window so it never
		// exceeds what the model can accept. If all retries fail, return an
		// error so the caller emits EventCompactionFailed and the user cannot
		// continue until compaction succeeds (retry re-enters compaction).
		passBudget := summaryMaxOut
		if passBudget > maxBudget {
			passBudget = maxBudget
		}
		var summary string
		var lastErr error
		for attempt := 0; attempt <= compactionSummaryMaxRetries; attempt++ {
			resp, err := a.completeWithRetry(ctx, adapter, ChatRequest{
				Model:     model,
				System:    systemPrompt,
				Messages:  msgs,
				Tools:     []ToolDef{compactionSummaryToolDef},
				MaxTokens: passBudget,
			})
			if err != nil {
				lastErr = err
				a.log("warn", "agent", "compaction pass %d failed for %s: %v", attempt+1, c.ID, err)
				continue
			}
			summary = extractCompactionSummary(resp)
			if len(strings.TrimSpace(summary)) >= summaryMinChars {
				runningSummary = summary
				break
			}
			nextBudget := passBudget * 2
			if nextBudget > maxBudget {
				nextBudget = maxBudget
			}
			a.log("warn", "agent", "compaction pass %d produced short summary (%d chars, min %d) for %s, retrying with budget %d",
				attempt+1, len(summary), summaryMinChars, c.ID, nextBudget)
			passBudget = nextBudget
			lastErr = fmt.Errorf("compaction summary too short (%d chars, min %d) after %d attempts",
				len(summary), summaryMinChars, attempt+1)
		}
		if len(strings.TrimSpace(summary)) < summaryMinChars {
			return "", fmt.Errorf("compaction failed: summary too short after %d retries (last=%d chars, min=%d): %w",
				compactionSummaryMaxRetries, len(summary), summaryMinChars, lastErr)
		}
		runningSummary = summary
	}

	return runningSummary, a.persistCompactedConversation(c, runningSummary, effectiveKeepBudget)
}

// compactionAttachmentNote renders a short text note for message attachments
// so the compaction summary knows media/files were attached without
// receiving their bytes (see the stripping comment in compactConversation).
func compactionAttachmentNote(m domain.Message) string {
	if len(m.Attachments) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.Attachments))
	for _, att := range m.Attachments {
		label := att.Name
		if label == "" {
			label = att.Type
		}
		if label == "" {
			label = "attachment"
		}
		if att.Type != "" && !strings.Contains(label, att.Type) {
			label += " (" + att.Type + ")"
		}
		names = append(names, label)
	}
	return "[attachments: " + strings.Join(names, ", ") + "]"
}

// capCompactionToolCalls returns a copy of the tool calls whose args and
// output are truncated to capChars with an omission marker, so one oversized
// call (e.g. a grep over huge lines, a 10MB file_write content) cannot exceed
// the compaction model's context window.
func capCompactionToolCalls(calls []domain.ToolCall, capChars int) []domain.ToolCall {
	if capChars <= 0 {
		return calls
	}
	out := make([]domain.ToolCall, 0, len(calls))
	for _, tc := range calls {
		if len(tc.Args) > capChars {
			tc.Args = truncateCompactionText(tc.Args, capChars)
		}
		if len(tc.Output) > capChars {
			tc.Output = truncateCompactionText(tc.Output, capChars)
		}
		out = append(out, tc)
	}
	return out
}

// truncateCompactionText keeps the first n runes of s and appends an
// omission marker with the number of characters dropped, so the summary
// model can tell the payload was cut.
func truncateCompactionText(s string, n int) string {
	omitted := len(s) - n
	head := []rune(s)
	if len(head) > n {
		head = head[:n]
	}
	return string(head) + fmt.Sprintf("\n\n[truncated: %d chars omitted]", omitted)
}

const compactionSummaryPrefix = "Previous summary of earlier conversation:\n"

// compactionPassAvailable is the per-pass token budget for message content.
// The running summary grows across passes, so later chunks shrink to leave
// room for it instead of using a one-shot 2000-token reserve.
func compactionPassAvailable(contextWindow int, runningSummary string, summaryMaxOut int) int {
	summaryTokens := domain.EstimateTokens(runningSummary)
	if runningSummary != "" {
		summaryTokens += domain.EstimateTokens(compactionSummaryPrefix)
	}
	available := contextWindow - compactionSystemReserve - summaryTokens - summaryMaxOut
	if available < 1000 {
		return 1000
	}
	return available
}

// takeCompactionChunk takes the longest prefix of msgs whose token estimate
// fits in available. A single oversized message is still taken so compaction
// cannot stall. System markers should already have been stripped.
func takeCompactionChunk(msgs []domain.Message, available int) (chunk, rest []domain.Message) {
	var current []domain.Message
	currentTokens := 0
	for i, m := range msgs {
		mt := m.EstimateTokens()
		if currentTokens+mt > available && len(current) > 0 {
			return current, msgs[i:]
		}
		current = append(current, m)
		currentTokens += mt
	}
	return current, nil
}

// persistCompactedConversation archives dropped messages (without hydration
// checkpoints), strips hydration from the live transcript, then applies Compact.
func (a *App) persistCompactedConversation(c *domain.Conversation, summary string, keepBudget int) error {
	c.Messages = filterHydrationDomainMessages(c.Messages)
	toArchive := c.ArchiveMessages(keepBudget)
	if len(toArchive) > 0 {
		idx, err := a.Conversations.ArchiveChunk(c.ID, toArchive)
		if err != nil {
			a.log("warn", "agent", "failed to archive chunk for %s: %v", c.ID, err)
		} else {
			c.ChunkCount = idx + 1
		}
	}
	c.Summary = ""
	c.Compact(summary, keepBudget)
	return a.Conversations.Save(c)
}

// compactionTodoContext renders the live user goal and open TODOs for the
// conversation into a compact block appended to the compaction prompt. The
// hydration checkpoint (which carries this state) is stripped before
// summarization, so without this the summary's "remaining steps and TODO
// status" would rely on the model's memory of what it wrote. Goal and item
// content are truncated so a large goal cannot blow the compaction budget.
// Returns "" when no todo store is configured or there is nothing to report.
func (a *App) compactionTodoContext(conversationID string) string {
	if a.Todos == nil {
		return ""
	}
	var sb strings.Builder
	if brief := strings.TrimSpace(a.Todos.GetBrief(conversationID)); brief != "" {
		sb.WriteString("User goal: ")
		sb.WriteString(truncate(brief, 2000))
		sb.WriteString("\n")
	}
	var open []domain.TodoItem
	for _, it := range a.Todos.Get(conversationID) {
		if it.Status != domain.TodoCompleted {
			open = append(open, it)
		}
	}
	if len(open) > 0 {
		sb.WriteString("Open TODOs:\n")
		for _, it := range open {
			sb.WriteString("- [")
			sb.WriteString(string(it.Status))
			sb.WriteString("] ")
			sb.WriteString(truncate(it.Content, 300))
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

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
		// The provider-measured context fill was not refreshed by this turn
		// (the request failed before usage came back), so it is stale and can
		// massively understate the real conversation size — e.g. 401k shown
		// while the history actually holds ~2M tokens. Clear it so the UI
		// falls back to the server-side EstimatedTokens heuristic instead of
		// showing a misleading number.
		c.ContextTokens = 0
		_ = a.Conversations.Save(c)
	}
	a.discardQueuedSteer(run)
	a.Bus.Emit(contracts.EventTurnError, contracts.TurnErrorEvent{
		RunID: run.ID, ConversationID: run.ConversationID, Message: err.Error(),
	})
}

func (a *App) interruptTurn(run *TurnRun, msgID string, round streamedTurnRound, usage ChatUsage, contextTokens int, model string) {
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
		if contextTokens > 0 {
			c.ContextTokens = int64(contextTokens)
		}
		_ = a.Conversations.Save(c)
	}
	a.discardQueuedSteer(run)
	a.Bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: msgID, Model: model,
		Usage:         &contracts.UsageDTO{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens},
		ContextTokens: contextTokens,
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

// ModelCapabilities describes which input modalities the active chat model
// accepts. It is resolved once per turn from the provider's model metadata
// and threaded through the turn chain so tool dispatch (read_media)
// and chatMessages can behave correctly without
// re-querying the provider on every round.
type ModelCapabilities struct {
	Vision   bool // image input
	Audio    bool // audio input
	Video    bool // video input
	Document bool // PDF/document input
	// Reasoning reports whether the model supports reasoning/thinking mode.
	// When false, effort levels other than "auto"/"none" are stripped
	// before the request is sent so non-reasoning models do not receive a
	// thinking field they would reject or ignore.
	Reasoning bool
	// ReasoningReplay is true when the upstream requires reasoning_content
	// (Chat Completions) or reasoning items (Responses API) to be echoed
	// back on every assistant message in subsequent turns. Resolved from
	// the model's InterleavedField catalog signal or a provider/model
	// pattern fallback.
	ReasoningReplay bool
}

// modelCapabilities resolves the input modalities the given model on the
// given provider supports. Unknown models (not in catalog) default to
// Vision=true but Audio=false and Video=false — see domain.ModelCapabilities
// for rationale. ReasoningReplay is resolved from the model's
// InterleavedField (catalog signal) with a provider/model pattern fallback.
//
// Learned disabled modalities (from 400-learning) override the catalog:
// if a previous 400 taught us that this provider+model is text-only
// (or lacks a specific modality), the corresponding caps field is forced
// to false so the first request strips those attachments instead of
// waiting for a 400 and retrying.
func modelCapabilities(provider *domain.Provider, model string) ModelCapabilities {
	return modelCapabilitiesWithLearned(provider, model, nil)
}

// modelCapabilitiesWithLearned is the testable form of modelCapabilities
// that accepts a learnedParamsCache for applying learned disabled
// modalities. When cache is nil, no learned overrides are applied.
func modelCapabilitiesWithLearned(provider *domain.Provider, model string, cache *learnedParamsCache) ModelCapabilities {
	dc := domain.ModelCapabilitiesOf(provider, model)
	caps := ModelCapabilities{Vision: dc.Vision, Audio: dc.Audio, Video: dc.Video, Document: dc.Document}
	if provider != nil {
		if m := provider.FindModel(model); m != nil {
			caps.Reasoning = m.Reasoning
			caps.ReasoningReplay = domain.RequiresReasoningReplay(provider.ID, model, m.InterleavedField)
		} else {
			caps.ReasoningReplay = domain.RequiresReasoningReplay(provider.ID, model, "")
		}
	}
	// Apply learned disabled modalities as proactive override. A previous
	// 400 that taught us "this model is text-only" (or lacks audio/video)
	// should prevent sending unsupported content on the first request,
	// not just on retry.
	providerID := ""
	if provider != nil {
		providerID = provider.ID
	}
	for _, modality := range cache.DisabledModalities(providerID, model) {
		switch strings.ToLower(modality) {
		case "vision":
			caps.Vision = false
		case "audio":
			caps.Audio = false
		case "video":
			caps.Video = false
		case "document":
			caps.Document = false
		}
	}
	return caps
}

// modelSupportsVision reports whether the given model on the given provider
// supports image input. Returns true when the model metadata is unknown
// (not in catalog) to preserve backward compatibility — providers will
// reject the image if unsupported, and the reactive error path handles that.
func modelSupportsVision(provider *domain.Provider, model string) bool {
	return domain.ModelSupportsVision(provider, model)
}

// filterHydrationDomainMessages strips hydration checkpoint messages (pure
// hydration tool calls, no content/reasoning) from a domain.Message slice.
// Used by compaction to exclude synthetic runtime snapshots from summaries.
func filterHydrationDomainMessages(msgs []domain.Message) []domain.Message {
	return domain.FilterHydrationDomainMessages(msgs)
}

func chatMessages(c *domain.Conversation, pendingMsgID string, caps ModelCapabilities) []ChatMessage {
	var out []ChatMessage
	for _, m := range c.Messages {
		switch m.Role {
		case domain.RoleUser:
			content := m.Content
			attachments := m.Attachments
			if !caps.Vision && hasAttachmentOfType(attachments, "image") {
				imageAtts := filterAttachmentsByType(attachments, "image")
				attachments = stripAttachmentsByType(attachments, "image")
				placeholder := omittedPlaceholderFor("image", "read_media", imageAtts)
				if content == "" {
					content = placeholder
				} else if !containsOmissionNote(content, "image") {
					content = content + "\n\n" + placeholder
				}
			}
			// Vision model: keep the image pixels visible AND surface the
			// absolute file path so the model can reference it for image-to-
			// image editing via generate_image. Unlike the non-vision branch
			// above, the attachment is not stripped — this only appends a path
			// note with distinct wording so the model does not mistake it for
			// a missing/omitted image.
			if caps.Vision && hasAttachmentOfType(attachments, "image") {
				imageAtts := filterAttachmentsByType(attachments, "image")
				if note := domain.VisionImagePathNote(imageAtts); note != "" && !domain.ContainsVisionImageNote(content) {
					if content == "" {
						content = note
					} else {
						content = content + "\n\n" + note
					}
				}
			}
			if !caps.Audio && hasAttachmentOfType(attachments, "audio") {
				audioAtts := filterAttachmentsByType(attachments, "audio")
				attachments = stripAttachmentsByType(attachments, "audio")
				placeholder := omittedPlaceholderFor("audio", "read_media", audioAtts)
				if content == "" {
					content = placeholder
				} else if !containsOmissionNote(content, "audio") {
					content = content + "\n\n" + placeholder
				}
			}
			if !caps.Video && hasAttachmentOfType(attachments, "video") {
				videoAtts := filterAttachmentsByType(attachments, "video")
				attachments = stripAttachmentsByType(attachments, "video")
				placeholder := omittedPlaceholderFor("video", "read_media", videoAtts)
				if content == "" {
					content = placeholder
				} else if !containsOmissionNote(content, "video") {
					content = content + "\n\n" + placeholder
				}
			}
			if !caps.Document && hasAttachmentOfType(attachments, "file") {
				fileAtts := filterAttachmentsByType(attachments, "file")
				attachments = stripAttachmentsByType(attachments, "file")
				placeholder := omittedPlaceholderFor("document", "read_media", fileAtts)
				if content == "" {
					content = placeholder
				} else if !containsOmissionNote(content, "document") {
					content = content + "\n\n" + placeholder
				}
			}
			// Folder attachments are path-only references. Inject the path
			// as text so the agent can use file tools to explore the
			// directory. The attachment itself is stripped from the
			// attachment list (it has no bytes for the provider).
			if hasAttachmentOfType(attachments, "folder") {
				folderAtts := filterAttachmentsByType(attachments, "folder")
				attachments = stripAttachmentsByType(attachments, "folder")
				placeholder := folderPlaceholderFor(folderAtts)
				if content == "" {
					content = placeholder
				} else {
					content = content + "\n\n" + placeholder
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
				// Summarize first (show/subagent get short summaries),
				// then filter attachments by model capability (appends
				// notes for unsupported media), then wrap the combined
				// content in the untrusted envelope. This order ensures
				// capability-filter notes land INSIDE the envelope —
				// appending notes after wrapping would let a malicious
				// tool craft a file path containing injection
				// instructions that the model treats as trusted content.
				toolContent := summarizeToolContent(tc.Name, tc.Output)
				toolAtts := tc.OutputAttachments
				if len(toolAtts) > 0 {
					toolAtts, toolContent = filterToolAttachmentsByCaps(toolAtts, toolContent, caps)
				}
				toolContent = wrapToolOutput(tc.Name, toolContent)
				out = append(out, ChatMessage{Role: "tool", ToolResult: &ToolResult{
					ToolCallID: tc.ID, Name: tc.Name, Content: toolContent,
					Attachments: toolAtts,
				}})
			}
		case domain.RoleSystem:
			// folded into the system prompt by buildSystemPrompt
		}
	}
	return out
}

// filterToolAttachmentsByCaps strips media attachments from a tool result
// when the active model does not support the corresponding modality, and
// appends a text note to the content so the model knows the media exists
// but couldn't be delivered. This prevents provider errors when a read_*
// tool returns media that the model can't process:
//   - audio sent to a non-audio model via input_audio/image_url transport
//     (Nvidia NIM rejects with "Failed to load image from data:audio/...")
//   - video sent to a non-video model via video_url/input_image transport
//     (Stealth rejects with HTTP 400; OpenAI doesn't support video
//     natively at all — only frames as input_image)
//   - image sent to a non-vision model
//
// The same gating applies to user-authored attachments in chatMessages
// (stripped + placeholder) and to proactive fallback enrichment
// (enrichWithAudioDescriptions / enrichWithVideoDescriptions describe the
// media via a fallback model so the text-only model still gets the content).
func filterToolAttachmentsByCaps(atts []domain.Attachment, content string, caps ModelCapabilities) ([]domain.Attachment, string) {
	if len(atts) == 0 {
		return atts, content
	}
	filtered := make([]domain.Attachment, 0, len(atts))
	var notes []string
	for _, att := range atts {
		switch att.Type {
		case "image":
			if !caps.Vision {
				notes = append(notes, fmt.Sprintf("[Image %q was loaded but cannot be shown to this model. File path: %s]", att.Name, att.FilePath))
				continue
			}
		case "audio":
			if !caps.Audio {
				notes = append(notes, fmt.Sprintf("[Audio %q was loaded but cannot be played to this model. File path: %s]", att.Name, att.FilePath))
				continue
			}
		case "video":
			if !caps.Video {
				notes = append(notes, fmt.Sprintf("[Video %q was loaded but cannot be shown to this model. File path: %s]", att.Name, att.FilePath))
				continue
			}
		case "file":
			if !caps.Document {
				notes = append(notes, fmt.Sprintf("[Document %q was loaded but cannot be read by this model. File path: %s]", att.Name, att.FilePath))
				continue
			}
		}
		filtered = append(filtered, att)
	}
	if len(notes) > 0 {
		if content != "" {
			content += "\n"
		}
		content += strings.Join(notes, "\n")
	}
	return filtered, content
}

// omittedPlaceholderFor builds a placeholder that tells the model to call
// the matching read_* tool with the absolute file path to access the
// attachment. Only absolute paths are shown — relative paths are rejected
// to avoid ambiguity between the model's working directory and the actual
// file location. kind is "image" | "audio" | "video"; toolName is the
// matching read_* tool.
func omittedPlaceholderFor(kind, toolName string, atts []domain.Attachment) string {
	return domain.OmittedPlaceholderFor(kind, toolName, atts)
}

// imageOmittedPlaceholderFor is kept for backward compatibility with tests.

// folderPlaceholderFor builds a text placeholder that tells the agent the
// absolute path of a dropped folder. The agent can use file tools
// (list_dir, read_file, etc.) to explore the directory. Folder attachments
// carry no bytes — only the path.
func folderPlaceholderFor(atts []domain.Attachment) string {
	return domain.FolderPlaceholderFor(atts)
}

func hasAttachmentOfType(atts []domain.Attachment, typ string) bool {
	return domain.HasAttachmentOfType(atts, typ)
}

func stripAttachmentsByType(atts []domain.Attachment, typ string) []domain.Attachment {
	return domain.StripAttachmentsByType(atts, typ)
}

func filterAttachmentsByType(atts []domain.Attachment, typ string) []domain.Attachment {
	return domain.FilterAttachmentsByType(atts, typ)
}

func containsOmissionNote(content, kind string) bool {
	return domain.ContainsOmissionNote(content, kind)
}

// saveAttachmentsToDisk writes image/file attachments to the attachment store
// and fills in the FilePath field with the absolute path. Failures are
// logged but non-fatal — the attachment still has its DataURL for inline
// use by vision-capable models.
func (a *App) saveAttachmentsToDisk(conversationID string, attachments []domain.Attachment) {
	if a.Attachments == nil {
		return
	}
	for i := range attachments {
		att := &attachments[i]
		if att.Type == "text" || att.Type == "folder" || att.FilePath != "" {
			continue
		}
		path, err := a.Attachments.Save(conversationID, *att)
		if err != nil {
			a.log("warn", "attachments", "failed to save %s to disk: %v", att.Name, err)
			continue
		}
		att.FilePath = path
	}
}

// toolRoundSignature returns a deterministic, order-independent signature for
// a set of tool calls. Tools are sorted by name+args so the same set in any
// order produces the same signature. This lets the repeated-tool guard detect
// parallel-tool loops (e.g. GPT-5.6 Luna calling 6 mcp_enable in a different
// order each round).
func toolRoundSignature(calls []domain.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	sigs := make([]string, len(calls))
	for i, c := range calls {
		sigs[i] = c.Name + "|" + c.Args
	}
	sort.Strings(sigs)
	return strings.Join(sigs, "\n")
}

// repeatedToolGuard detects when the agent calls the same set of tools with
// the same arguments for N consecutive rounds without producing text. When
// the streak reaches the limit, check returns true and the caller strips
// tools for the next round to break the loop. A round with text content or a
// different tool signature resets the streak. limit=0 disables the guard.
type repeatedToolGuard struct {
	limit   int
	streak  int
	lastSig string
}

// check updates the guard state and returns true if the repeated-tool limit
// has been reached (the caller should force a text-only round). After firing,
// the guard resets so a new streak must build up before firing again.
func (g *repeatedToolGuard) check(calls []domain.ToolCall, content string) bool {
	if g.limit <= 0 || len(calls) == 0 || content != "" {
		g.streak = 0
		g.lastSig = ""
		return false
	}
	sig := toolRoundSignature(calls)
	if sig == g.lastSig {
		g.streak++
	} else {
		g.streak = 1
		g.lastSig = sig
	}
	if g.streak >= g.limit {
		g.streak = 0
		g.lastSig = ""
		return true
	}
	return false
}
