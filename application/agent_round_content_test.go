package application

import (
	"testing"

	"nusashell/domain"
)

func TestApplyStreamRoundDropsWhitespaceOnlyContent(t *testing.T) {
	for _, raw := range []string{"\n\n", "\n\n\n\n", "  \n"} {
		msg := &domain.Message{ID: "a1", Role: domain.RoleAssistant}
		applyStreamRound(msg, "qwen", streamedTurnRound{Content: raw})
		if msg.Content != "" {
			t.Fatalf("Content = %q from %q, want empty", msg.Content, raw)
		}
		if len(msg.Steps) != 0 {
			t.Fatalf("Steps = %+v, want none for whitespace-only content", msg.Steps)
		}
	}
}

func TestApplyStreamRoundTrimsLeadingTrailingNewlines(t *testing.T) {
	msg := &domain.Message{ID: "a1", Role: domain.RoleAssistant}
	applyStreamRound(msg, "qwen", streamedTurnRound{Content: "\n\nNow update main.go:\n\n"})
	if msg.Content != "Now update main.go:" {
		t.Fatalf("Content = %q, want trimmed text", msg.Content)
	}
}

// TestApplyStreamRoundKeepsTrailingSpace pins partial-stream fidelity: a
// stream cut mid-sentence persists its trailing space. The continuation
// round starts a new message, and the space is the word boundary between
// the two — stripping it runs words together ("here.And").
func TestApplyStreamRoundKeepsTrailingSpace(t *testing.T) {
	msg := &domain.Message{ID: "a1", Role: domain.RoleAssistant}
	applyStreamRound(msg, "model", streamedTurnRound{Content: "The answer starts here. "})
	if msg.Content != "The answer starts here. " {
		t.Fatalf("Content = %q, want trailing space preserved", msg.Content)
	}
	if len(msg.Steps) != 1 || msg.Steps[0].Content != "The answer starts here. " {
		t.Fatalf("Steps = %+v, want the same trailing-space text", msg.Steps)
	}
}
