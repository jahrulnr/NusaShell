package pets

import (
	"context"

	"nusashell/contracts"
	"nusashell/domain"
)

// Installer is the port for desktop pet control: install, spawn, stop, and status.
type Installer interface {
	Status() contracts.PetsStatusResult
	Install(ctx context.Context, version string, report func(contracts.PetsInstallProgressDTO)) error
	Launch() (string, error)
	Stop() error
}

// SettingsSource reads the pets_auto_start flag at boot.
type SettingsSource interface {
	Get() domain.Settings
}

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// Emitter publishes pets.install.* events.
type Emitter interface {
	Emit(typ string, v any)
}

// GoFunc runs background work (install). App wires this to goSafe.
type GoFunc func(fn func())

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Installer Installer
	Settings  SettingsSource
	Log       Logger
	Bus       Emitter
	Go        GoFunc
}
