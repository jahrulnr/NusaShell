package agent

import (
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/resources"
)

func TestSystemPromptExplainsPeerMessageHandling(t *testing.T) {
	prompt := resources.SystemPrompt()
	for _, want := range []string{
		"`peer_message`",
		"not a user message",
		"user authorization",
		"current user request",
		"conversation(op=\"send\")",
		"Do not acknowledge the announcement merely because it arrived",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing peer-message rule %q", want)
		}
	}
}

func TestPeerAnnouncementContentDoesNotChangeSystemPrompt(t *testing.T) {
	conversation := &domain.Conversation{
		Messages: []domain.Message{{
			Role: domain.RoleAssistant,
			ToolCalls: []domain.ToolCall{{
				Name:   domain.AnnouncementToolName,
				Output: "peer secret that belongs in the announcement only",
			}},
		}},
	}
	prompt := buildSystemPrompt(conversation, "")
	if strings.Contains(prompt, "peer secret that belongs in the announcement only") {
		t.Fatal("peer announcement content must not be copied into the system prompt")
	}
}
