package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// SpawnDelegate is the `delegate` tool implementation. conversationID and
// toolCallID are passed from the App wrapper (context.go stays in root).
func (s *Service) SpawnDelegate(ctx context.Context, conversationID, toolCallID string, argsJSON []byte) (string, error) {
	var args struct {
		Prompt    string `json:"prompt"`
		Title     string `json:"title"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	title := domain.NormalizeAcpRunTitle(args.Title)
	workspace := strings.TrimSpace(args.Workspace)
	if workspace == "" && conversationID != "" && s.deps.Conversations != nil {
		if conv, err := s.deps.Conversations.Get(conversationID); err == nil {
			workspace = conv.Workspace
		}
	}
	if workspace != "" && !domain.PathRooted(workspace) {
		return "", fmt.Errorf("workspace must be an absolute path")
	}

	modelID, err := s.ResolveDelegateModel(conversationID)
	if err != nil {
		return "", err
	}

	runID := domain.NewID(domain.IDPrefixRun)
	starting, running := s.RegisterDelegateRun(runID, toolCallID, conversationID, workspace, prompt, modelID, title)
	s.EmitRun(contracts.EventAcpRunStarted, starting)
	s.EmitRun(contracts.EventAcpRunUpdated, running)

	s.trackPending(conversationID, runID, domain.DelegateToolName)
	s.goSafe("delegate", func() {
		s.runDelegate(runID, toolCallID, conversationID, workspace, prompt, modelID)
	})
	return fmt.Sprintf("Delegate run %s started; the result will be injected when it finishes.", runID), nil
}

// ResolveDelegateModel picks the provider:model for a delegate run. An
// explicit Settings → Internal delegate model wins; the empty setting means
// inherit the parent conversation's model, otherwise ResolveModel chooses
// the first enabled provider with a model.
func (s *Service) ResolveDelegateModel(parentConvID string) (string, error) {
	if s.deps.Settings != nil {
		if configured := strings.TrimSpace(s.deps.Settings.Get().DelegateModel); configured != "" {
			return configured, nil
		}
	}
	if parentConvID != "" && s.deps.Conversations != nil {
		if conv, err := s.deps.Conversations.Get(parentConvID); err == nil && strings.TrimSpace(conv.Model) != "" {
			return conv.Model, nil
		}
	}
	if s.deps.ResolveModel == nil {
		return "", fmt.Errorf("no enabled provider with a model")
	}
	return s.deps.ResolveModel(parentConvID)
}

func (s *Service) runDelegate(runID, toolCallID, parentConvID, workspace, prompt, modelID string) {
	ctx := context.Background()
	if s.deps.Headless == nil {
		if run := s.FinishDelegateRun(runID, "", "", fmt.Errorf("delegate headless runner is not wired")); run != nil {
			s.PersistRun(run)
			s.EmitRun(contracts.EventAcpRunUpdated, run)
			s.EmitRun(contracts.EventAcpRunDone, run)
		}
		s.deliverDelegate(parentConvID, runID, toolCallID, domain.ToolFailed, "error: delegate headless runner is not wired", "")
		return
	}
	output, runConvID, err := s.deps.Headless(ctx, prompt, modelID, domain.TrustTrusted, nil,
		func(conversationID string) {
			s.UpdateDelegateRunTranscript(runID, conversationID)
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
	if run := s.FinishDelegateRun(runID, runConvID, text, err); run != nil {
		s.PersistRun(run)
		s.EmitRun(contracts.EventAcpRunUpdated, run)
		s.EmitRun(contracts.EventAcpRunDone, run)
	}
	s.deliverDelegate(parentConvID, runID, toolCallID, status, text, runConvID)
}

func (s *Service) deliverDelegate(parentConvID, runID, toolCallID string, status domain.ToolCallStatus, text, runConvID string) {
	if s.deps.DeliverRunDone == nil {
		return
	}
	s.deps.DeliverRunDone(parentConvID, runID, func(cid string) error {
		if s.deps.CompleteDelegate == nil {
			return nil
		}
		return s.deps.CompleteDelegate(cid, runID, toolCallID, status, text, runConvID)
	})
}

const (
	internalDelegateAgentID   = "internal"
	internalDelegateAgentName = "NusaShell delegate"
)

func (s *Service) RegisterDelegateRun(runID, toolCallID, conversationID, workspace, prompt, modelID, title string) (*domain.AcpRun, *domain.AcpRun) {
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
		Title:            domain.NormalizeAcpRunTitle(title),
		ConversationID:   conversationID,
		ParentToolCallID: toolCallID,
		Workspace:        workspace,
		Prompt:           prompt,
		CurrentModelID:   currentModel,
		RiskTier:         domain.TrustLevelToRiskTierCap(domain.TrustTrusted),
		UpdatedAt:        now,
	}
	s.delegateRunsMu.Lock()
	if s.delegateRuns == nil {
		s.delegateRuns = map[string]*domain.AcpRun{}
	}
	s.delegateRuns[runID] = run
	starting := cloneDelegateRun(run)
	run.BeginRunning(now)
	running := cloneDelegateRun(run)
	s.delegateRunsMu.Unlock()
	return starting, running
}

// FinishDelegateRun seals the public ACP-shaped snapshot after the hidden
// headless conversation has completed.
func (s *Service) FinishDelegateRun(runID, runConversationID, output string, runErr error) *domain.AcpRun {
	s.delegateRunsMu.Lock()
	defer s.delegateRunsMu.Unlock()
	run := s.delegateRuns[runID]
	if run == nil {
		return nil
	}
	if runConversationID != "" && s.deps.Conversations != nil {
		if conversation, err := s.deps.Conversations.Get(runConversationID); err == nil {
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

func (s *Service) UpdateDelegateRunTranscript(runID, conversationID string) {
	if s.deps.Conversations == nil || conversationID == "" {
		return
	}
	conversation, err := s.deps.Conversations.Get(conversationID)
	if err != nil {
		return
	}
	transcript := delegateTranscriptFromConversation(conversation)
	s.delegateRunsMu.Lock()
	run := s.delegateRuns[runID]
	if run == nil || !run.Live() {
		s.delegateRunsMu.Unlock()
		return
	}
	run.Transcript = transcript
	run.UpdatedAt = clock.NewTime().Time()
	snapshot := cloneDelegateRun(run)
	s.delegateRunsMu.Unlock()
	s.EmitRun(contracts.EventAcpRunUpdated, snapshot)
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

func (s *Service) DelegateRunSnapshot(runID string) (*domain.AcpRun, bool) {
	s.delegateRunsMu.RLock()
	run, ok := s.delegateRuns[runID]
	cloned := cloneDelegateRun(run)
	s.delegateRunsMu.RUnlock()
	return cloned, ok
}

func (s *Service) DelegateRunList(conversationID string) []*domain.AcpRun {
	s.delegateRunsMu.RLock()
	defer s.delegateRunsMu.RUnlock()
	out := make([]*domain.AcpRun, 0, len(s.delegateRuns))
	for _, run := range s.delegateRuns {
		if conversationID != "" && run.ConversationID != conversationID {
			continue
		}
		out = append(out, cloneDelegateRun(run))
	}
	return out
}
