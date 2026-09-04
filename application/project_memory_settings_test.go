package application

import (
	"path/filepath"
	"strings"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

func TestHandleSettingsSetProjectMemoryBase(t *testing.T) {
	app := NewApp(Deps{Settings: &fakeSettings{Settings: domain.DefaultSettings()}})

	rel := "relative/path"
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{ProjectMemoryBase: &rel}); err == nil {
		t.Fatal("relative project_memory_base must fail validation")
	} else if !strings.Contains(err.Message, "absolute") {
		t.Fatalf("validation message = %s", err.Message)
	}

	empty := ""
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{ProjectMemoryBase: &empty}); err != nil {
		t.Fatalf("empty base must be allowed: %v", err)
	}
	if app.Settings.Get().ProjectMemoryBase != "" {
		t.Fatalf("empty should clear the setting, got %q", app.Settings.Get().ProjectMemoryBase)
	}

	abs := t.TempDir()
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{ProjectMemoryBase: &abs}); err != nil {
		t.Fatalf("absolute base: %v", err)
	}
	if got := app.Settings.Get().ProjectMemoryBase; got != filepath.Clean(abs) {
		t.Fatalf("stored base = %q, want %q", got, filepath.Clean(abs))
	}

	home := osUserHomeForTest(t)
	tilde := "~/.memory"
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{ProjectMemoryBase: &tilde}); err != nil {
		t.Fatalf("~ expansion: %v", err)
	}
	want := filepath.Join(home, ".memory")
	if got := app.Settings.Get().ProjectMemoryBase; got != want {
		t.Fatalf("expanded base = %q, want %q", got, want)
	}
}

func osUserHomeForTest(t *testing.T) string {
	t.Helper()
	home, err := domain.ExpandHomeDir("~")
	if err != nil {
		t.Fatal(err)
	}
	return home
}
