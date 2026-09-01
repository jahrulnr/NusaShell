package application

import (
	"context"
	"strings"
	"time"

	"nusashell/application/service/attachments"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/nonce"
	clock "nusashell/pkg/time"
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
	c.ProviderRoute = req.ProviderRoute
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
	continuation := domain.ShouldContinueFailedTurn(*failed)
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
	c.ProviderRoute = req.ProviderRoute
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
		if msgs[i].Role != domain.RoleAssistant || domain.IsHydrationMessage(msgs[i]) {
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
		if !domain.IsHydrationMessage(m) {
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
	if domain.IsFreshRoom(c) && !hasHydrationCheckpoint(c) {
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
	// Pending harness announcements (config/memory/skills changes published
	// while idle) are injected after the user message and cleared. An active
	// turn's worker drains newer entries at round boundaries instead.
	for _, pa := range c.DrainPendingAnnouncements() {
		c.AddMessage(a.pendingAnnouncementMessage(pa))
	}
	c.AddMessage(asstMsg)
}

func hasHydrationCheckpoint(c *domain.Conversation) bool {
	if c == nil {
		return false
	}
	for _, m := range c.Messages {
		if domain.IsHydrationMessage(m) {
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
