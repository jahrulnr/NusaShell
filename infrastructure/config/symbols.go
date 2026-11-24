package config

// SymbolsConfig tunes the symbol_map hydration record — the bounded
// workspace symbol index rendered before each provider call. All three
// knobs are cost controls; none switch the record off (it is present in
// every binary built with the repomap tag).
type SymbolsConfig struct {
	// BudgetTokens bounds the rendered map in ~4 chars/token units.
	BudgetTokens int `json:"budget_tokens" default:"3000"`
	// Workers is the parser concurrency an index build uses. One keeps a
	// cold build near 300 MB RSS; 2–4 shortens it at roughly +100 MB each.
	Workers int `json:"workers" default:"1"`
	// MaxFileBytes skips single files past this size — generated bundles
	// and minified sources dominate it.
	MaxFileBytes int `json:"max_file_bytes" default:"262144"`
}

// Normalize clamps each knob into the band the adapters are tuned for:
// a map below 500 tokens is noise, workers outside 1–8 either stall or
// blow the memory budget, and a byte cap under 16 KB would skip real
// sources while no ceiling invites pathological files.
func (c *SymbolsConfig) Normalize() {
	ApplyDefaults(c)
	if c.BudgetTokens < 500 {
		c.BudgetTokens = 500
	}
	if c.BudgetTokens > 20000 {
		c.BudgetTokens = 20000
	}
	if c.Workers < 1 {
		c.Workers = 1
	}
	if c.Workers > 8 {
		c.Workers = 8
	}
	if c.MaxFileBytes < 16<<10 {
		c.MaxFileBytes = 16 << 10
	}
}
