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

// SlowDown: 0 = off (default). Negative values (stale -1 from JSON
// omitempty on older files) reset to off; values above the 60s cap are
// clamped so a typo cannot stall a turn for minutes.
func TestNormalizeSettingsClampsSlowDown(t *testing.T) {
	unset := NormalizeSettings(Settings{})
	if unset.SlowDown != 0 {
		t.Fatalf("SlowDown = %d, want 0 (off by default)", unset.SlowDown)
	}
	neg := NormalizeSettings(Settings{SlowDown: -1})
	if neg.SlowDown != 0 {
		t.Fatalf("SlowDown = %d, want 0 (negative resets to off)", neg.SlowDown)
	}
	ok := NormalizeSettings(Settings{SlowDown: 3})
	if ok.SlowDown != 3 {
		t.Fatalf("SlowDown = %d, want 3 (preserved)", ok.SlowDown)
	}
	over := NormalizeSettings(Settings{SlowDown: 3600})
	if over.SlowDown != 60 {
		t.Fatalf("SlowDown = %d, want 60 (clamped to cap)", over.SlowDown)
	}
}

// Explicit user intent persists verbatim; unrecognized values reset to empty
// (not to a concrete default) for the same reason.

func TestNormalizeSettingsCompactionWorkflowDefaultsAndEnforcesReuseModel(t *testing.T) {
	if got := DefaultSettings().CompactionWorkflow; got != CompactionWorkflowDedicated {
		t.Fatalf("default compaction workflow = %q, want %q", got, CompactionWorkflowDedicated)
	}
	if got := NormalizeSettings(Settings{}).CompactionWorkflow; got != CompactionWorkflowDedicated {
		t.Fatalf("unset compaction workflow = %q, want %q", got, CompactionWorkflowDedicated)
	}
	if got := NormalizeSettings(Settings{CompactionWorkflow: "unknown"}).CompactionWorkflow; got != CompactionWorkflowDedicated {
		t.Fatalf("unknown compaction workflow = %q, want %q", got, CompactionWorkflowDedicated)
	}
	got := NormalizeSettings(Settings{
		CompactionWorkflow: CompactionWorkflowReuse,
		CompactionModel:    "other:model",
	})
	if got.CompactionWorkflow != CompactionWorkflowReuse {
		t.Fatalf("reuse workflow normalized to %q", got.CompactionWorkflow)
	}
	if got.CompactionModel != "" {
		t.Fatalf("reuse workflow kept compaction model override %q", got.CompactionModel)
	}
}
