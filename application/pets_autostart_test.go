package application

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// autostartFakeInstaller mirrors the application.PetsInstaller contract
// and counts launches. Kept inline so the test stays in this package —
// the public-facing fake lives in transport/pets_rpc_test.go.
type autostartFakeInstaller struct {
	mu       sync.Mutex
	status   contracts.PetsStatusResult
	launches atomic.Int32
	launchCh chan string // closed when Launch has been called once
	err      error
}

func (f *autostartFakeInstaller) Status() contracts.PetsStatusResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *autostartFakeInstaller) Install(ctx context.Context, version string, report func(contracts.PetsInstallProgressDTO)) error {
	return nil
}

func (f *autostartFakeInstaller) Launch() (string, error) {
	f.launches.Add(1)
	if f.launchCh != nil {
		select {
		case <-f.launchCh:
		default:
			close(f.launchCh)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	return f.status.Path, nil
}

func autostartApp(t *testing.T, inst *autostartFakeInstaller, settings domain.Settings) *App {
	t.Helper()
	return &App{
		PetsInstaller: inst,
		Settings:      &memSettingsStore{s: settings},
		Logs:          &fakeLogStore{},
	}
}

func waitForLaunch(t *testing.T, inst *autostartFakeInstaller, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if inst.launches.Load() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("pet was not launched within deadline")
}

func TestStartPetAutoLaunchRespectsOffSetting(t *testing.T) {
	inst := &autostartFakeInstaller{status: contracts.PetsStatusResult{
		Supported: runtime.GOOS == "linux",
		Installed: true,
		Path:      "/opt/pets/nusashell-pets",
	}}
	app := autostartApp(t, inst, domain.Settings{PetsAutoStart: false})
	app.StartPetAutoLaunch(context.Background())
	// goSafe runs in the background; wait briefly to confirm no launch.
	time.Sleep(50 * time.Millisecond)
	if got := inst.launches.Load(); got != 0 {
		t.Fatalf("expected 0 launches when PetsAutoStart=false, got %d", got)
	}
}

func TestStartPetAutoLaunchSkipsWhenNotInstalled(t *testing.T) {
	inst := &autostartFakeInstaller{status: contracts.PetsStatusResult{
		Supported: runtime.GOOS == "linux",
		Installed: false,
	}}
	app := autostartApp(t, inst, domain.Settings{PetsAutoStart: true})
	app.StartPetAutoLaunch(context.Background())
	time.Sleep(50 * time.Millisecond)
	if got := inst.launches.Load(); got != 0 {
		t.Fatalf("expected 0 launches when not installed, got %d", got)
	}
}

func TestStartPetAutoLaunchSkipsOnUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only pet platform; non-Linux path is exercised by the same code branch")
	}
	inst := &autostartFakeInstaller{status: contracts.PetsStatusResult{
		Supported: false,
		Installed: true,
		Path:      "/opt/pets/nusashell-pets",
	}}
	app := autostartApp(t, inst, domain.Settings{PetsAutoStart: true})
	app.StartPetAutoLaunch(context.Background())
	time.Sleep(50 * time.Millisecond)
	if got := inst.launches.Load(); got != 0 {
		t.Fatalf("expected 0 launches on unsupported platform, got %d", got)
	}
}

func TestStartPetAutoLaunchLaunchesWhenEnabledAndInstalled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet auto-start runs on Linux only")
	}
	inst := &autostartFakeInstaller{status: contracts.PetsStatusResult{
		Supported: runtime.GOOS == "linux",
		Installed: true,
		Path:      "/opt/pets/nusashell-pets",
	}}
	app := autostartApp(t, inst, domain.Settings{PetsAutoStart: true})
	app.StartPetAutoLaunch(context.Background())
	waitForLaunch(t, inst, 2*time.Second)
	if got := inst.launches.Load(); got != 1 {
		t.Fatalf("expected 1 launch, got %d", got)
	}
}

func TestStartPetAutoLaunchIsSingleFlight(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet auto-start runs on Linux only")
	}
	inst := &autostartFakeInstaller{status: contracts.PetsStatusResult{
		Supported: runtime.GOOS == "linux",
		Installed: true,
		Path:      "/opt/pets/nusashell-pets",
	}}
	app := autostartApp(t, inst, domain.Settings{PetsAutoStart: true})
	app.StartPetAutoLaunch(context.Background())
	app.StartPetAutoLaunch(context.Background())
	app.StartPetAutoLaunch(context.Background())
	waitForLaunch(t, inst, 2*time.Second)
	// Give the goroutine enough time to (not) run additional launches.
	time.Sleep(80 * time.Millisecond)
	if got := inst.launches.Load(); got != 1 {
		t.Fatalf("expected single launch across 3 calls, got %d", got)
	}
}

func TestStartPetAutoLaunchIsNoopWhenInstallerNil(t *testing.T) {
	app := &App{PetsInstaller: nil, Settings: &memSettingsStore{s: domain.Settings{PetsAutoStart: true}}, Logs: &fakeLogStore{}}
	// Must not panic.
	app.StartPetAutoLaunch(context.Background())
}
