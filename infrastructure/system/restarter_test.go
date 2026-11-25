//go:build !windows

package system

// Restarter tests — Unix semantics only. The Windows Restart swaps a
// staged binary through a detached helper and exits the process, which
// is untestable here; this file also provides the fakeManager the
// (linux-tagged) selection tests share.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeManager is a minimal Service for selection and restarter tests.
type fakeManager struct{ name string }

func (f fakeManager) Name() string                      { return f.name }
func (f fakeManager) Available() error                  { return nil }
func (f fakeManager) Register(Spec) error               { return nil }
func (f fakeManager) Deregister(string) (string, error) { return "", nil }
func (f fakeManager) Start(string) error                { return nil }
func (f fakeManager) Stop(string) error                 { return nil }
func (f fakeManager) Restart(string) error              { return nil }
func (f fakeManager) Status(string) (string, State, error) {
	return "", StateUnknown, nil
}
func (f fakeManager) Active(string) bool          { return false }
func (f fakeManager) ActiveElsewhere(string) bool { return false }
func (f fakeManager) MainPID(string) int          { return 0 }
func (f fakeManager) PersistAcrossLogout() error  { return nil }

// restartFake is a registered service whose state the test sets directly.
type restartFake struct {
	fakeManager
	active, elsewhere bool
	mainPID           int
	restarted         []string
	restartErr        error
}

func (f *restartFake) Active(string) bool          { return f.active }
func (f *restartFake) MainPID(string) int          { return f.mainPID }
func (f *restartFake) ActiveElsewhere(string) bool { return f.elsewhere }
func (f *restartFake) Restart(name string) error {
	f.restarted = append(f.restarted, name)
	return f.restartErr
}

// Service mode means this process IS the unit's main process. An active unit
// owned by another pid — or a child of it — must not take the manager path: it
// would restart the real service from under itself.
func TestRestarterModeFollowsProcessOwnership(t *testing.T) {
	cases := []struct {
		name    string
		active  bool
		mainPID int
		want    string
	}{
		{"this process is the unit's main process", true, os.Getpid(), "service"},
		{"the unit runs another process", true, os.Getpid() + 1, "process"},
		{"a child of the unit is not the unit", true, os.Getppid(), "process"},
		{"no registered service running", false, 0, "process"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &restartFake{active: c.active, mainPID: c.mainPID}
			r := NewRestarter(svc, "nusashell", "/home/u/.local/bin/nusashell", []string{"nusashell", "serve"})
			if got := r.Mode(); got != c.want {
				t.Fatalf("Mode() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRestarterServiceModeAsksTheManager(t *testing.T) {
	svc := &restartFake{active: true}
	reexecCalled := false
	r := NewRestarter(svc, "nusashell", "/bin/nusashell", nil)
	r.reexec = func(string, []string) error { reexecCalled = true; return nil }
	if err := r.Restart("service"); err != nil {
		t.Fatalf("Restart(service): %v", err)
	}
	if len(svc.restarted) != 1 || svc.restarted[0] != "nusashell" || reexecCalled {
		t.Fatalf("restarted=%v reexec=%v", svc.restarted, reexecCalled)
	}
}

func TestRestarterProcessModeReplacesInPlace(t *testing.T) {
	svc := &restartFake{}
	var gotPath string
	var gotArgv []string
	r := NewRestarter(svc, "nusashell", "/home/u/.local/bin/nusashell", []string{"nusashell", "serve", "--port", "11998"})
	r.reexec = func(path string, argv []string) error {
		gotPath, gotArgv = path, argv
		return nil
	}
	if err := r.Restart("process"); err != nil {
		t.Fatalf("Restart(process): %v", err)
	}
	if len(svc.restarted) != 0 {
		t.Fatalf("manager restarted a foreground run: %v", svc.restarted)
	}
	if gotPath != "/home/u/.local/bin/nusashell" || len(gotArgv) != 4 || gotArgv[3] != "11998" {
		t.Fatalf("reexec path=%q argv=%v", gotPath, gotArgv)
	}
}

func TestRestarterSurfacesManagerError(t *testing.T) {
	svc := &restartFake{active: true, restartErr: errors.New("bus down")}
	r := NewRestarter(svc, "nusashell", "/bin/nusashell", nil)
	if err := r.Restart("service"); err == nil {
		t.Fatal("a failing manager restart was reported as success")
	}
}

// SameBinary is the guard that keeps a process which is not the install from
// replacing it: a symlink to the same file is the same binary, any other file
// is not, and an unresolvable path never passes.
func TestSameBinaryResolvesSymlinksAndRefusesUnknowns(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "nusashell")
	if err := os.WriteFile(real, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "nusashell-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !SameBinary(real, real) {
		t.Fatal("a path is not the same binary as itself")
	}
	if !SameBinary(link, real) {
		t.Fatal("a symlink to the install must count as the install")
	}
	if SameBinary(other, real) {
		t.Fatal("two different files must not compare equal")
	}
	if SameBinary(filepath.Join(dir, "missing"), real) {
		t.Fatal("an unresolvable path must never pass the guard")
	}
	if SameBinary(real, filepath.Join(dir, "missing")) {
		t.Fatal("an unresolvable installed path must never pass the guard")
	}
}

// SelfPath answers the running executable with symlinks resolved, so a PATH
// shim compares equal to the file it points at.
func TestSelfPathResolvesTheRunningBinary(t *testing.T) {
	self, err := SelfPath()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(self) {
		t.Fatalf("SelfPath = %q, want an absolute path", self)
	}
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil && resolved != self {
		t.Fatalf("SelfPath = %q, want symlinks resolved to %q", self, resolved)
	}
}
