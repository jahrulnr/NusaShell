package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// codexModelsCache is the on-disk shape of ~/.codex/models_cache.json.
// The Codex CLI caches the full model catalog (including context_window
// and max_context_window) here. We parse this file to get the real context
// window that Codex enforces — which is often smaller than the model's
// documented ceiling (e.g. luna: 272k cache vs 1.05M models.dev).
type codexModelsCache struct {
	Models []struct {
		Slug                      string `json:"slug"`
		ContextWindow             int    `json:"context_window"`
		MaxContextWindow          int    `json:"max_context_window"`
		EffectiveContextWindowPct int    `json:"effective_context_window_percent"`
	} `json:"models"`
}

// loadCodexModelsCache reads ~/.codex/models_cache.json and returns a
// slug → context window map. Returns nil if the file is missing or
// cannot be parsed — callers fall back to other catalog sources.
func loadCodexModelsCache() map[string]int {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(home, ".codex", "models_cache.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cache codexModelsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	out := make(map[string]int, len(cache.Models))
	for _, m := range cache.Models {
		if m.Slug == "" {
			continue
		}
		// Use max_context_window if available (the user can raise it via
		// config), otherwise the default context_window.
		cw := m.MaxContextWindow
		if cw <= 0 {
			cw = m.ContextWindow
		}
		if cw > 0 {
			out[m.Slug] = cw
		}
	}
	return out
}
