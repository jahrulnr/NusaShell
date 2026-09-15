package agent

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

func TestResolveConversationProviderPrefersQualifiedConversationModel(t *testing.T) {
	codex := &domain.Provider{ID: "codex", Name: "Codex", Kind: domain.ProviderCodex, Enabled: true}
	openAI := &domain.Provider{ID: "openai", Name: "OpenAI", Kind: domain.ProviderResponses, Enabled: true}
	service := New(Deps{
		ResolveModel: func(model string) (*domain.Provider, string, string, *contracts.RPCError) {
			switch model {
			case "codex:gpt-5.6-terra":
				return codex, "gpt-5.6-terra", "codex-token", nil
			case "gpt-5.6-terra":
				return openAI, "gpt-5.6-terra", "openai-token", nil
			default:
				return nil, "", "", &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown model"}
			}
		},
	})

	conversation := &domain.Conversation{
		ID:    "conv-codex",
		Model: "codex:gpt-5.6-terra",
		Messages: []domain.Message{
			{Role: domain.RoleAssistant, Model: "gpt-5.6-terra", Status: domain.StatusDone},
		},
	}

	provider, model, key, _, err := service.ResolveConversationProvider(conversation)
	if err != nil {
		t.Fatalf("ResolveConversationProvider: %v", err)
	}
	if provider != codex {
		t.Fatalf("provider = %v, want Codex", provider)
	}
	if model != "gpt-5.6-terra" {
		t.Fatalf("model = %q, want bare Codex model", model)
	}
	if key != "codex-token" {
		t.Fatalf("key = %q, want Codex credential", key)
	}
}
