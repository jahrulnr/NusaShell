//go:build darwin

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestDarwinManager(t *testing.T) (*darwinManager, *fakeRunner) {
	t.Helper()
	runner := &fakeRunner{}
	opts := testOptions(t)
	m := newDarwinManager(opts, runner)
	m.plistDir = t.TempDir()
	m.uid = func() int { return 501 }
	return m, runner
}

func TestDarwinInstallWritesPlistAndBootstraps(t *testing.T) {
	m, runner := newTestDarwinManager(t)
	if err := m.Install(); err != nil {
		t.Fatalf("install: %v", err)
	}
	plist, err := os.ReadFile(filepath.Join(m.plistDir, LaunchdLabel+".plist"))
	if err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if !strings.Contains(string(plist), LaunchdLabel) {
		t.Fatalf("plist missing label:\n%s", plist)
	}
	target := "gui/501/" + LaunchdLabel
	if !runner.has("launchctl", "bootout", target) && !runner.has("launchctl", "bootstrap", "gui/501") {
		t.Fatalf("missing activation calls: %+v", runner.calls)
	}
}

func TestDarwinUninstallBootoutsAndMovesToTrash(t *testing.T) {
	m, runner := newTestDarwinManager(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(m.plistDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistPath := filepath.Join(m.plistDir, LaunchdLabel+".plist")
	if err := os.WriteFile(plistPath, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist still present: %v", err)
	}
	trash := filepath.Join(home, ".Trash", LaunchdLabel+".plist")
	if _, err := os.Stat(trash); err != nil {
		t.Fatalf("plist not moved to Trash: %v", err)
	}
	if !runner.has("launchctl", "bootout", "gui/501/"+LaunchdLabel) {
		t.Fatalf("missing bootout: %+v", runner.calls)
	}
}

func TestDarwinStatusParsesPrint(t *testing.T) {
	m, runner := newTestDarwinManager(t)
	runner.script = map[string]string{
		"launchctl print gui/501/" + LaunchdLabel: "state = running\npid = 4242",
	}
	if err := os.MkdirAll(m.plistDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.plistDir, LaunchdLabel+".plist"), []byte(LaunchdPlist(m.opts, ServiceEnv(m.opts))), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Installed || !st.Loaded || !st.Running || st.Drifted {
		t.Fatalf("status = %+v", st)
	}
}
