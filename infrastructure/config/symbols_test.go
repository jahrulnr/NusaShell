package config

import "testing"

func TestSymbolsDefaults(t *testing.T) {
	cfg := NewConfig()
	if cfg.Symbols.BudgetTokens != 3000 || cfg.Symbols.Workers != 1 || cfg.Symbols.MaxFileBytes != 262144 {
		t.Fatalf("defaults: %+v", cfg.Symbols)
	}
}

func TestSymbolsNormalizeClamps(t *testing.T) {
	cfg := SymbolsConfig{BudgetTokens: 1, Workers: 0, MaxFileBytes: 3}
	cfg.Normalize()
	if cfg.BudgetTokens != 500 || cfg.Workers != 1 || cfg.MaxFileBytes != 16<<10 {
		t.Fatalf("clamps: %+v", cfg)
	}
	cfg = SymbolsConfig{BudgetTokens: 1 << 30, Workers: 99}
	cfg.Normalize()
	if cfg.BudgetTokens != 20000 || cfg.Workers != 8 {
		t.Fatalf("upper clamps: %+v", cfg)
	}
}
