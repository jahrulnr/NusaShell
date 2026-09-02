package application

import (
	"testing"

	"nusashell/domain"
)

func TestFinalHeadlessAssistantMessageUsesTheLastTurnMessage(t *testing.T) {
	messages := []domain.Message{
		{ID: "user-1", Role: domain.RoleUser, Content: "inspect the file"},
		{
			ID:      "assistant-1",
			Role:    domain.RoleAssistant,
			Content: "I will inspect the file first.",
			ToolCalls: []domain.ToolCall{{
				ID: "tool-1", Name: "file_read", Status: domain.ToolOK, Output: "contents",
			}},
		},
		{ID: "assistant-2", Role: domain.RoleAssistant, Content: "The file contains the final answer."},
	}

	message, ok := finalHeadlessAssistantMessage(messages, "assistant-2")
	if !ok {
		t.Fatal("final assistant message was not found")
	}
	if message.ID != "assistant-2" || message.Content != "The file contains the final answer." {
		t.Fatalf("final assistant message = %+v, want the last assistant round", message)
	}
}

func TestFinalHeadlessAssistantMessageKeepsAnEmptyFinalRoundEmpty(t *testing.T) {
	messages := []domain.Message{
		{ID: "assistant-1", Role: domain.RoleAssistant, Content: "preliminary text"},
		{ID: "assistant-2", Role: domain.RoleAssistant, Content: ""},
	}

	message, ok := finalHeadlessAssistantMessage(messages, "assistant-2")
	if !ok {
		t.Fatal("final assistant message was not found")
	}
	if message.ID != "assistant-2" || message.Content != "" {
		t.Fatalf("final assistant message = %+v, want the empty final round", message)
	}
}
