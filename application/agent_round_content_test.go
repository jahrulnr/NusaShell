package application

import (
	"testing"

	"nusashell/domain"
)

func TestApplyStreamRoundDropsWhitespaceOnlyContent(t *testing.T) {
	msg := &domain.Message{ID: "a1", Role: domain.RoleAssistant}
	applyStreamRound(msg, "qwen", streamedTurnRound{Content: "\n\n"})
	if msg.Content != "" {
		t.Fatalf("Content = %q, want empty", msg.Content)
	}
	if len(msg.Steps) != 0 {
		t.Fatalf("Steps = %+v, want none for whitespace-only content", msg.Steps)
	}
}

func TestApplyStreamRoundTrimsLeadingTrailingNewlines(t *testing.T) {
	msg := &domain.Message{ID: "a1", Role: domain.RoleAssistant}
	applyStreamRound(msg, "qwen", streamedTurnRound{Content: "\n\nNow update main.go:\n\n"})
	if msg.Content != "Now update main.go:" {
		t.Fatalf("Content = %q, want trimmed text", msg.Content)
	}
}
