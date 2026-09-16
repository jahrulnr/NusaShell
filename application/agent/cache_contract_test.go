package agent

import (
	"testing"

	"nusashell/domain"
)

func TestConversationRulesPromptCacheFollowsRoundToolContract(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.MaxToolRounds = 1
	rules := &conversationRules{
		run:      &TurnRun{ID: "run_1", ConversationID: "conv_1"},
		conv:     &domain.Conversation{ID: "conv_1"},
		settings: settings,
		provider: &domain.Provider{
			ID:       "openai",
			Kind:     domain.ProviderChat,
			Driver:   domain.ProviderDriverOpenAI,
			BaseURL:  "https://api.openai.com/v1",
			CacheTTL: "30m",
		},
		model: "gpt-5",
		toolDefs: []ToolDef{{
			Name:        "subagent",
			Description: "delegate to enabled subagents",
			InputSchema: map[string]any{"type": "object"},
		}},
	}

	withTools := rules.promptCacheForTools(rules.toolsForRound())
	rules.toolRounds = settings.MaxToolRounds
	withoutTools := rules.promptCacheForTools(rules.toolsForRound())

	if withTools == nil || withoutTools == nil {
		t.Fatal("expected prompt cache policies for both round contracts")
	}
	if withTools.Key == withoutTools.Key {
		t.Fatalf("round with tools and final round without tools reused cache key %q", withTools.Key)
	}
}
