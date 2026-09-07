package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteModelListAuthUsesSelectedNusaShellAccount(t *testing.T) {
	dir := t.TempDir()
	if err := writeModelListAuth(dir, "selected-token", "plus-account"); err != nil {
		t.Fatalf("writeModelListAuth: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var auth codexCLIAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		t.Fatalf("decode auth.json: %v", err)
	}
	if auth.Tokens.AccessToken != "selected-token" || auth.Tokens.AccountID != "plus-account" {
		t.Fatalf("auth account = %q token=%q", auth.Tokens.AccountID, auth.Tokens.AccessToken)
	}
}

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
	if got["gpt-5.6-luna"] != 272000 {
		t.Fatalf("luna context = %d, want 272000 (active context_window)", got["gpt-5.6-luna"])
	}
	if got["gpt-5.6-terra"] != 272000 {
		t.Fatalf("terra context = %d, want 272000 (context_window fallback)", got["gpt-5.6-terra"])
	}
}

func TestLoadCodexModelsCacheMissingFile(t *testing.T) {
	dir := t.TempDir()
	// os.UserHomeDir uses $HOME on Unix and %USERPROFILE% on Windows.
	// Set both so the test never reads the developer's real ~/.codex.
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if got := loadCodexModelsCache(); got != nil {
		t.Fatalf("expected nil for missing file, got %v", got)
	}
}
