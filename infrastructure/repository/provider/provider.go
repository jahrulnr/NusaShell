// Package repoprovider implements the Provider repository port as one JSON
// document per provider: <path>/<providerID>.json. Providers are few and
// configured rather than user-generated, so a flat file per id keeps Save
// atomic (tmp+rename) and List a directory glob.
//
// apiKey is stored as a reference, not plaintext: "env:VAR_NAME" resolves
// via os.Getenv, "secret:<key>" resolves through the injected SecretStore.
// A literal key passed to Save is auto-stored in the SecretStore when one
// is configured, so the file only ever carries "secret:provider/<id>/api_key";
// without a store, a literal is refused — persisting a key into JSON is a
// deployment defect, not a fallback.
package repoprovider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// ProviderRepository is a JSON-file-backed Provider store. secrets is the
// injected credential store — nil disables "secret:" resolution and
// literal-key auto-storage. The mutex serializes writes — concurrent Save
// on one id would otherwise collide on the shared .tmp path.
type ProviderRepository struct {
	path    string
	secrets drepo.SecretStore
	mu      sync.Mutex
}

func NewProviderRepository(path string, secrets drepo.SecretStore) drepo.Provider {
	return &ProviderRepository{path: path, secrets: secrets}
}

func (r *ProviderRepository) file(id string) string {
	return filepath.Join(r.path, id+".json")
}

// validID is the boundary guard: the id becomes a filename, so the
// domain's own rule (valueobject.ValidID) is re-asserted against this root.
func validID(id string) error {
	if err := valueobject.ValidID(id); err != nil {
		return apperror.New(400, "invalid provider id", fmt.Errorf("id %q: %w", id, err))
	}
	return nil
}

// errUnreadable marks an existing record the filesystem refused to read —
// a permission or I/O fault, not data damage. Listings propagate it so a
// provider can never silently vanish behind an operational fault.
var errUnreadable = errors.New("provider record unreadable")

func (r *ProviderRepository) readProvider(id string) (providerRecord, error) {
	raw, err := os.ReadFile(r.file(id))
	if errors.Is(err, os.ErrNotExist) {
		return providerRecord{}, apperror.NewNotFoundError("provider "+id, err)
	}
	if err != nil {
		return providerRecord{}, fmt.Errorf("%w: %v", errUnreadable, err)
	}
	var rec providerRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return providerRecord{}, fmt.Errorf("provider %q corrupt: %w", id, err)
	}
	return rec, nil
}
