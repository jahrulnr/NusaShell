package application

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
)

type fakePetsInstaller struct {
	mu         sync.Mutex
	status     contracts.PetsStatusResult
	installs   []string
	launches   []string
	failWith   error
	block      chan struct{}
	launchFail bool
	launchPath string
}

func (f *fakePetsInstaller) Status() contracts.PetsStatusResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakePetsInstaller) Install(ctx context.Context, version string, report func(contracts.PetsInstallProgressDTO)) error {
	f.mu.Lock()
	f.installs = append(f.installs, version)
	block := f.block
	fail := f.failWith
	f.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-block:
		}
	}
	report(contracts.PetsInstallProgressDTO{Phase: "download", BytesFetched: 1024, BytesTotal: 4096})
	report(contracts.PetsInstallProgressDTO{Phase: "verify"})
	if fail != nil {
		return fail
	}
	return nil
}

func (f *fakePetsInstaller) Launch() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launches = append(f.launches, "")
	if f.launchFail {
		return "", errors.New("spawn failed")
	}
	return f.launchPath, nil
}

func petsApp(inst *fakePetsInstaller) *App {
	return &App{PetsInstaller: inst, Logs: &fakeLogStore{}, Bus: NewBus()}
}

func TestPetsStatusReturnsSnapshot(t *testing.T) {
	inst := &fakePetsInstaller{status: contracts.PetsStatusResult{
		Supported: runtime.GOOS == "linux",
		Installed: true,
		Path:      "/opt/nusashell-pets/current/nusashell-pets",
		Version:   "0.2.0",
	}}
	out, rpcErr := petsApp(inst).handlePetsStatus()
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	res := out.(contracts.PetsStatusResult)
	if res.Supported != (runtime.GOOS == "linux") {
		t.Errorf("supported flag must mirror platform, got %v", res.Supported)
	}
	if !res.Installed || res.Path == "" || res.Version != "0.2.0" {
		t.Errorf("unexpected snapshot: %+v", res)
	}
}

func TestPetsStatusWithoutInstallerReturnsRuntimePlatform(t *testing.T) {
	out, rpcErr := (&App{Logs: &fakeLogStore{}}).handlePetsStatus()
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	res := out.(contracts.PetsStatusResult)
	if res.Supported != (runtime.GOOS == "linux") {
		t.Errorf("supported must mirror runtime platform, got %v", res.Supported)
	}
	if res.Installed || res.Path != "" {
		t.Errorf("missing installer must report not installed, got %+v", res)
	}
}

func TestPetsInstallRequiresSupportedPlatform(t *testing.T) {
	inst := &fakePetsInstaller{status: contracts.PetsStatusResult{Supported: false}}
	_, rpcErr := petsApp(inst).handlePetsInstallStart(contracts.PetsInstallStartRequest{})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "Linux") {
		t.Fatalf("expected Linux validation, got %v", rpcErr)
	}
	if inst.installCount() != 0 {
		t.Error("installer must not be invoked on unsupported platform")
	}
}

func (f *fakePetsInstaller) installCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.installs)
}

func TestPetsInstallSingleFlight(t *testing.T) {
	inst := &fakePetsInstaller{
		status: contracts.PetsStatusResult{Supported: runtime.GOOS == "linux"},
		block:  make(chan struct{}),
	}
	app := petsApp(inst)

	first, _ := app.handlePetsInstallStart(contracts.PetsInstallStartRequest{Version: "0.2.0"})
	if !(first.(contracts.PetsInstallStartResult)).Started {
		t.Fatal("first start must begin the install")
	}
	waitForCondition(t, func() bool { return inst.installCount() == 1 }, "first install never started")

	second, _ := app.handlePetsInstallStart(contracts.PetsInstallStartRequest{Version: "0.2.0"})
	res := second.(contracts.PetsInstallStartResult)
	if res.Started || !res.Running {
		t.Errorf("second concurrent start must report running=true started=false, got %+v", res)
	}
	close(inst.block)
	waitForCondition(t, func() bool { return !app.PetsInstallRunning() }, "install never finished")
}

func TestPetsInstallDoneEventEmitted(t *testing.T) {
	inst := &fakePetsInstaller{status: contracts.PetsStatusResult{Supported: true}}
	app := petsApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handlePetsInstallStart(contracts.PetsInstallStartRequest{Version: "0.2.0"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	waitForEvent(t, events, contracts.EventPetsInstallDone)
	waitForCondition(t, func() bool { return !app.PetsInstallRunning() }, "run flag never cleared")
}

func TestPetsInstallErrorSurfacesOnBus(t *testing.T) {
	inst := &fakePetsInstaller{
		status:   contracts.PetsStatusResult{Supported: true},
		failWith: errors.New("network down"),
	}
	app := petsApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handlePetsInstallStart(contracts.PetsInstallStartRequest{Version: "0.2.0"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	waitForEvent(t, events, contracts.EventPetsInstallError)
	waitForCondition(t, func() bool { return !app.PetsInstallRunning() }, "run flag never cleared")
}

func TestPetsLaunchRequiresInstallerAndSupported(t *testing.T) {
	t.Run("missing installer", func(t *testing.T) {
		_, rpcErr := (&App{Logs: &fakeLogStore{}}).handlePetsLaunch()
		if rpcErr == nil || !strings.Contains(rpcErr.Message, "installer") {
			t.Fatalf("expected installer-missing error, got %v", rpcErr)
		}
	})
	t.Run("unsupported platform", func(t *testing.T) {
		inst := &fakePetsInstaller{status: contracts.PetsStatusResult{Supported: false}}
		_, rpcErr := petsApp(inst).handlePetsLaunch()
		if rpcErr == nil || !strings.Contains(rpcErr.Message, "Linux") {
			t.Fatalf("expected Linux error, got %v", rpcErr)
		}
	})
	t.Run("not installed", func(t *testing.T) {
		inst := &fakePetsInstaller{status: contracts.PetsStatusResult{Supported: true}}
		_, rpcErr := petsApp(inst).handlePetsLaunch()
		if rpcErr == nil || !strings.Contains(rpcErr.Message, "not installed") {
			t.Fatalf("expected not-installed error, got %v", rpcErr)
		}
	})
}

func TestPetsLaunchSurfacesSpawnFailure(t *testing.T) {
	inst := &fakePetsInstaller{
		status:     contracts.PetsStatusResult{Supported: true, Installed: true, Path: "/opt/pets"},
		launchFail: true,
	}
	out, rpcErr := petsApp(inst).handlePetsLaunch()
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	res := out.(contracts.PetsLaunchResult)
	if res.Launched || res.Path != "" {
		t.Errorf("launch failure must surface, got %+v", res)
	}
	if res.Message == "" {
		t.Error("expected non-empty failure message")
	}
}

func TestPetsLaunchSuccess(t *testing.T) {
	inst := &fakePetsInstaller{
		status:     contracts.PetsStatusResult{Supported: true, Installed: true, Path: "/opt/pets/nusashell-pets"},
		launchPath: "/opt/pets/nusashell-pets",
	}
	out, rpcErr := petsApp(inst).handlePetsLaunch()
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	res := out.(contracts.PetsLaunchResult)
	if !res.Launched || res.Path != "/opt/pets/nusashell-pets" {
		t.Errorf("launch success mismatch: %+v", res)
	}
}

// guard: collectEvents is defined in tts_install_test.go and shared.
var _ = time.Second
