package service

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// CatalogModel is one entry a provider advertises, carrying the metadata
// the provider declares for it. Zero values mean "the provider did not
// say" — merge logic fills unknown fields, it never clears set ones.
type CatalogModel struct {
	ID            string
	Name          string // display name the provider publishes
	Canonical     string // stable base id, e.g. a canonical slug
	AliasOf       string // target id when the entry is a pure alias
	ContextWindow int    // total context window in tokens
	OutputLimit   int    // max completion tokens
	ModelType     enum.ModelType
	// Style is the wire API the provider recommends for this model when it
	// differs per model on one endpoint (e.g. GPT on Responses, Claude on
	// Messages). Only a style the provider's own endpoint serves is declared.
	Style            enum.Style
	InputModalities  []enum.Modality
	OutputModalities []enum.Modality
	SupportedEfforts []enum.Effort
	Pricing          *valueobject.Pricing // per-1M USD rates, nil = undeclared
}

// ModelCatalog lists model entries advertised by a configured provider.
// The adapter owns provider-specific URLs, headers, pagination, and how
// much metadata the surface declares — a thin /models list yields id-only
// entries. The wire id is never rewritten: Canonical/AliasOf describe the
// entry, callers keep sending ID verbatim.
type ModelCatalog interface {
	ListModels(ctx context.Context, provider *aggregate.Provider) ([]CatalogModel, error)
	// ListUpstreams returns the upstream providers a provider can route a
	// model to — aggregator surfaces (OpenRouter-style) expose several;
	// direct providers return empty. Values are {name, slug} pairs the
	// caller echoes back as the model's upstream pin.
	ListUpstreams(ctx context.Context, provider *aggregate.Provider, modelID string) ([]Upstream, error)
}

// Upstream is one routable backend behind a provider surface.
type Upstream struct {
	Name string // display label, e.g. "DeepInfra"
	Slug string // the value a providerID pin carries
}
