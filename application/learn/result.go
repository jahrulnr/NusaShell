package learn

import (
	"strings"

	"nusashell/domain"
)

const learnerResultToolName = "learn"

func LearnerTurnOutput(conv *domain.Conversation, assistantText string) string {
	if extracted := extractLearnerToolResult(conv); extracted != "" {
		return extracted
	}
	return strings.TrimSpace(assistantText)
}

func extractLearnerToolResult(conv *domain.Conversation) string {
	if conv == nil {
		return ""
	}
	var last string
	for _, m := range conv.Messages {
		for _, tc := range m.ToolCalls {
			if args := acceptedLearnerToolArgs(tc); args != "" {
				last = args
			}
		}
	}
	return last
}

func acceptedLearnerToolArgs(tc domain.ToolCall) string {
	if tc.Name != learnerResultToolName {
		return ""
	}
	switch tc.Status {
	case domain.ToolFailed, domain.ToolInterrupted, domain.ToolRunning:
		return ""
	}
	args := strings.TrimSpace(tc.Args)
	if args == "" || ParseLearnerResult(args) == nil {
		return ""
	}
	return args
}

// ValidLearnerResult reports whether args are a typed learn() payload.
func ValidLearnerResult(args string) bool {
	return ParseLearnerResult(args) != nil
}
