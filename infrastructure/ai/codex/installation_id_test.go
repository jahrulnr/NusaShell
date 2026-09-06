package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrGenerateInstallationIDPersists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NUSASHELL_DATA_DIR", dir)

	first := LoadOrGenerateInstallationID()
	if first == "" {
		t.Fatal("expected non-empty installation id")
	}
	path := filepath.Join(dir, "config", "codex-installation-id")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted id: %v", err)
	}
	if got := string(data); got != first {
		t.Fatalf("persisted %q, want %q", got, first)
	}

	second := LoadOrGenerateInstallationID()
	if second != first {
		t.Fatalf("second load = %q, want %q", second, first)
	}
}
