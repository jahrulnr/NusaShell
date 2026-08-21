package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

func TestBuildSystemPromptWithoutUserPrompt(t *testing.T) {
	c := &domain.Conversation{ID: "c1"}
	const sentinel = "SENTINEL_INSTRUCTION_9b2c"
	empty := buildSystemPrompt(c, "")
	if strings.Contains(empty, sentinel) {
		t.Fatal("empty user prompt injected unexpected content")
	}
	wrapped := buildSystemPrompt(c, sentinel)
	if !strings.Contains(wrapped, "<user_instructions>\n"+sentinel+"\n</user_instructions>") {
		t.Error("non-empty user prompt should be wrapped in <user_instructions> tags")
	}
}

func TestBuildSystemPromptWithUserPrompt(t *testing.T) {
	c := &domain.Conversation{ID: "c1"}
	prompt := buildSystemPrompt(c, "Always respond in Indonesian.")
	if !strings.Contains(prompt, "<user_instructions>") {
		t.Error("system prompt should contain <user_instructions> open tag")
	}
	if !strings.Contains(prompt, "</user_instructions>") {
		t.Error("system prompt should contain </user_instructions> close tag")
	}
	if !strings.Contains(prompt, "Always respond in Indonesian.") {
		t.Error("system prompt should contain the user prompt text")
	}
}

func TestBuildSystemPromptUserPromptPlacement(t *testing.T) {
	c := &domain.Conversation{
		ID:        "c1",
		Workspace: "/tmp/project",
		Messages: []domain.Message{
			{Role: domain.RoleSystem, Content: "SYSTEM_MSG_MARKER"},
		},
	}
	prompt := buildSystemPrompt(c, "USER_PROMPT_MARKER")
	userPromptIdx := strings.Index(prompt, "USER_PROMPT_MARKER")
	systemMsgIdx := strings.Index(prompt, "SYSTEM_MSG_MARKER")
	workspaceIdx := strings.Index(prompt, "/tmp/project")
	if userPromptIdx == -1 || systemMsgIdx == -1 || workspaceIdx == -1 {
		t.Fatal("markers not found in prompt")
	}
	if userPromptIdx > systemMsgIdx {
		t.Error("user prompt should appear before system messages")
	}
	if systemMsgIdx > workspaceIdx {
		t.Error("system messages should appear before workspace")
	}
}

func TestBuildSystemPromptWhitespaceUserPromptIgnored(t *testing.T) {
	c := &domain.Conversation{ID: "c1"}
	before := buildSystemPrompt(c, "")
	after := buildSystemPrompt(c, "   \n\t  ")
	if after != before {
		t.Error("whitespace-only user prompt should be ignored entirely")
	}
}
