package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// codexModelsCache is the on-disk shape of ~/.codex/models_cache.json.
// The Codex CLI caches account-specific discovery metadata here. It is used
// only to fill gaps in the app-server model/list response; direct NusaShell
// chat turns use the ChatGPT Responses API and do not consult this file.
type codexModelsCache struct {
	Models []struct {
		Slug                      string `json:"slug"`
		ContextWindow             int    `json:"context_window"`
		MaxContextWindow          int    `json:"max_context_window"`
		EffectiveContextWindowPct int    `json:"effective_context_window_percent"`
	} `json:"models"`
}

// loadCodexModelsCache reads ~/.codex/models_cache.json and returns a
// slug → context window map for app-server discovery enrichment. Returns nil
// if the file is missing or cannot be parsed — callers fall back to the
// public models.dev catalog for direct-API context metadata.
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
		// For discovery metadata, prefer context_window and only use
		// max_context_window when the CLI omitted the former. The application
		// layer does not use this value as the direct ChatGPT API limit.
		cw := m.ContextWindow
		if cw <= 0 {
			cw = m.MaxContextWindow
		}
		if cw > 0 {
			out[m.Slug] = cw
		}
	}
	return out
}
