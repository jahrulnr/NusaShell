// Package appprovider is the provider management plane: registrations,
// edits, catalog updates, and removals for the API providers
// conversations run on. It is the sibling of appconversation — that app
// resolves a room's provider to serve turns; this one owns the lifecycle
// of the providers themselves.
//
// Credential rule: key material is write-only here and lives in the
// SecretStore — the provider record carries only its "secret:<key>" name.
// Upsert performs the vault CRUD itself (set on literal, delete on
// clear); keep-on-update carries the stored ref verbatim so env:/secret:
// indirections survive. Get/List answer a sanitized projection — KeyRef
// exposes the key name, never the resolved material — so transports can
// render "configured" without a key ever crossing the boundary.
package appprovider

import (
	"sync"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// ProviderApp fronts the drepo.Provider port plus the SecretStore the
// records reference by name. Unlike conversations there is no
// per-resource worker — providers are few, ops are single record writes,
// and the repository already serializes them.
//
// mu serializes each inspect→save read-modify-write (Upsert carries
// stored hidden fields forward; Delete can interleave mid-window). It
// is process-local by nature — a CLI process editing the same store
// concurrently is outside its reach.
type ProviderApp struct {
	providers drepo.Provider
	secrets   drepo.SecretStore
	catalog   dservice.ModelCatalog
	mu        sync.Mutex
}

func New(providers drepo.Provider, secrets drepo.SecretStore, catalog dservice.ModelCatalog) *ProviderApp {
	return &ProviderApp{providers: providers, secrets: secrets, catalog: catalog}
}
