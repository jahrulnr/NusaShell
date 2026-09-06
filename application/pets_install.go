package application

import (
	"context"

	"nusashell/contracts"
)

// PetsInstaller is the port for the desktop pet release + launcher.
// The RPC/install policy lives in application/pets; this interface stays
// on the root ports surface because infrastructure adapters and cmd
// wiring still construct it here.
type PetsInstaller interface {
	Status() contracts.PetsStatusResult
	Install(ctx context.Context, version string, report func(contracts.PetsInstallProgressDTO)) error
	Launch() (string, error)
}
