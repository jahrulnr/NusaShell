package application

import (
	"context"

	"nusashell/contracts"
)

// PetsInstaller is the port for desktop pet control: install, spawn, stop,
// and status. Application policy lives in application/pets; the adapter is
// infrastructure/pet.
type PetsInstaller interface {
	Status() contracts.PetsStatusResult
	Install(ctx context.Context, version string, report func(contracts.PetsInstallProgressDTO)) error
	Launch() (string, error)
	Stop() error
}
