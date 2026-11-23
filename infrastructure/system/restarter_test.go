//go:build !windows

package system

// Restarter tests — Unix semantics only. The Windows Restart swaps a
// staged binary through a detached helper and exits the process, which
// is untestable here; this file also provides the fakeManager the
// (linux-tagged) selection tests share.

import (
	"errors"
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
func (f fakeManager) PersistAcrossLogout() error  { return nil }

// restartFake is a registered service whose state the test sets directly.
type restartFake struct {
	fakeManager
	active, elsewhere bool
	restarted         []string
	restartErr        error
}

func (f *restartFake) Active(string) bool          { return f.active }
func (f *restartFake) ActiveElsewhere(string) bool { return f.elsewhere }
func (f *restartFake) Restart(name string) error {
	f.restarted = append(f.restarted, name)
	return f.restartErr
}

func TestRestarterModeFollowsManagerOwnership(t *testing.T) {
	cases := []struct {
		name              string
		active, elsewhere bool
		want              string
	}{
		{"owned by the manager", true, false, "service"},
		{"manager runs another process", true, true, "process"},
		{"no registered service running", false, false, "process"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &restartFake{active: c.active, elsewhere: c.elsewhere}
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
