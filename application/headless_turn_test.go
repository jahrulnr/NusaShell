package application

import (
	"testing"
	"time"

	"nusashell/contracts"
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

func TestHeadlessWorkspaceLearnerUsesDataDir(t *testing.T) {
	tests := []struct {
		name    string
		kind    AgentKind
		ctxWS   string
		dataDir string
		want    string
	}{
		{"learner with dataDir", AgentLearner, "/media/disk/project", "/home/u/.config/nusashell", "/home/u/.config/nusashell"},
		{"legacy learner alias", AgentMemoryConsolidator, "/proj", "/data/nusashell", "/data/nusashell"},
		{"learner without dataDir keeps ctx", AgentLearner, "/proj", "", "/proj"},
		{"automation keeps ctx workspace", AgentAutomation, "/proj", "/data/nusashell", "/proj"},
		{"delegate keeps ctx workspace", AgentDelegate, "/proj", "/data/nusashell", "/proj"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headlessWorkspace(tt.ctxWS, tt.kind, tt.dataDir); got != tt.want {
				t.Fatalf("headlessWorkspace(%q, %s, %q) = %q, want %q", tt.ctxWS, tt.kind, tt.dataDir, got, tt.want)
			}
		})
	}
}

func TestHeadlessTurnsDoNotBroadcastRoomCompletion(t *testing.T) {
	bus := NewBus()
	app := &App{Bus: bus}
	_, events, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	app.emitInteractiveTurnEvent(&TurnRun{Headless: true, ID: "run_learn"}, contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: "run_learn", ConversationID: "conv_learn",
	})
	select {
	case event := <-events:
		t.Fatalf("headless learning turn leaked %s to the UI bus", event.Type)
	case <-time.After(50 * time.Millisecond):
	}

	app.emitInteractiveTurnEvent(&TurnRun{Headless: false, ID: "run_chat"}, contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: "run_chat", ConversationID: "conv_chat",
	})
	select {
	case event := <-events:
		if event.Type != contracts.EventTurnDone {
			t.Fatalf("interactive turn event = %s, want %s", event.Type, contracts.EventTurnDone)
		}
	case <-time.After(time.Second):
		t.Fatal("interactive Agent rooms must still broadcast turn.done")
	}
}
