package agent

import (
	"context"
	"strings"
	"testing"

	"nusashell/application/provider"
	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/domain"
)

func TestResolveContextWindowUsesCodexModelMetadataAndPreservesOverrides(t *testing.T) {
	codex := &domain.Provider{
		ID:   "codex",
		Kind: domain.ProviderCodex,
		Models: []domain.Model{
			{ID: "model", Context: 1_050_000},
		},
	}
	settings := domain.Settings{MaxInputTokens: 200000}

	// Chat turns use the direct ChatGPT Codex Responses API. The local
	// app-server model cache is discovery metadata, not an agent runtime cap.
	svc := New(Deps{})
	if got := svc.ResolveContextWindow(codex, "model", settings); got != 1_050_000 {
		t.Fatalf("Codex model context window = %d, want catalog value 1050000", got)
	}

	learned := learnedparams.New(nil)
	if !learned.LearnTPMContextCap("codex", "model", 200000, "test") {
		t.Fatal("expected learned context cap to be recorded")
	}
	svc = New(Deps{LearnedParams: learned})
	if got := svc.ResolveContextWindow(codex, "model", settings); got != 200000 {
		t.Fatalf("learned context cap = %d, want 200000", got)
	}

	manualContext := 123456
	overrides := modeloverrides.New(nil)
	if err := overrides.Set(&domain.ModelOverride{Provider: "codex", Model: "model", Context: &manualContext}); err != nil {
		t.Fatalf("set manual context override: %v", err)
	}
	svc = New(Deps{
		LearnedParams:  learned,
		ModelOverrides: overrides,
	})
	if got := svc.ResolveContextWindow(codex, "model", settings); got != manualContext {
		t.Fatalf("manual context override = %d, want %d", got, manualContext)
	}

	nonCodex := &domain.Provider{
		ID:   "openai",
		Kind: domain.ProviderChat,
		Models: []domain.Model{
			{ID: "model", Context: 1_000_000},
		},
	}
	if got := svc.ResolveContextWindow(nonCodex, "model", settings); got != 1_000_000 {
		t.Fatalf("non-Codex context window = %d, want catalog value 1000000", got)
	}
}
func TestConversationRulesRequestEstimateMatchesProviderVisibleShape(t *testing.T) {
	svc := New(Deps{})
	run := &TurnRun{
		ID:             "run",
		ConversationID: "conv",
		ProviderID:     "responses",
		Ctx:            context.Background(),
	}
	conversation := &domain.Conversation{
		ID: "conv",
		Messages: []domain.Message{
			{ID: "user", Role: domain.RoleUser, Content: "hello", Status: domain.StatusDone},
		},
		CompactionBlob: `[ {"type":"compaction","encrypted_content":"opaque"} ]`,
	}
	providerMeta := &domain.Provider{
		ID:   "responses",
		Kind: domain.ProviderResponses,
		Models: []domain.Model{
			{ID: "model", Context: 400000},
		},
	}
	adapter := ProviderContext{Kind: domain.ProviderResponses, ReasoningSummary: "auto"}
	settings := domain.DefaultSettings()
	tools := []ToolDef{{
		Name:        "search",
		Description: strings.Repeat("tool description ", 20),
		InputSchema: map[string]any{"type": "object"},
	}}
	rules := svc.NewConversationRules(
		run, adapter, conversation, settings, providerMeta, "model", "auto", "assistant",
		ModelCapabilities{}, tools, 1024, nil, false, "",
	)

	request := ChatRequest{
		Model:                    "model",
		System:                   buildSystemPromptForRun(run, conversation, settings.UserPrompt),
		Messages:                 chatMessages(conversation, "assistant", ModelCapabilities{}),
		Tools:                    tools,
		PromptCaching:            settings.PromptCaching,
		MaxTokens:                1024,
		ReasoningSummary:         adapter.ReasoningSummary,
		CompactionBlob:           conversation.CompactionBlob,
		CompactionPrefixMessages: 0,
		ContextManagement:        serverCompactionContextManagementForKind("model", adapter.Kind),
	}
	want := provider.EstimateRequestTokens(request, adapter.Kind, adapter.OpenRouter)
	got := rules.requestEstimate(nil)
	if got != want {
		t.Fatalf("request estimate = %d, want provider estimate %d", got, want)
	}
	if got <= int64(conversation.EstimateTokens()) {
		t.Fatalf("provider-shaped estimate = %d, want it to include more than rough conversation estimate %d", got, conversation.EstimateTokens())
	}

	withoutBlob := *conversation
	withoutBlob.CompactionBlob = ""
	withoutBlobRules := svc.NewConversationRules(
		run, adapter, &withoutBlob, settings, providerMeta, "model", "auto", "assistant",
		ModelCapabilities{}, tools, 1024, nil, false, "",
	)
	if gotWithoutBlob := withoutBlobRules.requestEstimate(nil); gotWithoutBlob >= got {
		t.Fatalf("estimate without Responses compaction blob = %d, want less than %d", gotWithoutBlob, got)
	}
}
