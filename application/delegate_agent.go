package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

	// The delegate runs on the parent conversation's model (same
	// capabilities), falling back to the first enabled provider.
	modelID, err := a.resolveDelegateModel(parentConvID)
	if err != nil {
		return "", err
	}

	runID := domain.NewID(domain.IDPrefixRun)
	toolCallID := ToolCallIDFromContext(ctx)

	// Track before spawning: the goroutine may finish before this call
	// returns, and deliverRunDone must find the pending run to untrack
	// and trigger the completion turn.
	a.trackPendingRun(parentConvID, runID)
	a.goSafe("delegate", func() {
		a.runDelegate(runID, toolCallID, parentConvID, workspace, prompt, modelID)
	})
	return fmt.Sprintf("Delegate run %s started; the result will be injected when it finishes.", runID), nil
}

// resolveDelegateModel picks the provider:model for a delegate run: the
// parent conversation's model when available, otherwise the first enabled
// provider with a model (mirrors headless resolution).
func (a *App) resolveDelegateModel(parentConvID string) (string, error) {
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
	output, runConvID, err := a.runHeadlessTurnKind(ctx, prompt, modelID, domain.TrustTrusted, nil, AgentDelegate)
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
	a.deliverRunDone(parentConvID, pendingRunDone{
		RunID: runID,
		Complete: func(cid string) error {
			return a.completeDelegateRunLocked(cid, runID, toolCallID, status, text, runConvID)
		},
	})
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
	toolArgs := toolCallArgsFromConversation(conv, toolCallID)
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
			Args:           toolArgsRaw(toolArgs),
			Output:         brief,
			Presentation:   buildToolPresentation(domain.DelegateToolName, toolArgs, status, brief),
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
