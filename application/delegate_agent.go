package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/application/service/toolpresentation"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/nonce"
	clock "nusashell/pkg/time"
)

// SpawnDelegate is the `delegate` tool implementation: it spawns an
// internal NusaShell background agent (the same AgentEngine, running a
// headless turn in a hidden pipeline conversation) and always returns
// immediately with a run id. When the delegate finishes, the result is
// delivered through the shared background-run path (pendingRunDone): the
// original tool call is updated to a brief terminal status and a
// synthetic delegate_result tool call is injected at the next tool-round
// boundary (or a new turn if the parent is idle).
func (a *App) SpawnDelegate(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		Prompt    string `json:"prompt"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	parentConvID := ConversationIDFromContext(ctx)
	workspace := strings.TrimSpace(args.Workspace)
	if workspace == "" && parentConvID != "" && a.Conversations != nil {
		if conv, err := a.Conversations.Get(parentConvID); err == nil {
			workspace = conv.Workspace
		}
	}
	if workspace != "" && !domain.PathRooted(workspace) {
		return "", fmt.Errorf("workspace must be an absolute path")
	}

	// The delegate runs on the configured delegate model, or inherits the
	// parent conversation's model when that setting is empty.
	modelID, err := a.resolveDelegateModel(parentConvID)
	if err != nil {
		return "", err
	}

	runID := domain.NewID(domain.IDPrefixRun)
	toolCallID := ToolCallIDFromContext(ctx)
	starting, running := a.registerDelegateRun(runID, toolCallID, parentConvID, workspace, prompt, modelID)
	a.emitAcpRun(contracts.EventAcpRunStarted, starting)
	a.emitAcpRun(contracts.EventAcpRunUpdated, running)

	// Track before spawning: the goroutine may finish before this call
	// returns, and deliverRunDone must find the pending run to untrack
	// and trigger the completion turn.
	a.trackPendingRun(parentConvID, runID, domain.DelegateToolName)
	a.goSafe("delegate", func() {
		a.runDelegate(runID, toolCallID, parentConvID, workspace, prompt, modelID)
	})
	return fmt.Sprintf("Delegate run %s started; the result will be injected when it finishes.", runID), nil
}

// resolveDelegateModel picks the provider:model for a delegate run. An
// explicit Settings → Internal delegate model wins; the empty setting means
// inherit the parent conversation's model, otherwise normal headless
// resolution chooses the first enabled provider with a model.
func (a *App) resolveDelegateModel(parentConvID string) (string, error) {
	if a.Settings != nil {
		if configured := strings.TrimSpace(a.Settings.Get().DelegateModel); configured != "" {
			return configured, nil
		}
	}
	if parentConvID != "" && a.Conversations != nil {
		if conv, err := a.Conversations.Get(parentConvID); err == nil && strings.TrimSpace(conv.Model) != "" {
			return conv.Model, nil
		}
	}
	p, bare, _, err := a.resolveHeadlessModel("")
	if err != nil {
		return "", err
	}
	return p.ID + ":" + bare, nil
}

// runDelegate executes one delegate run headlessly and delivers the
// outcome to the parent conversation. Runs detached from any request.
func (a *App) runDelegate(runID, toolCallID, parentConvID, workspace, prompt, modelID string) {
	ctx := context.Background()
	output, runConvID, err := a.runHeadlessTurnKindObserved(ctx, prompt, modelID, domain.TrustTrusted, nil, AgentDelegate,
		func(conversationID string) {
			a.updateDelegateRunTranscript(runID, conversationID)
		})
	status := domain.ToolOK
	text := ""
	if err != nil {
		status = domain.ToolFailed
		text = "error: " + err.Error()
	} else if out, ok := output["output"].(string); ok {
		text = out
	}
	if status == domain.ToolOK && strings.TrimSpace(text) == "" {
		text = "Delegate run " + runID + " completed with no text output."
	}
	if run := a.finishDelegateRun(runID, runConvID, text, err); run != nil {
		a.persistAcpRun(run)
		a.emitAcpRun(contracts.EventAcpRunUpdated, run)
		a.emitAcpRun(contracts.EventAcpRunDone, run)
	}
	a.deliverRunDone(parentConvID, pendingRunDone{
		RunID: runID,
		Complete: func(cid string) error {
			return a.completeDelegateRunLocked(cid, runID, toolCallID, status, text, runConvID)
		},
	})
}

const (
	internalDelegateAgentID   = "internal"
	internalDelegateAgentName = "NusaShell delegate"
)

func (a *App) registerDelegateRun(runID, toolCallID, conversationID, workspace, prompt, modelID string) (*domain.AcpRun, *domain.AcpRun) {
	now := clock.NewTime().Time()
	_, currentModel, ok := domain.SplitQualifiedModel(strings.TrimSpace(modelID))
	if !ok {
		currentModel = strings.TrimSpace(modelID)
	}
	run := &domain.AcpRun{
		TaskState: domain.TaskState[domain.AcpRunStatus]{
			ID:        runID,
			Status:    domain.AcpRunStarting,
			StartedAt: now,
		},
		AgentID:          internalDelegateAgentID,
		AgentName:        internalDelegateAgentName,
		ConversationID:   conversationID,
		ParentToolCallID: toolCallID,
		Workspace:        workspace,
		Prompt:           prompt,
		CurrentModelID:   currentModel,
		RiskTier:         domain.TrustLevelToRiskTierCap(domain.TrustTrusted),
		UpdatedAt:        now,
	}
	a.delegateRunsMu.Lock()
	if a.delegateRuns == nil {
		a.delegateRuns = map[string]*domain.AcpRun{}
	}
	a.delegateRuns[runID] = run
	starting := cloneDelegateRun(run)
	run.BeginRunning(now)
	running := cloneDelegateRun(run)
	a.delegateRunsMu.Unlock()
	return starting, running
}

// finishDelegateRun seals the public ACP-shaped snapshot after the hidden
// headless conversation has completed. The hidden transcript is copied only
// after runHeadlessTurnKind returns, so the UI receives the actual final
// assistant round rather than the first acknowledgement.
func (a *App) finishDelegateRun(runID, runConversationID, output string, runErr error) *domain.AcpRun {
	a.delegateRunsMu.Lock()
	defer a.delegateRunsMu.Unlock()
	run := a.delegateRuns[runID]
	if run == nil {
		return nil
	}
	if runConversationID != "" && a.Conversations != nil {
		if conversation, err := a.Conversations.Get(runConversationID); err == nil {
			run.Transcript = delegateTranscriptFromConversation(conversation)
		}
	}
	if len(run.Transcript) == 0 && strings.TrimSpace(output) != "" {
		run.AppendTranscript(domain.AcpTranscriptChunk{
			Kind: "text",
			Text: output,
			At:   clock.NewTime().Time(),
		})
	}
	now := clock.NewTime().Time()
	if runErr != nil {
		run.AppendTranscript(domain.AcpTranscriptChunk{
			Kind: "status",
			Text: "Delegate failed: " + runErr.Error(),
			At:   now,
		})
		run.Finish(domain.AcpRunFailed, runErr.Error(), "error", now)
	} else {
		run.Finish(domain.AcpRunCompleted, "", "completed", now)
	}
	return cloneDelegateRun(run)
}

func (a *App) updateDelegateRunTranscript(runID, conversationID string) {
	if a.Conversations == nil || conversationID == "" {
		return
	}
	conversation, err := a.Conversations.Get(conversationID)
	if err != nil {
		return
	}
	transcript := delegateTranscriptFromConversation(conversation)
	a.delegateRunsMu.Lock()
	run := a.delegateRuns[runID]
	if run == nil || !run.Live() {
		a.delegateRunsMu.Unlock()
		return
	}
	run.Transcript = transcript
	run.UpdatedAt = clock.NewTime().Time()
	snapshot := cloneDelegateRun(run)
	a.delegateRunsMu.Unlock()
	a.emitAcpRun(contracts.EventAcpRunUpdated, snapshot)
}

func delegateTranscriptFromConversation(conversation *domain.Conversation) []domain.AcpTranscriptChunk {
	if conversation == nil {
		return nil
	}
	builder := &domain.AcpRun{}
	appendText := func(kind, value string, at time.Time) {
		if value == "" {
			return
		}
		builder.AppendTranscript(domain.AcpTranscriptChunk{Kind: kind, Text: value, At: at})
	}
	appendTools := func(calls []domain.ToolCall, at time.Time) {
		for _, call := range calls {
			builder.AppendTranscript(domain.AcpTranscriptChunk{
				Kind:       "tool",
				Text:       call.Output,
				ToolID:     call.ID,
				ToolTitle:  call.Name,
				ToolKind:   call.Name,
				ToolStatus: delegateTranscriptToolStatus(call.Status),
				At:         at,
			})
		}
	}
	for _, message := range conversation.Messages {
		if message.Role != domain.RoleAssistant || domain.IsHydrationMessage(message) {
			continue
		}
		if len(message.Steps) > 0 {
			for _, step := range message.Steps {
				switch step.Type {
				case domain.StepReasoning:
					appendText("thought", step.Content, message.CreatedAt)
				case domain.StepText:
					appendText("text", step.Content, message.CreatedAt)
				case domain.StepToolCalls:
					appendTools(step.ToolCalls, message.CreatedAt)
				}
			}
			continue
		}
		appendText("thought", message.Reasoning, message.CreatedAt)
		appendText("text", message.Content, message.CreatedAt)
		appendTools(message.ToolCalls, message.CreatedAt)
	}
	return builder.Transcript
}

func delegateTranscriptToolStatus(status domain.ToolCallStatus) string {
	switch status {
	case domain.ToolRunning:
		return "running"
	case domain.ToolFailed:
		return "failed"
	case domain.ToolInterrupted:
		return "cancelled"
	case domain.ToolOK:
		return "completed"
	default:
		return ""
	}
}

func cloneDelegateRun(run *domain.AcpRun) *domain.AcpRun {
	if run == nil {
		return nil
	}
	cloned := *run
	cloned.AvailableModes = append([]domain.AcpMode(nil), run.AvailableModes...)
	cloned.Transcript = append([]domain.AcpTranscriptChunk(nil), run.Transcript...)
	if run.PendingPermission != nil {
		permission := *run.PendingPermission
		permission.Paths = append([]string(nil), run.PendingPermission.Paths...)
		permission.Options = append([]domain.AcpPermissionOption(nil), run.PendingPermission.Options...)
		cloned.PendingPermission = &permission
	}
	return &cloned
}

func (a *App) delegateRunSnapshot(runID string) (*domain.AcpRun, bool) {
	a.delegateRunsMu.RLock()
	run, ok := a.delegateRuns[runID]
	cloned := cloneDelegateRun(run)
	a.delegateRunsMu.RUnlock()
	return cloned, ok
}

func (a *App) delegateRunList(conversationID string) []*domain.AcpRun {
	a.delegateRunsMu.RLock()
	defer a.delegateRunsMu.RUnlock()
	out := make([]*domain.AcpRun, 0, len(a.delegateRuns))
	for _, run := range a.delegateRuns {
		if conversationID != "" && run.ConversationID != conversationID {
			continue
		}
		out = append(out, cloneDelegateRun(run))
	}
	return out
}

// completeDelegateRunLocked updates the original `delegate` tool call to
// its brief terminal state and injects a synthetic assistant message
// carrying the `delegate_result` tool call with the full output
// pre-filled. Callers hold the conversation turn lock (deliverRunDone or
// the turn-boundary drain).
func (a *App) completeDelegateRunLocked(conversationID, runID, toolCallID string, status domain.ToolCallStatus, output, runConvID string) error {
	repo, err := a.loadRepo(conversationID)
	if err != nil {
		a.log("error", "delegate", "completeDelegateRun: conversation %s not found: %v", conversationID, err)
		return err
	}
	conv := repo.Conversation()
	toolArgs := toolpresentation.ToolCallArgsFromConversation(conv, toolCallID)
	brief := domain.DelegateBriefResult(runID, status == domain.ToolOK)
	if toolCallID != "" {
		conv = a.updateToolResult(conv, "", toolCallID, status, brief, nil)
	}
	if err := repo.Add(domain.RoleAssistant, a.delegateResultMessage(runID, status, output, runConvID)); err != nil {
		a.log("error", "delegate", "completeDelegateRun: add result failed: %v", err)
		return err
	}
	if err := repo.Save(); err != nil {
		a.log("error", "delegate", "completeDelegateRun: save failed: %v", err)
		return err
	}
	parentRunID := ""
	if parentRun := a.activeRunForConversation(conversationID); parentRun != nil {
		parentRunID = parentRun.ID
	}
	if toolCallID != "" {
		a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
			RunID:          parentRunID,
			ConversationID: conversationID,
			ToolCallID:     toolCallID,
			Name:           domain.DelegateToolName,
			Status:         string(status),
			Args:           toolpresentation.ToolArgsRaw(toolArgs),
			Output:         brief,
			Presentation:   toolpresentation.BuildToolPresentation(domain.DelegateToolName, toolArgs, status, brief),
		})
	}
	return nil
}

// delegateResultMessage builds the synthetic assistant message carrying
// the `delegate_result` tool call with its result pre-filled. Mirrors
// subagentResultMessage: persisted into the conversation so the model
// processes the result like any freshly completed tool output.
func (a *App) delegateResultMessage(runID string, status domain.ToolCallStatus, output, runConvID string) domain.Message {
	return domain.Message{
		ID:        domain.NewID(domain.IDPrefixMsg),
		Role:      domain.RoleAssistant,
		CreatedAt: clock.NewTime().Time(),
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.DelegateResultPrefix + nonce.Random(),
			Name:   domain.DelegateResultToolName,
			Args:   domain.DelegateResultArgs(runID, runConvID),
			Status: status,
			Output: output,
		}},
	}
}
