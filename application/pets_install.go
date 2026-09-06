package application

import (
	"context"
	"runtime"

	"nusashell/contracts"
)

// Desktop pet launcher + one-click install: the installer port lives in
// infrastructure/petsinstall (release resolver + tar extraction + launcher
// writer); the App owns the single in-flight install, RPC handlers, and
// progress events on the Bus — same shape as tts_install.go/stt_install.go.
//
// The desktop pet (nusashell-pets) is Linux-only. On macOS/Windows the
// installer is not wired (PetsInstaller == nil) and the status RPC reports
// Supported=false so the UI hides the launcher entirely.

// PetsInstaller is the port for the desktop pet release + launcher.
type PetsInstaller interface {
	// Status snapshots what is installed plus the running flag the installer
	// observes (procfs scan + launcher probe).
	Status() contracts.PetsStatusResult
	// Install runs the resolve → download → verify → extract → activate
	// pipeline. version="" resolves the latest published release.
	Install(ctx context.Context, version string, report func(contracts.PetsInstallProgressDTO)) error
	// Launch spawns the resolved pet binary in the background and returns
	// the absolute path it ran, or a non-nil error.
	Launch() (string, error)
}

// handlePetsStatus returns the installer's snapshot plus the live install
// flag so the sidebar launcher can render its current state without
// racing the install goroutine.
func (a *App) handlePetsStatus() (any, *contracts.RPCError) {
	res := contracts.PetsStatusResult{Supported: runtime.GOOS == "linux"}
	if a.PetsInstaller != nil {
		res = a.PetsInstaller.Status()
		res.Supported = res.Supported && runtime.GOOS == "linux"
	}
	res.InstallActive = a.PetsInstallRunning()
	return res, nil
}

// handlePetsInstallStart kicks off one pet install in the background.
// Progress flows to the UI over the Bus as pets.install.* events; the RPC
// returns immediately so the dialog can render live progress.
func (a *App) handlePetsInstallStart(req contracts.PetsInstallStartRequest) (any, *contracts.RPCError) {
	if a.PetsInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet installer is not available in this build"}
	}
	if !a.PetsInstaller.Status().Supported {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet is supported on Linux only"}
	}
	if !a.petsInstallBegin() {
		return contracts.PetsInstallStartResult{Started: false, Running: true, Message: "a desktop pet install is already running"}, nil
	}

	go func() {
		defer a.petsInstallEnd()
		err := a.PetsInstaller.Install(context.Background(), req.Version, func(p contracts.PetsInstallProgressDTO) {
			a.Bus.Emit(contracts.EventPetsInstallProgress, p)
		})
		if err != nil {
			a.log("error", "pets", "desktop pet install failed: %v", err)
			a.Bus.Emit(contracts.EventPetsInstallError, contracts.PetsInstallProgressDTO{Message: err.Error()})
			return
		}
		a.log("info", "pets", "desktop pet installed: version=%q", req.Version)
		a.Bus.Emit(contracts.EventPetsInstallDone, contracts.PetsInstallProgressDTO{
			Phase:   "verify",
			Message: "Desktop pet ready",
		})
	}()
	return contracts.PetsInstallStartResult{Started: true, Running: true}, nil
}

// handlePetsLaunch spawns the resolved pet binary in the background. The
// RPC returns once the spawn call returns; the pet keeps running
// independently of the browser tab.
func (a *App) handlePetsLaunch() (any, *contracts.RPCError) {
	if a.PetsInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet installer is not available in this build"}
	}
	status := a.PetsInstaller.Status()
	if !status.Supported {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet is supported on Linux only"}
	}
	if a.PetsInstallRunning() {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "a desktop pet install is currently running; launch is paused"}
	}
	if !status.Installed || status.Path == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet is not installed"}
	}
	path, err := a.PetsInstaller.Launch()
	if err != nil {
		a.log("warn", "pets", "launch failed: %v", err)
		return contracts.PetsLaunchResult{Launched: false, Message: err.Error()}, nil
	}
	a.log("info", "pets", "launched desktop pet: %s", path)
	return contracts.PetsLaunchResult{Launched: true, Path: path}, nil
}

// petsInstallBegin claims the single-flight slot.
func (a *App) petsInstallBegin() bool {
	a.petsInstallMu.Lock()
	defer a.petsInstallMu.Unlock()
	if a.petsInstallActive {
		return false
	}
	a.petsInstallActive = true
	return true
}

// petsInstallEnd releases the single-flight slot.
func (a *App) petsInstallEnd() {
	a.petsInstallMu.Lock()
	defer a.petsInstallMu.Unlock()
	a.petsInstallActive = false
}

// petsInstallRunning reports the single-flight slot state.
func (a *App) PetsInstallRunning() bool {
	a.petsInstallMu.Lock()
	defer a.petsInstallMu.Unlock()
	return a.petsInstallActive
}
