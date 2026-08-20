package domain

// CompactionTriggerTokens is the estimated-token watermark that starts
// compaction. When CompactionThreshold is 0 (auto, the default), compaction
// triggers at 80% of the model's available input budget (contextWindow minus
// maxOutput) — so a 256k model with 64k output compacts at ~122k input, not
// ~205k, which would overflow once the output budget is added. When
// CompactionThreshold is non-zero, it is used as the trigger but still capped
// at 80% of the available budget so a high threshold cannot wait until the
// next turn already overflows.
func CompactionTriggerTokens(contextWindow, maxOutput int, settings Settings) int {
	available := contextWindow
	if maxOutput > 0 && maxOutput < contextWindow {
		available = contextWindow - maxOutput
	}
	trigger := settings.CompactionThreshold
	budgetCap := available * 4 / 5
	if trigger <= 0 {
		// Auto: use 80% of the available input budget.
		if budgetCap > 0 {
			return budgetCap
		}
		return DefaultSettings().CompactionThreshold
	}
	if budgetCap > 0 && budgetCap < trigger {
		return budgetCap
	}
	return trigger
}
