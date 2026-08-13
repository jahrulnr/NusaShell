package application

import (
	"testing"

	"nusashell/domain"
)

// TestResolveMaxOutputPrefersModelAdvertisedLimit: when the model advertises
// its own max output, that value wins over the global settings default.
func TestResolveMaxOutputPrefersModelAdvertisedLimit(t *testing.T) {
	provider := &domain.Provider{
		Models: []domain.Model{
			{ID: "gpt-4o", MaxOutput: 16384},
			{ID: "no-limit-model"},
		},
	}
	settings := domain.Settings{MaxOutputTokens: 65536}

	if got := resolveMaxOutput(provider, "gpt-4o", settings); got != 16384 {
		t.Fatalf("model with advertised limit: got %d, want 16384", got)
	}
	if got := resolveMaxOutput(provider, "no-limit-model", settings); got != 65536 {
		t.Fatalf("model without advertised limit: got %d, want 65536 (settings default)", got)
	}
	if got := resolveMaxOutput(provider, "unknown-model", settings); got != 65536 {
		t.Fatalf("unknown model: got %d, want 65536 (settings default)", got)
	}
}

// TestNormalizeSettingsFillsMaxTokenDefaults: settings written before
// max_input_tokens/max_output_tokens existed get the factory defaults.
func TestNormalizeSettingsFillsMaxTokenDefaults(t *testing.T) {
	old := domain.Settings{
		CompactionEnabled:   true,
		CompactionThreshold: 40000,
		PromptCaching:       true,
		MaxToolRounds:       8,
	}
	normalized := domain.NormalizeSettings(old)
	if normalized.MaxInputTokens != 200000 {
		t.Fatalf("max_input_tokens = %d, want 200000", normalized.MaxInputTokens)
	}
	if normalized.MaxOutputTokens != 65536 {
		t.Fatalf("max_output_tokens = %d, want 65536", normalized.MaxOutputTokens)
	}
}
