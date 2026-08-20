package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadCodexModelsCache(t *testing.T) {
	dir := t.TempDir()
	// os.UserHomeDir uses $HOME on Unix and %USERPROFILE% on Windows.
	// Set both so the test is cross-platform.
	origHome := os.Getenv("HOME")
	origProfile := os.Getenv("USERPROFILE")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	defer t.Setenv("HOME", origHome)
	defer t.Setenv("USERPROFILE", origProfile)
	_ = runtime.GOOS // keep import for future platform-specific guards

	cache := codexModelsCache{
		Models: []struct {
			Slug                      string `json:"slug"`
			ContextWindow             int    `json:"context_window"`
			MaxContextWindow          int    `json:"max_context_window"`
			EffectiveContextWindowPct int    `json:"effective_context_window_percent"`
		}{
			{Slug: "gpt-5.6-luna", ContextWindow: 272000, MaxContextWindow: 872000, EffectiveContextWindowPct: 95},
			{Slug: "gpt-5.6-terra", ContextWindow: 272000, MaxContextWindow: 0, EffectiveContextWindowPct: 95},
		},
	}
	data, _ := json.Marshal(map[string]any{"models": cache.Models})
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "models_cache.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	got := loadCodexModelsCache()
	if got["gpt-5.6-luna"] != 872000 {
		t.Fatalf("luna context = %d, want 872000 (max_context_window)", got["gpt-5.6-luna"])
	}
	if got["gpt-5.6-terra"] != 272000 {
		t.Fatalf("terra context = %d, want 272000 (context_window fallback)", got["gpt-5.6-terra"])
	}
}

func TestLoadCodexModelsCacheMissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if got := loadCodexModelsCache(); got != nil {
		t.Fatalf("expected nil for missing file, got %v", got)
	}
}
