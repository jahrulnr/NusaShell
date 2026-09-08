package subagent

import (
	"fmt"
	"strings"
	"time"

	"nusashell/domain"
)

const (
	internalDelegateAgentID   = "internal"
	internalDelegateAgentName = "NusaShell delegate"
)

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
