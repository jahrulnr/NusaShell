// Package provider is the WebUI provider-route boundary — list/get reads
// and the full-replace PUT/DELETE mutations, with the sanitized
// projection (key state, never key material or raw refs) owned here.
package provider

import (
	"context"

	"github.com/gin-gonic/gin"

	appprovider "github.com/jahrulnr/nusashell/application/provider"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// Reader is the read surface — the sanitized ProviderInfo projection,
// never a raw record. *appprovider.ProviderApp satisfies it.
type Reader interface {
	Get(ctx context.Context, id string) (appprovider.ProviderInfo, error)
	List(ctx context.Context) ([]appprovider.ProviderInfo, error)
	DiscoverUpstreams(ctx context.Context, id, modelID string) ([]dservice.Upstream, error)
}

// Writer is the mutation surface — full-document upsert, catalog
// re-import, and removal. *appprovider.ProviderApp satisfies it.
type Writer interface {
	Upsert(ctx context.Context, in appprovider.ProviderInput) (appprovider.UpsertOutcome, error)
	ImportModels(ctx context.Context, id string) (appprovider.UpsertOutcome, error)
	Delete(ctx context.Context, id string) error
}

// Deps wires the use cases; Reads and Writes are optional — nil leaves
// the corresponding routes unregistered.
type Deps struct {
	Reads  Reader
	Writes Writer
	// Jobs is the server-lifecycle context for an accepted delete.
	// A dropped connection must not cancel a removal halfway; server
	// shutdown still cancels it. Nil means Background.
	Jobs context.Context
}

// Register mounts this boundary's routes under /api.
func Register(api *gin.RouterGroup, d Deps) {
	jobs := d.Jobs
	if jobs == nil {
		jobs = context.Background()
	}
	s := &server{reads: d.Reads, writes: d.Writes, jobs: jobs}
	if d.Reads != nil {
		api.GET("/providers", s.list)
		api.GET("/providers/:id", s.get)
		api.GET("/providers/:id/upstreams", s.upstreams)
	}
	if d.Writes != nil {
		api.DELETE("/providers/:id", s.delete)
		// PUT and import answer with the write outcome merged into a fresh
		// read projection — a Writes-only composition has no projection to
		// render, so those routes stay unregistered rather than mounting a
		// handler that dereferences a nil reader.
		if d.Reads != nil {
			api.PUT("/providers/:id", s.put)
			api.POST("/providers/:id/import-models", s.importModels)
		}
	}
}

type server struct {
	reads  Reader
	writes Writer
	jobs   context.Context
}
