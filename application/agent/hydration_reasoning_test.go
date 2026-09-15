package agent

import (
	"testing"

	"nusashell/domain"
)

func TestHydrationReasoningIsPersistedAndHidden(t *testing.T) {
	result := NewHydrationBuilder(HydrationSource{}).Build()
	if result.Messages[0].Reasoning == "" {
		t.Fatal("hydration assistant message must carry synthetic reasoning")
	}

	built := buildHydrationDomainMessages(result.Messages)
	if len(built) != 1 {
		t.Fatalf("persisted hydration messages = %d, want 1", len(built))
	}
	if built[0].Reasoning != result.Messages[0].Reasoning {
		t.Fatalf("persisted hydration reasoning = %q, want %q", built[0].Reasoning, result.Messages[0].Reasoning)
	}
	if !domain.IsHydrationMessage(built[0]) {
		t.Fatal("persisted hydration with synthetic reasoning must remain hidden")
	}
}

func TestHydrationReasoningOnlyReplaysWhenProviderRequiresIt(t *testing.T) {
	result := NewHydrationBuilder(HydrationSource{}).Build()
	persisted := buildHydrationDomainMessages(result.Messages)
	conversation := &domain.Conversation{Messages: append([]domain.Message{{
		ID: "u1", Role: domain.RoleUser, Content: "hi",
	}}, persisted...)}

	replayed := chatMessages(conversation, "", ModelCapabilities{ReasoningReplay: true})
	var replayedReasoning string
	for _, message := range replayed {
		if message.Role == "assistant" {
			replayedReasoning = message.Reasoning
			break
		}
	}
	if replayedReasoning == "" {
		t.Fatal("required reasoning replay must include hydration reasoning")
	}

	omitted := chatMessages(conversation, "", ModelCapabilities{ReasoningReplay: false})
	for _, message := range omitted {
		if message.Role == "assistant" && message.Reasoning != "" {
			t.Fatalf("optional hydration reasoning must be omitted, got %q", message.Reasoning)
		}
	}
}
