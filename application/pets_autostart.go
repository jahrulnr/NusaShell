package application

import (
	"context"
	"runtime"
)

// StartPetAutoLaunch spawns the desktop pet overlay as a background
// sub-process during the Go server's own boot when the user has opted
// into auto-start. The pet is decoupled from the browser tab so it keeps
// running independently of the BrowserWindow lifecycle — closing or
// reloading the web UI never stops the pet.
//
// Conditions for a launch:
//  1. settings.pets_auto_start is true
//  2. PetsInstaller is wired (not nil)
//  3. Platform is Linux (the pet is Linux-only)
//  4. The pet binary resolves on disk (status.installed && status.path)
//  5. No install is currently running
//
// Single-flight via autostartOnce — calling StartPetAutoLaunch twice on
// the same App is a no-op. Tests that need to exercise multiple boot
// paths build fresh App instances rather than reset state.
func (a *App) StartPetAutoLaunch(ctx context.Context) {
	if a.PetsInstaller == nil {
		return
	}
	a.autostartOnce.Do(func() {
		a.goSafe("pets-autostart", func() {
			if a.Settings == nil {
				return
			}
			settings := a.Settings.Get()
			if !settings.PetsAutoStart {
				a.log("info", "pets", "auto-start off; pet stays managed by user")
				return
			}
			if runtime.GOOS != "linux" {
				a.log("info", "pets", "auto-start skipped (platform=%s)", runtime.GOOS)
				return
			}
			status := a.PetsInstaller.Status()
			if !status.Supported {
				a.log("info", "pets", "auto-start skipped (installer reports unsupported)")
				return
			}
			if !status.Installed || status.Path == "" {
				a.log("info", "pets", "auto-start skipped (binary not installed yet)")
				return
			}
			path, err := a.PetsInstaller.Launch()
			if err != nil {
				a.log("warn", "pets", "auto-start launch failed: %v", err)
				return
			}
			a.log("info", "pets", "auto-started pet overlay: %s", path)
		})
	})
	// ctx is reserved for a future ctx-cancellation hook; not consumed today.
}
