package codex

// AutoCompactTokenLimitScope selects which part of the active context is
// charged against the auto-compact limit (protocol/src/config_types.rs).
// The zero value is Total, matching the upstream default.
type AutoCompactTokenLimitScope int

const (
	// ScopeTotal counts the full active context against the limit.
	ScopeTotal AutoCompactTokenLimitScope = iota
	// ScopeBodyAfterPrefix counts sampled output and later growth after the
	// carried compaction-window prefix.
	ScopeBodyAfterPrefix
)

// DefaultEffectiveContextWindowPercent is the default percentage of the
// context window considered usable (openai_models.rs
// default_effective_context_window_percent).
const DefaultEffectiveContextWindowPercent = 95

// ModelInfo carries the context-window subset of the model metadata that
// drives auto-compaction (protocol/src/openai_models.rs ModelInfo). Zero
// values mean "not provided".
type ModelInfo struct {
	// ContextWindow is the model's context window; MaxContextWindow is the
	// fallback used when ContextWindow is absent.
	ContextWindow         int64
	MaxContextWindow      int64
	AutoCompactTokenLimit int64 // configured override; clamped to 90% of the window
	// EffectiveContextWindowPercent reserves headroom for prompts, tool
	// overhead, and output. Zero falls back to
	// DefaultEffectiveContextWindowPercent.
	EffectiveContextWindowPercent int64
}

// ResolvedContextWindow prefers ContextWindow, then MaxContextWindow.
func (m ModelInfo) ResolvedContextWindow() (int64, bool) {
	if m.ContextWindow > 0 {
		return m.ContextWindow, true
	}
	if m.MaxContextWindow > 0 {
		return m.MaxContextWindow, true
	}
	return 0, false
}

// effectivePercent resolves the usable-window percentage.
func (m ModelInfo) effectivePercent() int64 {
	if m.EffectiveContextWindowPercent > 0 {
		return m.EffectiveContextWindowPercent
	}
	return DefaultEffectiveContextWindowPercent
}

// UsableContextWindow returns the context available to inference after
// reserving headroom: window * percent / 100 (saturating).
func (m ModelInfo) UsableContextWindow() (int64, bool) {
	window, ok := m.ResolvedContextWindow()
	if !ok {
		return 0, false
	}
	return saturatingMul(window, m.effectivePercent()) / 100, true
}

// AutoCompactTokenLimitValue returns the model's auto-compaction token
// threshold: 90% of the resolved context window, clamped by the configured
// override when both exist. Without a context window, only the configured
// override applies; with neither, no limit exists and the scope-limit branch
// of the trigger decision is disabled.
func (m ModelInfo) AutoCompactTokenLimitValue() (int64, bool) {
	window, hasWindow := m.ResolvedContextWindow()
	if !hasWindow {
		if m.AutoCompactTokenLimit > 0 {
			return m.AutoCompactTokenLimit, true
		}
		return 0, false
	}
	contextLimit := window * 9 / 10
	if m.AutoCompactTokenLimit > 0 && m.AutoCompactTokenLimit < contextLimit {
		return m.AutoCompactTokenLimit, true
	}
	return contextLimit, true
}

// TokenBudgetConfig mirrors the token-budget config subset that the trigger
// formula reads: the fallback buffer reserved when there is a fallback
// prompt to spend it on.
type TokenBudgetConfig struct {
	FallbackBufferTokens int64
}

// ContextWindowConfig carries the user/model config subset used by the
// trigger decision (core/src/session/context_window.rs reads these from
// Config).
type ContextWindowConfig struct {
	// ModelAutoCompactTokenLimit overrides the auto-compact limit. Upstream
	// applies it explicitly on the BodyAfterPrefix branch only; the Total
	// branch always uses the model metadata limit.
	ModelAutoCompactTokenLimit int64
	Scope                      AutoCompactTokenLimitScope
	// TokenBudget enables the fallback buffer. A nil value means no fallback
	// prompt exists and no buffer is reserved.
	TokenBudget *TokenBudgetConfig
}

// ContextWindowTokenStatus is the pre-turn context window decision, ported
// from core/src/session/context_window.rs ContextWindowTokenStatus. The
// *int64 fields mirror upstream Option<i64>: nil means the branch is
// disabled, never "limit reached".
type ContextWindowTokenStatus struct {
	// ActiveContextTokens is the full active context usage, independent of
	// the configured auto-compact scope.
	ActiveContextTokens int64
	// AutoCompactScopeTokens is the usage counted against the scope limit.
	AutoCompactScopeTokens int64
	AutoCompactScopeLimit  *int64
	// FullContextWindowLimit is the hard cap: usable context window.
	FullContextWindowLimit *int64
	// BaseWindowTokensRemaining reports remaining tokens against the base
	// (unbuffered) window, capped by the full context: the minimum of the
	// available remaining values. It is a report, not an extra trigger
	// condition.
	BaseWindowTokensRemaining *int64
	// AutoCompactWindowPrefillTokens echoes the window prefill baseline when
	// the BodyAfterPrefix scope is active.
	AutoCompactWindowPrefillTokens *int64
	FullContextWindowLimitReached  bool
	// TokenLimitReached is the final inclusive (>=) decision:
	// (scope_tokens >= buffered scope limit) OR (active >= full cap).
	TokenLimitReached bool
}

// ContextWindowTokenStatus computes the pre-turn compaction decision. The
// thresholds are tested against the active state at check time: pending
// input that has not yet been recorded (such as a brand-new user turn) is
// intentionally not projected into this calculation, matching upstream
// run_pre_sampling_compact.
//
// prefillInputTokens is the compaction-window baseline; pass nil when no
// window prefill exists (the BodyAfterPrefix scope then falls back to the
// active context itself, making the first-scope value zero).
func ContextWindowStatus(activeContextTokens int64, prefillInputTokens *int64, model ModelInfo, cfg ContextWindowConfig) ContextWindowTokenStatus {
	status := ContextWindowTokenStatus{ActiveContextTokens: activeContextTokens}

	var scopeLimit *int64
	switch cfg.Scope {
	case ScopeTotal:
		status.AutoCompactScopeTokens = activeContextTokens
		if limit, ok := model.AutoCompactTokenLimitValue(); ok {
			scopeLimit = &limit
		}
	case ScopeBodyAfterPrefix:
		baseline := activeContextTokens
		if prefillInputTokens != nil {
			baseline = *prefillInputTokens
			status.AutoCompactWindowPrefillTokens = prefillInputTokens
		}
		status.AutoCompactScopeTokens = max64(saturatingSub(activeContextTokens, baseline), 0)
		if cfg.ModelAutoCompactTokenLimit > 0 {
			limit := cfg.ModelAutoCompactTokenLimit
			scopeLimit = &limit
		} else if limit, ok := model.AutoCompactTokenLimitValue(); ok {
			scopeLimit = &limit
		}
	}
	status.AutoCompactScopeLimit = scopeLimit

	// The usable context window is a hard cap, independent of the scope.
	if fullLimit, ok := model.UsableContextWindow(); ok {
		status.FullContextWindowLimit = &fullLimit
	}

	// Remaining against the base (unbuffered) window, capped by the full cap.
	var remainings []int64
	if scopeLimit != nil {
		remainings = append(remainings, tokensRemaining(*scopeLimit, status.AutoCompactScopeTokens))
	}
	if status.FullContextWindowLimit != nil {
		remainings = append(remainings, tokensRemaining(*status.FullContextWindowLimit, activeContextTokens))
	}
	if len(remainings) > 0 {
		remaining := remainings[0]
		for _, value := range remainings[1:] {
			remaining = min64(remaining, value)
		}
		status.BaseWindowTokensRemaining = &remaining
	}

	// Only reserve the fallback buffer when a fallback prompt can use it.
	fallbackBuffer := int64(0)
	if cfg.TokenBudget != nil {
		fallbackBuffer = max64(cfg.TokenBudget.FallbackBufferTokens, 0)
	}
	var bufferedLimit *int64
	if scopeLimit != nil {
		limit := saturatingAdd(*scopeLimit, fallbackBuffer)
		bufferedLimit = &limit
	}

	status.FullContextWindowLimitReached = status.FullContextWindowLimit != nil &&
		activeContextTokens >= *status.FullContextWindowLimit
	status.TokenLimitReached = (bufferedLimit != nil && status.AutoCompactScopeTokens >= *bufferedLimit) ||
		status.FullContextWindowLimitReached
	return status
}

func tokensRemaining(limit, used int64) int64 {
	return max64(saturatingSub(limit, used), 0)
}
