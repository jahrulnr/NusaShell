package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"nusashell/application/service/attachments"
	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/application/service/tooloutput"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/nonce"
	clock "nusashell/pkg/time"
	"nusashell/resources"
)

func (a *App) handleTurnsStart(ctx context.Context, req contracts.TurnStartRequest) (any, *contracts.RPCError) {
	// Existence check before any side effects (attachment writes, model
	// resolution); the conversation is re-fetched under startMu below.
	if _, rpcErr := a.getConversation(req.ConversationID); rpcErr != nil {
		return nil, rpcErr
	}
	text := strings.TrimSpace(req.Text)
	attachments, rpcErr := attachments.AttachmentsFromDTO(req.Attachments)
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
	if a.activeRunForConversation(req.ConversationID) != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
	}
	turnLock := a.conversationTurnLock(req.ConversationID)
	turnLock.Lock()
	defer turnLock.Unlock()
	repo, rpcErr := a.loadRepoRPC(req.ConversationID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	c := repo.Conversation()
	if c.Status == "running" {
		if a.activeRunForConversation(c.ID) != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
		}
		a.healOrphanedRunningConversation(c)
	}

	now := clock.NewTime().Time()
	userMsg := domain.Message{
		ID:          domain.NewID(domain.IDPrefixMsg),
		Role:        domain.RoleUser,
		Content:     text,
		Attachments: attachments,
		CreatedAt:   now,
		Status:      domain.StatusDone,
	}
	asstMsg := domain.Message{
		ID:         domain.NewID(domain.IDPrefixMsg),
		Role:       domain.RoleAssistant,
		CreatedAt:  now,
		ProviderID: provider.ID,
	}
	a.addTurnMessages(c, userMsg, asstMsg)
	c.Model = qualifiedModel
	c.Effort = req.Effort
	c.Status = "running"
	if err := repo.Save(); err != nil {
		return nil, rpcInternal(err)
	}

	// Turn goroutines outlive the HTTP request (fire-and-forget: the RPC
	// returns the RunID immediately while the turn streams via SSE/WS).
	// Derive from the request context but detach cancellation so the turn
	// is not killed when the HTTP response is sent. The turn is cancelled
	// explicitly via handleTurnsStop or server shutdown.
	turnCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	run := &TurnRun{ID: domain.NewID(domain.IDPrefixRun), ConversationID: c.ID, MessageID: asstMsg.ID, Ctx: turnCtx, Cancel: cancel, ProviderID: provider.ID, Workspace: c.Workspace}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	a.goSafe("agent", func() {
		a.runTurn(run, provider, apiKey, bareModel, req.Effort, asstMsg.ID, false, modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams, a.modelOverrides))
	})
	a.log("info", "agent", "turn started: %s (model %s)", run.ID, bareModel)
	return contracts.TurnStartResult{RunID: run.ID}, nil
}

// handleTurnsRetry re-runs the last failed assistant message with a different
// model picked by the user. When the failed message has partial content (and
// no tool calls), the partial is frozen as a completed step and the new model
// is asked to continue from where it stopped; otherwise a new assistant
// message is appended after the failed one (formed history is never deleted).
func (a *App) handleTurnsRetry(ctx context.Context, req contracts.TurnRetryRequest) (any, *contracts.RPCError) {
	// Existence check before validation; the conversation is re-fetched
	// under startMu below.
	if _, rpcErr := a.getConversation(req.ConversationID); rpcErr != nil {
		return nil, rpcErr
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "model is required"}
	}

	a.startMu.Lock()
	defer a.startMu.Unlock()
	if a.activeRunForConversation(req.ConversationID) != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
	}
	turnLock := a.conversationTurnLock(req.ConversationID)
	turnLock.Lock()
	defer turnLock.Unlock()
	repo, rpcErr := a.loadRepoRPC(req.ConversationID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	c := repo.Conversation()
	if c.Status == "running" {
		if a.activeRunForConversation(c.ID) != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "conversation is busy; stop the running turn first"}
		}
		a.healOrphanedRunningConversation(c)
	}

	failedIdx := lastFailedAssistantIndex(c.Messages)
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
	if continuation {
		failed.Status = domain.StatusDone
		failed.Error = ""
	}
	next := domain.Message{ID: domain.NewID(domain.IDPrefixMsg), Role: domain.RoleAssistant, CreatedAt: clock.NewTime().Time(), ProviderID: provider.ID}
	if err := repo.Add(domain.RoleAssistant, next); err != nil {
		return nil, rpcInternal(err)
	}
	targetMsgID := next.ID
	c.Model = qualifiedModel
	c.Effort = req.Effort
	c.Status = "running"
	if err := repo.Save(); err != nil {
		return nil, rpcInternal(err)
	}

	turnCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	run := &TurnRun{ID: domain.NewID(domain.IDPrefixRun), ConversationID: c.ID, MessageID: targetMsgID, Ctx: turnCtx, Cancel: cancel, ProviderID: provider.ID, Workspace: c.Workspace}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	a.goSafe("agent", func() {
		a.runTurn(run, provider, apiKey, bareModel, req.Effort, targetMsgID, continuation, modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams, a.modelOverrides))
	})
	a.log("info", "agent", "turn retried: %s (model %s)", run.ID, bareModel)
	return contracts.TurnStartResult{RunID: run.ID}, nil
}

// lastFailedAssistantIndex returns the index of the last real (non-hydration)
// assistant message when that message is in error status. Earlier failed
// assistants stay in formed history after retry, so scanning for any error
// would retry a recovered turn.
func lastFailedAssistantIndex(msgs []domain.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != domain.RoleAssistant || isHydrationMessage(msgs[i]) {
			continue
		}
		if msgs[i].Status == domain.StatusError {
			return i
		}
		return -1
	}
	return -1
}

// shouldAnnounceRestart reports whether a persisted conversation predates
// the current process (was used before the backend restarted) and therefore
// gets a one-shot restart announcement on its next user message. Fresh
// conversations (created after startup) and empty conversations never
// qualify; a zero startedAt (tests, unusual composition) disables it.
func shouldAnnounceRestart(c *domain.Conversation, startedAt time.Time) bool {
	return !startedAt.IsZero() && startedAt.After(c.UpdatedAt) && hasDurableHistory(c)
}

// hasDurableHistory reports whether the conversation has any message the
// model should treat as prior work. A hydration-only transcript is not
// durable history: it was persisted by an empty-room workspace pick and
// must not trigger a restart announcement on the first real user turn.
func hasDurableHistory(c *domain.Conversation) bool {
	if c == nil {
		return false
	}
	for _, m := range c.Messages {
		if !isHydrationMessage(m) {
			return true
		}
	}
	return false
}

// addTurnMessages appends the user message, optional harness notices
// (workspace-switch then restart), and the assistant turn message, in that
// order. The restart decision is taken BEFORE AddMessage because AddMessage
// touches UpdatedAt past startedAt — deciding after would defeat the
// predates-restart check and break the one-shot-per-restart guarantee. The
// workspace-switch notice is taken at the same moment so a pick that raced
// this Save cannot double-inject.
func (a *App) addTurnMessages(c *domain.Conversation, userMsg, asstMsg domain.Message) {
	announce := shouldAnnounceRestart(c, a.startedAt)
	wsNotice := a.takeWorkspaceSwitchNotice(c)
	c.AddMessage(userMsg)
	if c.Title == "" || c.Title == "Untitled" {
		c.Title = c.DefaultTitle()
	}
	// Fresh-room hydration is appended after the opening user and before the
	// assistant placeholder so Save stays append-only (no mid-history insert).
	if isFreshRoom(c) && !hasHydrationCheckpoint(c) {
		if hyd := a.buildHydration(c); len(hyd) > 0 {
			for _, m := range buildHydrationDomainMessages(hyd) {
				c.AddMessage(m)
			}
		}
	}
	if wsNotice != nil {
		c.AddMessage(*wsNotice)
	}
	if announce {
		c.AddMessage(a.restartAnnouncement())
	}
	c.AddMessage(asstMsg)
}

func hasHydrationCheckpoint(c *domain.Conversation) bool {
	if c == nil {
		return false
	}
	for _, m := range c.Messages {
		if isHydrationMessage(m) {
			return true
		}
	}
	return false
}

// takeWorkspaceSwitchNotice consumes a queued workspace-switch notice and
// returns the synthetic assistant message to persist immediately after the
// new user turn. One-shot: the pending flag is cleared even if the notice
// is announcement-only (no AGENTS.md).
func (a *App) takeWorkspaceSwitchNotice(c *domain.Conversation) *domain.Message {
	if c == nil || !c.PendingWorkspaceAnnouncement {
		return nil
	}
	from := c.WorkspaceSwitchFrom
	to := c.Workspace
	c.PendingWorkspaceAnnouncement = false
	c.WorkspaceSwitchFrom = ""
	msg := a.workspaceSwitchNotice(from, to)
	return &msg
}

// workspaceSwitchNotice builds the visible synthetic assistant message for
// a workspace switch: an `announcement` tool result plus, when the file
// exists, a real `file_read` of <workspace>/AGENTS.md. Call IDs are not
// hydrate- prefixed so the UI and conversation JSON show the tools.
func (a *App) workspaceSwitchNotice(from, to string) domain.Message {
	calls := []domain.ToolCall{{
		ID:     domain.AnnouncementToolCallPrefix + nonce.Random(),
		Name:   domain.AnnouncementToolName,
		Args:   domain.WorkspaceChangedAnnouncementArgs(from, to),
		Status: domain.ToolOK,
		Output: domain.WorkspaceChangedAnnouncementMessage(from, to),
	}}
	if slot := a.readWorkspaceAgentsMD(to); slot.content != "" {
		calls = append(calls, domain.ToolCall{
			ID:     domain.NewID(domain.IDPrefixCall),
			Name:   slot.name,
			Args:   slot.args,
			Status: domain.ToolOK,
			Output: slot.content,
		})
	}
	return domain.Message{
		ID:        domain.NewID(domain.IDPrefixMsg),
		Role:      domain.RoleAssistant,
		CreatedAt: clock.NewTime().Time(),
		Status:    domain.StatusDone,
		ToolCalls: calls,
	}
}

func (a *App) readWorkspaceAgentsMD(ws string) hydrationSlot {
	if a == nil || a.Toolbox == nil {
		return hydrationSlot{}
	}
	return NewHydrationBuilder(HydrationSource{
		Executor:       a.Toolbox,
		RuntimeContext: RuntimeContextSnapshot{Workspace: ws},
	}).readAgentsMD()
}

// restartAnnouncement builds the synthetic assistant message carrying the
// `announcement` tool call with its result pre-filled. It is persisted
// into the conversation so the model sees it in this turn and in later
// turns (auto-continue), and the UI renders it as a normal tool card.
func (a *App) restartAnnouncement() domain.Message {
	return domain.Message{
		ID:        domain.NewID(domain.IDPrefixMsg),
		Role:      domain.RoleAssistant,
		CreatedAt: clock.NewTime().Time(),
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.AnnouncementToolCallPrefix + nonce.Random(),
			Name:   domain.AnnouncementToolName,
			Args:   "{}",
			Status: domain.ToolOK,
			Output: domain.AnnouncementMessage,
		}},
	}
}

// autoContinueAnnouncement builds the synthetic assistant message carrying
// the `announcement` tool call that continues the todo-driven chain into the
// next turn. Mirrors restartAnnouncement: persisted, result pre-filled, args
// self-describing the chain state (rounds used, open todos).
func (a *App) autoContinueAnnouncement(decision domain.AutoContinueDecision) domain.Message {
	return domain.Message{
		ID:        domain.NewID(domain.IDPrefixMsg),
		Role:      domain.RoleAssistant,
		CreatedAt: clock.NewTime().Time(),
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.AnnouncementToolCallPrefix + nonce.Random(),
			Name:   domain.AnnouncementToolName,
			Args:   domain.AutoContinueAnnouncementArgs(decision.ContinuesUsed, decision.OpenTodoCount),
			Status: domain.ToolOK,
			Output: continuePrompt,
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
	return a.activeRunForConversationLocked(convID)
}

func (a *App) activeRunForConversationLocked(convID string) *TurnRun {
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
	repo := bindConversation(a.Conversations, c)
	if !c.RecoverOrphanedTurn(domain.OrphanedTurnError) {
		return
	}
	a.log("error", "agent", "turn %s exited without terminal state, recovered conversation %s", run.ID, c.ID)
	_ = repo.Save()
	a.Bus.Emit(contracts.EventTurnError, contracts.TurnErrorEvent{
		RunID:          run.ID,
		ConversationID: run.ConversationID,
		MessageID:      run.currentMessageID(),
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
	out := contracts.TurnActiveResult{
		RunID:          run.ID,
		ConversationID: run.ConversationID,
		MessageID:      run.currentMessageID(),
		Active:         true,
	}
	if s := run.queuedSteer(); s != nil {
		out.QueuedSteer = s.Text
		out.QueuedSteerID = s.ID
	}
	return out, nil
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
	run := a.activeRunForConversation(req.ConversationID)
	if run == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "no active turn for this conversation"}
	}
	attachments, rpcErr := attachments.AttachmentsFromDTO(req.Attachments)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if text == "" && len(attachments) == 0 {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "steer text is required"}
	}
	a.saveAttachmentsToDisk(req.ConversationID, attachments)
	entry := newSteerEntry(text, attachments)
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
	entry := run.cancelSteerEntry()
	if entry == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "no queued steer to cancel"}
	}
	a.Bus.Emit(contracts.EventSteerCancelled, contracts.SteerEvent{
		ConversationID: req.ConversationID, SteerID: entry.ID, Text: entry.Text, Status: "cancelled", Reason: contracts.SteerCancelReasonUser,
	})
	a.log("info", "agent", "steer cancelled for %s", req.ConversationID)
	return map[string]any{"ok": true, "accepted": true}, nil
}

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
	maxTokens := resolveMaxOutput(provider, model, settings)
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
	// Server-side compaction (context_management) is handled by the server
	// during the normal stream call, not by a separate compaction model.
	// Skip the compaction-model override when the chat model is server-side
	// eligible so the compaction item stays valid for the same model.
	if domain.OpenAISupportsServerCompaction(defaultModel) {
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

// resolveContextWindow picks the effective context window for compaction
// decisions: min(model context, max_input_tokens) when both are known, or the
// configured max_input_tokens fallback when the model does not advertise one.
// A learned cap from a provider 400 overflow error overrides the catalog value
// for the provider+model so future turns do not overestimate the window.
// A manual context override (set via the review agent) wins over the learned
// cap — the provider clone already carries it, so we skip the learned cap
// when one is present to avoid clobbering the operator's correction.
func (a *App) resolveContextWindow(provider *domain.Provider, model string, settings domain.Settings) int {
	cw := domain.ResolveContextWindow(provider, model, settings)
	if a.learnedParams != nil {
		if cap := a.learnedParams.ContextCap(provider.ID, model); cap > 0 && cap < cw {
			a.log("info", "learning", "capping context window for %s/%s to %d from learned 400", provider.ID, model, cap)
			cw = cap
		}
	}
	// Manual override is applied last and wins over both the catalog value
	// and the learned cap. Applied directly (not just via the mutated clone)
	// so it also covers models absent from the catalog and any resolve path
	// that did not pre-mutate the provider.
	if a.modelOverrides != nil {
		if o := a.modelOverrides.Get(provider.ID, model); o != nil && o.Context != nil {
			cw = *o.Context
		}
	}
	return cw
}

const (
	compactionKeepTokenBudget   = domain.CompactionKeepTokenBudget
	compactionSummaryMaxOut     = domain.CompactionSummaryMaxOut
	compactionSystemReserve     = domain.CompactionSystemReserve
	compactionSummaryMinChars   = domain.CompactionSummaryMinChars
	compactionSummaryMaxRetries = domain.CompactionSummaryMaxRetries
	compactionMaxToolCallChars  = domain.CompactionMaxToolCallChars
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

// compactionToolChoice forces the compaction model to call summary() instead
// of continuing the agent turn as free-text (the failure mode that produced
// one-sentence "handovers" from reasoning models).
//
// The wire shape differs by provider kind:
//   - Anthropic Messages: {"type":"tool","name":"summary"}
//   - OpenAI Responses:   {"type":"function","name":"summary"} (flat — the
//     Responses API rejects the nested Chat shape with
//     "missing_required_parameter: 'tool_choice.name'")
//   - OpenAI Chat / OpenRouter chat: {"type":"function","function":{"name":"summary"}}
func compactionToolChoice(kind domain.ProviderKind) any {
	switch kind {
	case domain.ProviderMessages:
		return map[string]any{"type": "tool", "name": compactionSummaryToolName}
	case domain.ProviderResponses:
		return map[string]any{"type": "function", "name": compactionSummaryToolName}
	default:
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": compactionSummaryToolName},
		}
	}
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

// compactionSummaryEchoesAssistant reports whether the candidate summary is
// just a copy of the latest assistant turn (the compaction model continuing
// the live agent instead of writing a handoff).
func compactionSummaryEchoesAssistant(summary string, msgs []ChatMessage) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		ast := strings.TrimSpace(msgs[i].Content)
		if len(ast) < 80 {
			return false
		}
		prefix := ast
		if len(prefix) > 120 {
			prefix = prefix[:120]
		}
		return strings.Contains(summary, prefix)
	}
	return false
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
// When the model is eligible for OpenAI-native compaction (gpt-5+) and the
// adapter implements ServerCompactor, the call is delegated to the server-side
// /responses/compact endpoint first. The opaque blob replaces the archived
// prefix and the live suffix is kept intact (tool calls/reasoning preserved).
// If that fails (e.g. 404/400 for accounts without the endpoint, network
// error), the function falls back once to the client-side multi-pass
// summarization below.

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

	// Server-side compaction (context_management) is handled by the server
	// during the normal stream call. When the model is eligible, skip the
	// client-side summarization entirely — the server compacts automatically
	// when the threshold is crossed and returns a compaction item in the
	// response stream. The application layer captures and stores it.
	if domain.OpenAISupportsServerCompaction(model) {
		return "", nil
	}

	// Clone a contiguous prefix to summarize. Compact/ArchiveMessages use
	// the same split so keep is a suffix clone — not "all users + recent
	// assistants", which piled users on top of the handoff request.
	splitIdx := c.CompactionSplitIndex(effectiveKeepBudget)

	toCompact := filterHydrationDomainMessages(c.Messages[:splitIdx])
	runningSummary := c.Summary

	remainingMsgs := make([]domain.Message, 0, len(toCompact))
	for _, m := range toCompact {
		if m.Role == domain.RoleSystem {
			continue
		}
		if domain.IsCompactionSummary(m.Content) {
			continue
		}
		remainingMsgs = append(remainingMsgs, m)
	}
	if len(remainingMsgs) == 0 {
		return "", nil
	}

	systemPrompt := compactionPrompt
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
				Content: resources.CompactedUserPrompt(runningSummary),
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
						ToolCallID: tc.ID, Name: tc.Name, Content: tooloutput.ProviderToolContent(tc.Name, tc.Output),
					}})
				}
			}
		}
		msgs = appendCompactionHandoffUser(msgs)
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
		// One pass = one AgentEngine run: summary() is forced via
		// ToolChoice, retries double the token budget until the summary
		// is long enough or the budget is exhausted. On failure, return
		// an error so the caller emits EventCompactionFailed and the user
		// cannot continue until compaction succeeds (retry re-enters
		// compaction).
		pass := &compactionPass{
			app: a, adapter: adapter, model: model,
			system: systemPrompt, msgs: msgs,
			budget: passBudget, maxBudget: maxBudget, minChars: summaryMinChars,
			convID: c.ID,
		}
		if !pass.run(ctx) {
			return "", fmt.Errorf("compaction failed: summary too short after %d retries (last=%d chars, min=%d): %w",
				compactionSummaryMaxRetries, pass.lastLen, summaryMinChars, pass.lastErr)
		}
		runningSummary = pass.summary
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

// compactionPassAvailable is the per-pass token budget for message content.
// The running summary grows across passes, so later chunks shrink to leave
// room for it instead of using a one-shot 2000-token reserve.
func compactionPassAvailable(contextWindow int, runningSummary string, summaryMaxOut int) int {
	summaryTokens := domain.EstimateTokens(runningSummary)
	if runningSummary != "" {
		summaryTokens += domain.EstimateTokens(resources.CompactedUserPrompt(""))
	}
	handoffTokens := domain.EstimateTokens(strings.TrimSpace(compactionHandoffUserPrompt))
	available := contextWindow - compactionSystemReserve - summaryTokens - summaryMaxOut - handoffTokens
	if available < 1000 {
		return 1000
	}
	return available
}

// takeCompactionChunk takes the longest prefix of msgs whose token estimate
// fits in available. A single oversized message is still taken so compaction
// cannot stall. System markers should already have been stripped.
func takeCompactionChunk(msgs []domain.Message, available int) (chunk, rest []domain.Message) {
	return domain.TakeCompactionChunk(msgs, available)
}

// appendCompactionHandoffUser appends the handoff command as the last user
// message so the compaction model is not left on an assistant/tool turn.
func appendCompactionHandoffUser(msgs []ChatMessage) []ChatMessage {
	closer := strings.TrimSpace(compactionHandoffUserPrompt)
	if closer == "" {
		return msgs
	}
	return append(msgs, ChatMessage{Role: "user", Content: closer})
}

func (a *App) persistCompactedConversation(c *domain.Conversation, summary string, keepBudget int) error {
	if c == nil {
		return fmt.Errorf("conversation is required")
	}
	repo := bindConversation(a.Conversations, c)
	tmp := cloneConversation(c)
	tmp.Messages = filterHydrationDomainMessages(tmp.Messages)
	toArchive := tmp.ArchiveMessages(keepBudget)
	if len(toArchive) > 0 {
		idx, err := a.Conversations.ArchiveChunk(c.ID, toArchive)
		if err != nil {
			a.log("warn", "agent", "failed to archive chunk for %s: %v", c.ID, err)
		} else {
			c.ChunkCount = idx + 1
		}
	}
	tmp.Summary = ""
	handoverContent := resources.CompactedUserPrompt(summary)
	tmp.Compact(summary, handoverContent, keepBudget)
	if hydrationMsgs := a.buildHydration(tmp); len(hydrationMsgs) > 0 {
		tmp = a.persistHydration(tmp, hydrationMsgs)
	}
	chunkCount := c.ChunkCount
	workspace := c.Workspace
	model := c.Model
	effort := c.Effort
	origin := c.Origin
	title := c.Title
	status := c.Status
	pending := c.PendingWorkspaceAnnouncement
	switchFrom := c.WorkspaceSwitchFrom
	blob := tmp.CompactionBlob
	epochSummary := tmp.Summary
	repo.ResetTranscript()
	c.ChunkCount = chunkCount
	c.Workspace = workspace
	c.Model = model
	c.Effort = effort
	c.Origin = origin
	c.Title = title
	c.Status = status
	c.PendingWorkspaceAnnouncement = pending
	c.WorkspaceSwitchFrom = switchFrom
	c.CompactionBlob = blob
	c.Summary = epochSummary
	for _, m := range tmp.Messages {
		if err := repo.Add(m.Role, m); err != nil {
			return err
		}
	}
	return repo.Save()
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
			if visibleText(m.Content) == "" && visibleText(m.Reasoning) == "" && len(m.Steps) == 0 {
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

// modelCapabilitiesWithLearned is the testable form of model capabilities
// resolution: it accepts a learnedParamsCache for applying learned disabled
// modalities and a modelOverridesCache for applying manual overrides. When
// cache is nil, no learned overrides are applied; when manual is nil, no
// manual overrides are applied. Precedence: catalog → learned → manual
// (manual is applied last so it always wins).
func modelCapabilitiesWithLearned(provider *domain.Provider, model string, cache *learnedparams.Cache, manual *modeloverrides.Cache) ModelCapabilities {
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
	// Manual overrides win over both the catalog and learned adaptations.
	// Applied last so an operator/review-agent correction is never clobbered
	// by a learned rule re-derived from the (already mutated) provider.
	if manual != nil && provider != nil {
		if o := manual.Get(provider.ID, model); o != nil {
			if o.Vision != nil {
				caps.Vision = *o.Vision
			}
			if o.Audio != nil {
				caps.Audio = *o.Audio
			}
			if o.Video != nil {
				caps.Video = *o.Video
			}
			if o.Document != nil {
				caps.Document = *o.Document
			}
			if o.Reasoning != nil {
				caps.Reasoning = *o.Reasoning
			}
		}
	}
	return caps
}

// filterHydrationDomainMessages strips hydration checkpoint messages (pure
// hydration tool calls, no content/reasoning) from a domain.Message slice.
// Used by compaction to exclude synthetic runtime snapshots from summaries.
func filterHydrationDomainMessages(msgs []domain.Message) []domain.Message {
	return domain.FilterHydrationDomainMessages(msgs)
}

func chatMessages(c *domain.Conversation, pendingMsgID string, caps ModelCapabilities) []ChatMessage {
	src := c.Messages
	if hydrationPrecedesFirstUser(src) {
		src = relocateHydrationAfterFirstUser(src)
	}
	var out []ChatMessage
	for _, m := range src {
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
			content := visibleText(m.Content)
			if m.ID == pendingMsgID && content == "" && m.Reasoning == "" && len(m.ToolCalls) == 0 {
				continue
			}
			if content == "" && m.Reasoning == "" && len(m.ToolCalls) == 0 {
				continue
			}
			cm := ChatMessage{Role: "assistant", Content: content, Reasoning: m.Reasoning, ToolCalls: m.ToolCalls}
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
				toolContent := tooloutput.SummarizeToolContent(tc.Name, tc.Output)
				toolAtts := tc.OutputAttachments
				if len(toolAtts) > 0 {
					toolAtts, toolContent = filterToolAttachmentsByCaps(toolAtts, toolContent, caps)
				}
				toolContent = tooloutput.WrapToolOutput(tc.Name, toolContent)
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
