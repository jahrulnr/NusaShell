package codex

import "testing"

// Ported from openai_models.rs resolved_context_window_* and
// model_context_window_limits_preserve_their_distinct_meanings.
func TestModelInfoWindowLimits(t *testing.T) {
	model := ModelInfo{
		ContextWindow:                 272_000,
		MaxContextWindow:              400_000,
		AutoCompactTokenLimit:         250_000,
		EffectiveContextWindowPercent: 95,
	}
	if resolved, ok := model.ResolvedContextWindow(); !ok || resolved != 272_000 {
		t.Fatalf("resolved = %d/%v, want 272000", resolved, ok)
	}
	if usable, ok := model.UsableContextWindow(); !ok || usable != 258_400 {
		t.Fatalf("usable = %d/%v, want 258400", usable, ok)
	}
	if limit, ok := model.AutoCompactTokenLimitValue(); !ok || limit != 244_800 {
		t.Fatalf("auto compact limit = %d/%v, want 244800 (min(250000, 90%% of 272000))", limit, ok)
	}
}

func TestModelInfoResolvedWindowFallsBackToMax(t *testing.T) {
	// Ported: fallback to max_context_window -> usable 380000 @95%, auto
	// compact 360000.
	model := ModelInfo{MaxContextWindow: 400_000, EffectiveContextWindowPercent: 95}
	if resolved, ok := model.ResolvedContextWindow(); !ok || resolved != 400_000 {
		t.Fatalf("resolved = %d/%v, want 400000", resolved, ok)
	}
	if usable, ok := model.UsableContextWindow(); !ok || usable != 380_000 {
		t.Fatalf("usable = %d/%v, want 380000", usable, ok)
	}
	if limit, ok := model.AutoCompactTokenLimitValue(); !ok || limit != 360_000 {
		t.Fatalf("auto compact limit = %d/%v, want 360000", limit, ok)
	}
}

func TestModelInfoDefaultEffectivePercentIs95(t *testing.T) {
	model := ModelInfo{ContextWindow: 100_000}
	if usable, ok := model.UsableContextWindow(); !ok || usable != 95_000 {
		t.Fatalf("usable = %d/%v, want 95000 with default percent", usable, ok)
	}
}

func TestModelInfoWithoutWindowUsesConfigLimitOnly(t *testing.T) {
	model := ModelInfo{AutoCompactTokenLimit: 100_000}
	if limit, ok := model.AutoCompactTokenLimitValue(); !ok || limit != 100_000 {
		t.Fatalf("auto compact limit = %d/%v, want 100000", limit, ok)
	}
	if _, ok := model.UsableContextWindow(); ok {
		t.Fatalf("usable window exists without metadata, want none")
	}
	none := ModelInfo{}
	if _, ok := none.AutoCompactTokenLimitValue(); ok {
		t.Fatalf("limit exists with neither window nor config, want none")
	}
}

// Ported from context_window.rs semantics: the Total branch always uses the
// model metadata limit; the trigger is inclusive (>=).
func TestTokenStatusTotalScopeInclusiveTrigger(t *testing.T) {
	model := ModelInfo{ContextWindow: 272_000, EffectiveContextWindowPercent: 95}
	cfg := ContextWindowConfig{Scope: ScopeTotal}

	justUnder := ContextWindowStatus(244_799, nil, model, cfg)
	if justUnder.TokenLimitReached {
		t.Fatalf("token_limit_reached at 244799, want false")
	}
	if justUnder.AutoCompactScopeLimit == nil || *justUnder.AutoCompactScopeLimit != 244_800 {
		t.Fatalf("scope limit = %v, want 244800", justUnder.AutoCompactScopeLimit)
	}
	if justUnder.FullContextWindowLimit == nil || *justUnder.FullContextWindowLimit != 258_400 {
		t.Fatalf("full cap = %v, want 258400", justUnder.FullContextWindowLimit)
	}
	if justUnder.BaseWindowTokensRemaining == nil || *justUnder.BaseWindowTokensRemaining != 1 {
		t.Fatalf("base remaining = %v, want min(1, 13601) = 1", justUnder.BaseWindowTokensRemaining)
	}

	exactly := ContextWindowStatus(244_800, nil, model, cfg)
	if !exactly.TokenLimitReached {
		t.Fatalf("token_limit_reached at exactly the limit, want true (>= is inclusive)")
	}
	if exactly.FullContextWindowLimitReached {
		t.Fatalf("full cap reached at 244800, want false")
	}
}

func TestTokenStatusTotalScopeHardCapForcesTrigger(t *testing.T) {
	// The full context window is a hard cap independent of the scope limit:
	// reaching it triggers compaction even when the scope limit is absent.
	model := ModelInfo{AutoCompactTokenLimit: 0, EffectiveContextWindowPercent: 50} // no window, no scope limit
	cfg := ContextWindowConfig{Scope: ScopeTotal}
	status := ContextWindowStatus(1_000, nil, model, cfg)
	if status.TokenLimitReached {
		t.Fatalf("token_limit_reached without any limit, want false (nil means disabled)")
	}

	withWindow := ModelInfo{ContextWindow: 1_000, EffectiveContextWindowPercent: 50}
	hardCap := ContextWindowStatus(500, nil, withWindow, cfg) // 1000*50/100 = 500
	if !hardCap.TokenLimitReached || !hardCap.FullContextWindowLimitReached {
		t.Fatalf("hard cap at 500 not reached: %+v", hardCap)
	}
}

func TestTokenStatusBodyAfterPrefixScope(t *testing.T) {
	model := ModelInfo{ContextWindow: 400_000, EffectiveContextWindowPercent: 95}
	cfg := ContextWindowConfig{Scope: ScopeBodyAfterPrefix}

	// Without a prefill baseline the scope falls back to the active context
	// itself, so the first-scope value is zero.
	first := ContextWindowStatus(1_000, nil, model, cfg)
	if first.AutoCompactScopeTokens != 0 {
		t.Fatalf("first scope tokens = %d, want 0", first.AutoCompactScopeTokens)
	}
	if first.TokenLimitReached {
		t.Fatalf("token_limit_reached on empty scope, want false")
	}

	// With a prefill baseline the scope counts growth after the prefix.
	prefill := int64(1_000)
	grown := ContextWindowStatus(1_500, &prefill, model, cfg)
	if grown.AutoCompactScopeTokens != 500 {
		t.Fatalf("scope tokens = %d, want 500", grown.AutoCompactScopeTokens)
	}
	if grown.AutoCompactWindowPrefillTokens == nil || *grown.AutoCompactWindowPrefillTokens != 1_000 {
		t.Fatalf("prefill echo = %v, want 1000", grown.AutoCompactWindowPrefillTokens)
	}

	// The config override applies explicitly on this branch (audit: the
	// Total branch uses model metadata directly instead).
	cfgWithOverride := ContextWindowConfig{Scope: ScopeBodyAfterPrefix, ModelAutoCompactTokenLimit: 400}
	over := ContextWindowStatus(1_500, &prefill, model, cfgWithOverride)
	if over.AutoCompactScopeLimit == nil || *over.AutoCompactScopeLimit != 400 {
		t.Fatalf("scope limit = %v, want config override 400", over.AutoCompactScopeLimit)
	}
	if !over.TokenLimitReached {
		t.Fatalf("token_limit_reached with scope 500 >= override 400, want true")
	}

	// A baseline larger than the active context saturates at zero instead of
	// going negative.
	bigPrefill := int64(9_999)
	saturated := ContextWindowStatus(1_500, &bigPrefill, model, cfg)
	if saturated.AutoCompactScopeTokens != 0 {
		t.Fatalf("saturated scope tokens = %d, want 0", saturated.AutoCompactScopeTokens)
	}
}

func TestTokenStatusFallbackBufferDelaysScopeTrigger(t *testing.T) {
	// The fallback buffer is only reserved when a token-budget config (a
	// fallback prompt) exists.
	model := ModelInfo{ContextWindow: 272_000, EffectiveContextWindowPercent: 95}
	base := ContextWindowConfig{Scope: ScopeTotal}
	buffered := ContextWindowConfig{
		Scope:       ScopeTotal,
		TokenBudget: &TokenBudgetConfig{FallbackBufferTokens: 1_000},
	}

	// 245700 is past the base scope limit (244800) but under the buffered
	// limit (245800) and under the full cap (258400): only the buffered
	// config stays untriggered.
	pastBase := ContextWindowStatus(245_700, nil, model, base)
	if !pastBase.TokenLimitReached {
		t.Fatalf("unbuffered config did not trigger past the base limit")
	}
	bufferedUnder := ContextWindowStatus(245_700, nil, model, buffered)
	if bufferedUnder.TokenLimitReached {
		t.Fatalf("buffered config triggered before the buffered limit, want delayed")
	}

	// Reaching the buffered limit exactly triggers (inclusive).
	bufferedAt := ContextWindowStatus(245_800, nil, model, buffered)
	if !bufferedAt.TokenLimitReached {
		t.Fatalf("buffered config did not trigger at the buffered limit")
	}
}

func TestTokenStatusBaseRemainingIsMinOfScopeAndFullCap(t *testing.T) {
	model := ModelInfo{ContextWindow: 272_000, EffectiveContextWindowPercent: 95}
	cfg := ContextWindowConfig{Scope: ScopeTotal}
	status := ContextWindowStatus(200_000, nil, model, cfg)
	// scope remaining: 244800-200000 = 44800; full remaining: 258400-200000
	// = 58400; min = 44800.
	if status.BaseWindowTokensRemaining == nil || *status.BaseWindowTokensRemaining != 44_800 {
		t.Fatalf("base remaining = %v, want 44800", status.BaseWindowTokensRemaining)
	}

	// With no limits at all there is nothing to report remaining against.
	noLimits := ContextWindowStatus(1_000, nil, ModelInfo{}, cfg)
	if noLimits.BaseWindowTokensRemaining != nil {
		t.Fatalf("base remaining = %v, want nil with no limits", noLimits.BaseWindowTokensRemaining)
	}
}

func TestTokenStatusRemainingNeverNegative(t *testing.T) {
	model := ModelInfo{ContextWindow: 1_000, EffectiveContextWindowPercent: 50}
	cfg := ContextWindowConfig{Scope: ScopeTotal}
	status := ContextWindowStatus(9_000, nil, model, cfg)
	if !status.TokenLimitReached {
		t.Fatalf("expected trigger far past the cap")
	}
	if status.BaseWindowTokensRemaining == nil || *status.BaseWindowTokensRemaining != 0 {
		t.Fatalf("base remaining = %v, want clamped 0", status.BaseWindowTokensRemaining)
	}
}

func TestScopeDefaultIsTotal(t *testing.T) {
	// The zero value of the scope must be Total (upstream #[default]).
	var cfg ContextWindowConfig
	if cfg.Scope != ScopeTotal {
		t.Fatalf("default scope = %v, want ScopeTotal", cfg.Scope)
	}
}
