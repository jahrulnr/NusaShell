package domain

// CompactionTriggerTokens is the estimated-token watermark that starts
// compaction. When CompactionThreshold is 0 (auto, the default), compaction
// triggers at 80% of the model's context window — so a 1M-context model
// compacts at ~800k, not at a flat 40k. When CompactionThreshold is non-zero,
// it is used as the trigger but still capped at 80% of the window so a high
// threshold cannot wait until the next turn already overflows.
func CompactionTriggerTokens(contextWindow int, settings Settings) int {
	trigger := settings.CompactionThreshold
	windowCap := contextWindow * 4 / 5
	if trigger <= 0 {
		// Auto: use 80% of the model's context window.
		if windowCap > 0 {
			return windowCap
		}
		return DefaultSettings().CompactionThreshold
	}
	if windowCap > 0 && windowCap < trigger {
		return windowCap
	}
	return trigger
}
