package pet

import (
	"context"

	"nusashell/contracts"
)

// Adapter satisfies application.PetsInstaller. This package owns the
// desktop pet process: install the release, spawn/stop the overlay, and
// pass this Go process's listen address into the child.
type Adapter struct {
	*Installer
}

// NewAdapter wraps the pet adapter into the application.PetsInstaller port.
// Use New or NewWithResolver to build the underlying Installer.
func NewAdapter(in *Installer) *Adapter {
	if in == nil {
		in = New("")
	}
	return &Adapter{Installer: in}
}

// Compile-time check: the adapter satisfies the application.PetsInstaller
// port. The port itself lives in the application package; this assertion
// pins the wire shape from the infrastructure side so a future refactor
// cannot silently drift.
var _ interface {
	Status() contracts.PetsStatusResult
	Install(ctx context.Context, version string, report func(contracts.PetsInstallProgressDTO)) error
	Launch() (string, error)
	Stop() error
} = (*Adapter)(nil)

// Status translates the installer snapshot into the wire DTO.
func (a *Adapter) Status() contracts.PetsStatusResult {
	raw := a.Installer.Status()
	return contracts.PetsStatusResult{
		Supported:     raw.Supported,
		Installed:     raw.Installed,
		Path:          raw.Path,
		AssetsPath:    raw.AssetsPath,
		Version:       raw.Version,
		InstallRoot:   raw.InstallRoot,
		Launcher:      raw.Launcher,
		Running:       raw.Running,
		InstallActive: false, // application layer owns the live install flag
	}
}

// Install runs the resolve → download → extract pipeline and translates
// progress into the wire DTO.
func (a *Adapter) Install(ctx context.Context, version string, report func(contracts.PetsInstallProgressDTO)) error {
	return a.Installer.install(ctx, version, func(p Progress) {
		if report == nil {
			return
		}
		report(contracts.PetsInstallProgressDTO{
			Phase:        p.Phase,
			BytesFetched: p.BytesFetched,
			BytesTotal:   p.BytesTotal,
			Message:      p.Message,
		})
	})
}

// Launch spawns the resolved pet binary in the background when none is
// running. Stop kills that process. The path is returned to the caller
// (the application layer passes it through to the UI).
func (a *Adapter) Launch() (string, error) { return a.Installer.Launch() }

func (a *Adapter) Stop() error { return a.Installer.Stop() }
