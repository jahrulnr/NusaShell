//go:build repomap

package main

import (
	"path/filepath"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoconfig "github.com/jahrulnr/nusashell/infrastructure/repository/config"
	reposymbolmap "github.com/jahrulnr/nusashell/infrastructure/repository/symbolmap"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol/gotreesitter"
	toolsymbolmap "github.com/jahrulnr/nusashell/infrastructure/service/tool/symbolmap"
)

// wireSymbolMap registers the symbol_map hydration record. It is the one
// place the indexing engine is chosen: swapping gotreesitter for another
// SymbolIndex is this Options literal and nothing else. The manager is
// process-wide, so every room on a workspace shares one build and one
// on-disk cache; its absence under a lean build leaves no record behind.
func wireSymbolMap(tools map[string]dservice.Tool, configPath, storageDir string, store drepo.Workspace, root string) error {
	live := func() (config.SymbolsConfig, error) {
		cfg := config.NewConfig()
		if err := cfg.Load(repoconfig.NewLocal(), configPath); err != nil {
			return config.SymbolsConfig{}, err
		}
		return cfg.Symbols, nil
	}
	maps := symbol.NewManager(
		func(o symbol.Options) dservice.SymbolIndex {
			return gotreesitter.New(gotreesitter.Options{Workers: o.Workers, MaxFileBytes: o.MaxFileBytes})
		},
		func() symbol.Options {
			cfg, err := live()
			if err != nil {
				cfg = config.NewConfig().Symbols
			}
			return symbol.Options{BudgetTokens: cfg.BudgetTokens, Workers: cfg.Workers, MaxFileBytes: int64(cfg.MaxFileBytes)}
		},
		reposymbolmap.New(filepath.Join(storageDir, "symbols")),
	)
	tool, err := toolsymbolmap.New(store, root, maps)
	if err != nil {
		return err
	}
	tools[tool.Schema().Name()] = tool
	return nil
}
