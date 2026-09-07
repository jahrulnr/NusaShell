package pets

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

type retryInstaller struct {
	failsBefore int32
	launches    atomic.Int32
	running     atomic.Bool
	path        string
}

func (r *retryInstaller) Status() contracts.PetsStatusResult {
	return contracts.PetsStatusResult{
		Supported: runtime.GOOS == "linux",
		Installed: true,
		Path:      r.path,
		Running:   r.running.Load(),
	}
}

func (r *retryInstaller) Install(context.Context, string, func(contracts.PetsInstallProgressDTO)) error {
	return nil
}

func (r *retryInstaller) Launch() (string, error) {
	n := r.launches.Add(1)
	if int(n) <= int(r.failsBefore) {
		return "", errors.New("pet: exited immediately after spawn (DISPLAY may be unset)")
	}
	r.running.Store(true)
	return r.path, nil
}

func (r *retryInstaller) Stop() error {
	r.running.Store(false)
	return nil
}

type memSettings struct{ s domain.Settings }

func (m *memSettings) Get() domain.Settings { return m.s }

func TestAutoLaunchRetriesUntilRunning(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet auto-start runs on Linux only")
	}
	prev := autoLaunchBackoff
	autoLaunchBackoff = time.Millisecond
	t.Cleanup(func() { autoLaunchBackoff = prev })

	inst := &retryInstaller{failsBefore: 2, path: "/opt/pets/nusashell-pets"}
	svc := New(Deps{
		Installer: inst,
		Settings:  &memSettings{s: domain.Settings{PetsAutoStart: true}},
		Log:       func(string, string, string, ...any) {},
	})
	svc.AutoLaunch(context.Background())
	if got := inst.launches.Load(); got != 3 {
		t.Fatalf("expected 3 launch attempts (2 fail + 1 ok), got %d", got)
	}
	if !inst.running.Load() {
		t.Fatal("pet should be running after successful retry")
	}
}

func TestAutoLaunchStopsOnCancel(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet auto-start runs on Linux only")
	}
	prev := autoLaunchBackoff
	autoLaunchBackoff = 50 * time.Millisecond
	t.Cleanup(func() { autoLaunchBackoff = prev })

	inst := &retryInstaller{failsBefore: 100, path: "/opt/pets/nusashell-pets"}
	svc := New(Deps{
		Installer: inst,
		Settings:  &memSettings{s: domain.Settings{PetsAutoStart: true}},
		Log:       func(string, string, string, ...any) {},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.AutoLaunch(ctx)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AutoLaunch did not return after cancel")
	}
	if inst.running.Load() {
		t.Fatal("cancel must not leave a successful launch")
	}
}
