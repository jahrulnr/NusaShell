package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

func TestExistingConversationAfterRestartUsesCurrentPromptAndTools(t *testing.T) {
	oldAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	conv := &domain.Conversation{
		ID:        "c_old_runtime",
		UpdatedAt: oldAt,
		Messages: []domain.Message{
			{ID: "u_old", Role: domain.RoleUser, Content: "old request", Status: domain.StatusDone},
			hydrationCheckpointMessage(),
			{ID: "a_old", Role: domain.RoleAssistant, Content: "old answer", Status: domain.StatusDone},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{conv.ID: conv}}
	settings := domain.DefaultSettings()
	settings.UserPrompt = "fresh runtime instruction"
	adapter := &freshTurnStreamAdapter{}
	app := &App{
		Conversations: store,
		Settings:      &fakeSettingsStore{settings: settings},
		Toolbox: &factoryStubToolbox{tools: []ToolInfo{{
			Name:        "new_runtime_tool",
			Description: "tool added by the current binary",
			InputSchema: map[string]any{"type": "object"},
		}}},
		Logs:      &fakeLogStore{},
		Bus:       NewBus(),
		startedAt: oldAt.Add(time.Hour),
		Factory: func(context.Context, *domain.Provider, string) (AIProvider, error) {
			return adapter, nil
		},
	}
	app.addTurnMessages(conv,
		domain.Message{ID: "u_new", Role: domain.RoleUser, Content: "new request", Status: domain.StatusDone},
		domain.Message{ID: "a_new", Role: domain.RoleAssistant},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.runTurn(&TurnRun{ID: "r_old_runtime", ConversationID: conv.ID, Ctx: ctx, Cancel: cancel},
		&domain.Provider{ID: "provider", Kind: domain.ProviderChat, Models: []domain.Model{{ID: "model", Context: 128000}}},
		"key", "model", "", "a_new", false, ModelCapabilities{})

	if adapter.first == nil {
		t.Fatal("existing conversation did not reach the provider")
	}
	foundTool := false
	for _, tool := range adapter.first.Tools {
		if tool.Name == "new_runtime_tool" {
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Fatalf("provider tools = %+v, want current new_runtime_tool", adapter.first.Tools)
	}
	var systemText strings.Builder
	for _, message := range adapter.first.Messages {
		if message.Role != core.RoleSystem {
			continue
		}
		for _, block := range message.Blocks {
			if text, ok := block.(core.TextBlock); ok {
				systemText.WriteString(text.Text)
			}
		}
	}
	if !strings.Contains(systemText.String(), "fresh runtime instruction") {
		t.Fatalf("provider system prompt = %q, want current UserPrompt", systemText.String())
	}

	saved, err := store.Get(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRestartAnnouncement(saved) {
		t.Fatal("existing room should receive the restart announcement")
	}
}

func hasRestartAnnouncement(conv *domain.Conversation) bool {
	for _, message := range conv.Messages {
		for _, call := range message.ToolCalls {
			if call.Name != domain.AnnouncementToolName {
				continue
			}
			if call.Output == domain.AnnouncementMessage {
				return true
			}
		}
	}
	return false
}
