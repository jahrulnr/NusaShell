package domain

import "testing"

func TestNormalizeSettingsPreservesExistingValuesAndFillsNewRoundLimit(t *testing.T) {
	legacy := Settings{
		CompactionEnabled:   false,
		CompactionThreshold: 12000,
		PromptCaching:       false,
	}
	got := NormalizeSettings(legacy)
	if got.CompactionEnabled || got.PromptCaching || got.CompactionThreshold != 12000 {
		t.Fatalf("existing settings changed: %+v", got)
	}
	if got.MaxToolRounds != DefaultSettings().MaxToolRounds {
		t.Fatalf("MaxToolRounds = %d, want default %d", got.MaxToolRounds, DefaultSettings().MaxToolRounds)
	}
}
