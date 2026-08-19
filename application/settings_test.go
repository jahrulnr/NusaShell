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

// TestResolveMaxOutputCapsAtSettings: when the model advertises a very high
// max output (e.g. 1M tokens), the settings default acts as a ceiling to
// prevent sending absurdly high max_tokens that cause credit rejections on
// gateways like OpenRouter.
func TestResolveMaxOutputCapsAtSettings(t *testing.T) {
	provider := &domain.Provider{
		Models: []domain.Model{
			{ID: "deepseek-v4-flash", MaxOutput: 1048576},
		},
	}
	settings := domain.Settings{MaxOutputTokens: 65536}

	if got := resolveMaxOutput(provider, "deepseek-v4-flash", settings); got != 65536 {
		t.Fatalf("model with 1M output should be capped at settings default: got %d, want 65536", got)
	}

	// When the user raises the cap, the model's limit is still respected if lower
	settings.MaxOutputTokens = 2000000
	if got := resolveMaxOutput(provider, "deepseek-v4-flash", settings); got != 1048576 {
		t.Fatalf("model with 1M output and 2M cap: got %d, want 1048576", got)
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
	// Old flat default 40000 migrates to 0 (auto = 80% of model context).
	if normalized.CompactionThreshold != 0 {
		t.Fatalf("compaction_threshold = %d, want 0 (auto after migration)", normalized.CompactionThreshold)
	}
}

// TestNormalizeSettingsFillsMaxParallelTools: settings written before
// max_parallel_tools existed get the factory default (6), and out-of-range
// values are clamped into the valid 1–64 band.
func TestNormalizeSettingsFillsMaxParallelTools(t *testing.T) {
	// Unset (0) → default 6.
	zero := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8})
	if zero.MaxParallelTools != 6 {
		t.Fatalf("max_parallel_tools = %d, want 6 (default)", zero.MaxParallelTools)
	}
	// Negative → default 6.
	neg := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, MaxParallelTools: -1})
	if neg.MaxParallelTools != 6 {
		t.Fatalf("max_parallel_tools = %d, want 6 (default for negative)", neg.MaxParallelTools)
	}
	// In-range preserved.
	ok := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, MaxParallelTools: 12})
	if ok.MaxParallelTools != 12 {
		t.Fatalf("max_parallel_tools = %d, want 12 (preserved)", ok.MaxParallelTools)
	}
	// Above cap → clamped to 64.
	over := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, MaxParallelTools: 999})
	if over.MaxParallelTools != 64 {
		t.Fatalf("max_parallel_tools = %d, want 64 (clamped)", over.MaxParallelTools)
	}
}

// TestDefaultSettingsSoundNotificationsOn: the factory default has sound
// notifications enabled so the UI plays turn-complete/error cues without
// requiring the user to opt in.
func TestDefaultSettingsSoundNotificationsOn(t *testing.T) {
	s := domain.DefaultSettings()
	if !s.SoundNotifications {
		t.Fatal("default SoundNotifications = false, want true")
	}
}

// TestNormalizeSettingsPreservesSoundNotifications: NormalizeSettings must
// not reset SoundNotifications to the default when the field is explicitly
// false (user disabled it). Toggles use zero-value=false semantics, so we
// cannot distinguish "unset" from "intentionally false" — but the field is
// omitempty on the wire, so a settings file written by an older version
// simply omits it and NormalizeSettings leaves it as false. The UI treats
// false as "disabled" only when the DTO reports it; the default-on behavior
// is enforced by the frontend (`!== false` check) and by DefaultSettings
// for fresh installs.
func TestNormalizeSettingsPreservesSoundNotifications(t *testing.T) {
	disabled := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, SoundNotifications: false})
	if disabled.SoundNotifications != false {
		t.Fatal("NormalizeSettings should preserve SoundNotifications=false")
	}
	enabled := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, SoundNotifications: true})
	if !enabled.SoundNotifications {
		t.Fatal("NormalizeSettings should preserve SoundNotifications=true")
	}
}

// TestNormalizeSettingsPreservesUserPrompt: NormalizeSettings must not
// clear UserPrompt when it is set. An empty UserPrompt (unset) stays empty.
func TestNormalizeSettingsPreservesUserPrompt(t *testing.T) {
	withPrompt := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, UserPrompt: "Always respond in Indonesian."})
	if withPrompt.UserPrompt != "Always respond in Indonesian." {
		t.Fatalf("UserPrompt = %q, want preserved", withPrompt.UserPrompt)
	}
	withoutPrompt := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8})
	if withoutPrompt.UserPrompt != "" {
		t.Fatalf("UserPrompt = %q, want empty", withoutPrompt.UserPrompt)
	}
}
