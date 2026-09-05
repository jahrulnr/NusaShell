//go:build linux

package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner records every invocation and replays scripted answers.
type fakeRunner struct {
	calls  [][2]string
	script map[string]string // "name arg1 arg2" -> stdout
	err    map[string]error
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, [2]string{name, strings.Join(args, " ")})
	if f.err != nil {
		if err, ok := f.err[key]; ok {
			return "", err
		}
	}
	if f.script != nil {
		return f.script[key], nil
	}
	return "", nil
}

func (f *fakeRunner) has(name string, args ...string) bool {
	key := strings.Join(append([]string{name}, args...), " ")
	for _, call := range f.calls {
		if strings.Join(append([]string{call[0]}, strings.Split(call[1], " ")...), " ") == key {
			return true
		}
	}
	return false
}

func testOptions(t *testing.T) Options {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "current")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "nusashell")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Options{BinaryPath: bin, DataDir: filepath.Join(t.TempDir(), "nusashell-data")}
}

func newTestLinuxManager(t *testing.T) (*linuxManager, *fakeRunner) {
	t.Helper()
	runner := &fakeRunner{}
	opts := testOptions(t)
	m := newLinuxManager(opts, runner)
	m.unitDir = t.TempDir()
	return m, runner
}

func TestLinuxInstallWritesUnitAndActivates(t *testing.T) {
	m, runner := newTestLinuxManager(t)
	if err := m.Install(); err != nil {
		t.Fatalf("install: %v", err)
	}
	unit, err := os.ReadFile(filepath.Join(m.unitDir, ServiceName+".service"))
	if err != nil {
		t.Fatalf("unit not written: %v", err)
	}
	if !strings.Contains(string(unit), "ExecStart=") {
		t.Fatalf("unit missing ExecStart:\n%s", unit)
	}
	for _, want := range [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", ServiceName + ".service"},
		{"systemctl", "--user", "restart", ServiceName + ".service"},
	} {
		if !runner.has(want[0], want[1:]...) {
			t.Fatalf("missing systemctl call %v in %+v", want, runner.calls)
		}
	}
}

func TestLinuxInstallBacksUpPreviousUnit(t *testing.T) {
	m, _ := newTestLinuxManager(t)
	unitPath := filepath.Join(m.unitDir, ServiceName+".service")
	if err := os.MkdirAll(m.unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("ExecStart=/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(); err != nil {
		t.Fatalf("install: %v", err)
	}
	backup, err := os.ReadFile(unitPath + ".bak")
	if err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	if string(backup) != "ExecStart=/old\n" {
		t.Fatalf("backup content = %q", backup)
	}
}

func TestLinuxInstallRefusesSymlinkedUnit(t *testing.T) {
	m, _ := newTestLinuxManager(t)
	if err := os.MkdirAll(m.unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	unitPath := filepath.Join(m.unitDir, ServiceName+".service")
	if err := os.WriteFile(filepath.Join(m.unitDir, "elsewhere"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(m.unitDir, "elsewhere"), unitPath); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(); err == nil {
		t.Fatal("install must refuse a symlinked managed unit")
	}
}

func TestLinuxUninstallDisablesThenRemoves(t *testing.T) {
	m, runner := newTestLinuxManager(t)
	if err := os.MkdirAll(m.unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	unitPath := filepath.Join(m.unitDir, ServiceName+".service")
	if err := os.WriteFile(unitPath, []byte("ExecStart=/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("unit still present: %v", err)
	}
	if !runner.has("systemctl", "--user", "disable", "--now", ServiceName+".service") {
		t.Fatalf("missing disable --now in %+v", runner.calls)
	}
}

func TestLinuxUninstallMissingUnitSucceeds(t *testing.T) {
	m, _ := newTestLinuxManager(t)
	if err := os.MkdirAll(m.unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(); err != nil {
		t.Fatalf("uninstall without unit must succeed: %v", err)
	}
}

func TestLinuxStatusParsesSystemctl(t *testing.T) {
	m, runner := newTestLinuxManager(t)
	runner.script = map[string]string{
		"systemctl --user is-enabled " + ServiceName + ".service": "enabled",
		"systemctl --user is-active " + ServiceName + ".service":  "active",
	}
	if err := os.MkdirAll(m.unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.unitDir, ServiceName+".service"), []byte(SystemdUnit(m.opts, ServiceEnv(m.opts))), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Installed || !st.Loaded || !st.Running || st.Drifted {
		t.Fatalf("status = %+v, want installed+loaded+running, no drift", st)
	}
}

func TestLinuxStatusDetectsDrift(t *testing.T) {
	m, runner := newTestLinuxManager(t)
	runner.script = map[string]string{
		"systemctl --user is-enabled " + ServiceName + ".service": "enabled",
		"systemctl --user is-active " + ServiceName + ".service":  "active",
	}
	if err := os.MkdirAll(m.unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.unitDir, ServiceName+".service"), []byte("ExecStart=/stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Drifted {
		t.Fatalf("drift not detected: %+v", st)
	}
}

func TestLinuxLifecycleCommands(t *testing.T) {
	m, runner := newTestLinuxManager(t)
	for _, tc := range []struct {
		action string
		call   func() error
		want   []string
	}{
		{"start", m.Start, []string{"systemctl", "--user", "start", ServiceName + ".service"}},
		{"stop", m.Stop, []string{"systemctl", "--user", "stop", ServiceName + ".service"}},
		{"restart", m.Restart, []string{"systemctl", "--user", "restart", ServiceName + ".service"}},
	} {
		runner.calls = nil
		if err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		if !runner.has(tc.want[0], tc.want[1:]...) {
			t.Fatalf("%s missing call %v: %+v", tc.action, tc.want, runner.calls)
		}
	}
}

func TestLinuxEnsureUserBusGivesActionableError(t *testing.T) {
	m, runner := newTestLinuxManager(t)
	runner.err = map[string]error{
		"systemctl --user is-system-running": errors.New("Failed to connect to bus: No such file or directory"),
	}
	err := m.ensureUserBus()
	if err == nil {
		t.Fatal("expected actionable error when user bus is unavailable")
	}
	if !strings.Contains(err.Error(), "loginctl enable-linger") {
		t.Fatalf("error must carry linger remediation: %v", err)
	}
}
