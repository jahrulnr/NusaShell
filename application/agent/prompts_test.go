package agent

import (
	"strings"
	"testing"

	"nusashell/domain"
)

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
