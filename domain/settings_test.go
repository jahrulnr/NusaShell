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

// Anti-stamping: an unset contract mode must STAY empty on disk so it keeps
// following the factory default at runtime. Normalization used to burn the
// then-current default into storage, freezing every saved config at whatever
// the default was when the user last saved (proven in the field: configs
// stamped "require" survived a factory-default change to "hint").
func TestNormalizeSettingsLeavesContractModeEmptyForFactoryDefault(t *testing.T) {
	got := NormalizeSettings(Settings{})
	if got.PluginContractMode != "" {
		t.Fatalf("PluginContractMode = %q, want empty (runtime resolves the factory default)", got.PluginContractMode)
	}
}

// Explicit user intent persists verbatim; unrecognized values reset to empty
// (not to a concrete default) for the same reason.
func TestNormalizeSettingsKeepsExplicitContractMode(t *testing.T) {
	for _, mode := range []string{PluginContractOff, PluginContractHint, PluginContractRequire} {
		if got := NormalizeSettings(Settings{PluginContractMode: mode}).PluginContractMode; got != mode {
			t.Fatalf("explicit %q rewritten to %q", mode, got)
		}
	}
	if got := NormalizeSettings(Settings{PluginContractMode: "yolo"}).PluginContractMode; got != "" {
		t.Fatalf("unknown mode must reset to empty, got %q", got)
	}
}
