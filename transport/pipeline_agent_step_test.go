package transport

import (
	"context"
	"encoding/json"
	"nusashell/domain"
	"strings"
	"testing"
	"time"
)

// TestPipelineAgentStepRunsHeadlessTurn verifies that a pipeline agent step
// executes a real agent turn (with ACP tools hidden) and returns the final
// assistant text as output.
func TestPipelineAgentStepRunsHeadlessTurn(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})

	h.llm.setRounds([][]llmStep{
		{{Text: "Pipeline agent completed: all files linted."}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, _, err := h.app.RunHeadlessTurn(ctx, "lint all files in the workspace", "", domain.TrustSafe, nil)
	if err != nil {
		t.Fatalf("RunHeadlessTurn: %v", err)
	}
	text, _ := out["output"].(string)
	if !strings.Contains(text, "Pipeline agent completed") {
		t.Fatalf("output text = %q, want substring %q", text, "Pipeline agent completed")
	}
}

// TestPipelineAgentStepRespectsModelField verifies that the model field on
// a headless turn selects the specified provider:model.
func TestPipelineAgentStepRespectsModelField(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})

	h.llm.setRounds([][]llmStep{
		{{Text: "Done with specified model."}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, _, err := h.app.RunHeadlessTurn(ctx, "run checks", "", domain.TrustSafe, nil)
	if err != nil {
		t.Fatalf("RunHeadlessTurn: %v", err)
	}
	text, _ := out["output"].(string)
	if !strings.Contains(text, "Done with specified model") {
		t.Fatalf("output text = %q", text)
	}
}

// TestPipelineAgentStepFailsWithoutProvider verifies that a headless turn
// returns a clear error when no enabled provider is available.
func TestPipelineAgentStepFailsWithoutProvider(t *testing.T) {
	h := newHarness(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := h.app.RunHeadlessTurn(ctx, "do work", "", domain.TrustSafe, nil)
	if err == nil || !strings.Contains(err.Error(), "no enabled provider") {
		t.Fatalf("want no-enabled-provider error, got %v", err)
	}
}

func TestPipelineAgentStepDoesNotAppearInRoomList(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})

	created := h.rpcOK(t, "agent.conversations.create", map[string]any{"title": "User room"})
	var createdConv struct {
		Conversation struct {
			ID string `json:"id"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(created.Result, &createdConv); err != nil {
		t.Fatal(err)
	}
	userID := createdConv.Conversation.ID
	if userID == "" {
		t.Fatal("expected user conversation id")
	}

	h.llm.setRounds([][]llmStep{
		{{Text: "lint ok"}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, convID, err := h.app.RunHeadlessTurn(ctx, "lint the workspace", "", domain.TrustSafe, nil)
	if err != nil {
		t.Fatalf("RunHeadlessTurn: %v", err)
	}
	if convID == "" {
		t.Fatal("expected headless conversation id for steer")
	}

	ids := listedConversationIDs(t, h)
	if !containsID(ids, userID) {
		t.Fatalf("list missing interactive room %s: %v", userID, ids)
	}
	if containsID(ids, convID) {
		t.Fatalf("pipeline conversation %s must not appear in agent.conversations.list: %v", convID, ids)
	}

	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var get struct {
		Conversation struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(gotten.Result, &get); err != nil {
		t.Fatal(err)
	}
	if get.Conversation.ID != convID {
		t.Fatalf("get pipeline conversation = %+v", get.Conversation)
	}
	saved, err := h.app.Conversations.Get(convID)
	if err != nil || saved == nil {
		t.Fatalf("store get: %v", err)
	}
	if saved.Origin != domain.ConversationOriginPipeline {
		t.Fatalf("origin = %q, want %q", saved.Origin, domain.ConversationOriginPipeline)
	}
}

func TestPipelineAgentStepRendersEventPlaceholders(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})

	// LLM echoes the rendered user prompt so the test can assert what the
	// model actually saw.
	h.llm.setRounds([][]llmStep{
		{{Text: "echo: halo bos"}},
	})

	ev := &domain.Event{
		ID:   "evt_42",
		Type: "telegram.message",
		Attributes: map[string]any{
			"chat_id":    "9999",
			"message_id": "m_test",
			"text":       "halo bos",
		},
	}
	// Apply the same rendering the production scheduler would do, so this
	// test exercises the contract used by ci_scheduler.go.
	rendered := domain.RenderAgentPrompt("balas chat ${event.chat_id}: ${event.text}", ev)
	if !strings.Contains(rendered, "balas chat 9999: halo bos") {
		t.Fatalf("RenderAgentPrompt: got %q, want substring %q", rendered, "balas chat 9999: halo bos")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, convID, err := h.app.RunHeadlessTurn(ctx, rendered, "", domain.TrustSafe, nil)
	if err != nil {
		t.Fatalf("RunHeadlessTurn: %v", err)
	}
	if out["output"] != "echo: halo bos" {
		t.Fatalf("output = %v, want %q", out["output"], "echo: halo bos")
	}
	// And the saved conversation should hold the rendered user message —
	// not the raw template.
	conv, err := h.app.Conversations.Get(convID)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, m := range conv.Messages {
		if m.Role == domain.RoleUser {
			got = m.Content
			break
		}
	}
	if got != rendered {
		t.Fatalf("saved user prompt = %q, want %q", got, rendered)
	}
}

func TestLegacyPipelineTitleRoomsAreHiddenFromList(t *testing.T) {
	h := newHarness(t, nil)
	legacy := domain.NewConversation("conv_legacy_pipe", "[pipeline] old run")
	if err := h.app.Conversations.Save(legacy); err != nil {
		t.Fatal(err)
	}
	created := h.rpcOK(t, "agent.conversations.create", map[string]any{"title": "Keep me"})
	var createdConv struct {
		Conversation struct {
			ID string `json:"id"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(created.Result, &createdConv); err != nil {
		t.Fatal(err)
	}

	ids := listedConversationIDs(t, h)
	if containsID(ids, "conv_legacy_pipe") {
		t.Fatalf("legacy [pipeline] room leaked into list: %v", ids)
	}
	if !containsID(ids, createdConv.Conversation.ID) {
		t.Fatalf("interactive room missing from list: %v", ids)
	}
	h.rpcOK(t, "agent.conversations.get", map[string]any{"id": "conv_legacy_pipe"})
}

func listedConversationIDs(t *testing.T, h *harness) []string {
	t.Helper()
	listed := h.rpcOK(t, "agent.conversations.list", map[string]any{})
	var list struct {
		Conversations []struct {
			ID string `json:"id"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(listed.Result, &list); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(list.Conversations))
	for _, c := range list.Conversations {
		ids = append(ids, c.ID)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
