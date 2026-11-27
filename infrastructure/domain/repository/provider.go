package repository

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
)

// ProviderSnapshot is the management-plane read of one record: the
// provider hydrated WITHOUT resolving its apiKey — Provider is nil when
// the file can't decode at all, so broken records still enumerate
// instead of hiding. ApiKeyRef is the raw stored value — a literal, an
// "env:"/"secret:" ref, or "" — and KeyResolved reports whether that
// value dereferences to usable key material: refs answer for the
// environment/SecretStore they point at, literals and "" resolve by
// construction.
type ProviderSnapshot struct {
	ID          string
	Provider    *aggregate.Provider
	ApiKeyRef   string
	KeyResolved bool
}

// Provider is a separate aggregate root persisted on its own — credentials,
// endpoint, and model catalog are managed independently so updates propagate
// to every conversation referencing it by id. A missing id is not found,
// reported with a stable error identity.
//
// Secret material is the implementation's call: apiKey may be persisted
// inline, outsourced to a secret store, or stripped and re-injected from the
// environment at Load — the port does not dictate it.
type Provider interface {
	// Save upserts the provider record: style, endpoint, credential, catalog.
	Save(ctx context.Context, prov *aggregate.Provider) error

	// Load rebuilds one provider — the resume path resolves Conversation.ProviderID
	// through it. apiKey arrives resolved: "env:"/"secret:" refs dereference
	// into key material.
	Load(ctx context.Context, id string) (*aggregate.Provider, error)

	// Inspect is the management-plane read: the record hydrated with no
	// key material — ApiKeyRef carries the raw stored value (literal,
	// "env:NAME", "secret:<key>", or ""), never resolved, so an update to
	// other fields can carry the ref forward instead of pinning whatever
	// it resolved to. KeyResolved reports whether that stored value
	// resolves — ref resolution is the adapter's rule, read here
	// non-fatally. A record whose credential doesn't resolve still
	// loads; one that can't even decode returns a snapshot with nil
	// Provider. Not found, with a stable error identity, when absent.
	Inspect(ctx context.Context, id string) (ProviderSnapshot, error)

	// InspectList is List for the management plane: every record appears,
	// including ones Load would fail — an unresolvable key or a corrupt
	// file surfaces as an entry to fix, not a gap in the listing.
	InspectList(ctx context.Context) ([]ProviderSnapshot, error)

	// List returns every configured provider — backs model pickers and provider
	// enumeration.
	List(ctx context.Context) ([]aggregate.Provider, error)

	// Delete removes a provider. Conversations keep their provider reference —
	// it dangles, and the application owns the fallback when resolution
	// fails.
	Delete(ctx context.Context, id string) error
}
