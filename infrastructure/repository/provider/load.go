package repoprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Load reads <id>.json and hydrates the aggregate. The apiKey record
// dereferences here so the file itself stays credential-free: "env:NAME"
// reads the environment, "secret:<key>" hits the SecretStore, anything
// else passes through as literal key material. An unset env var fails
// the load rather than resolving to "" — a silent empty key becomes an
// upstream 401.
func (r *ProviderRepository) Load(ctx context.Context, id string) (*aggregate.Provider, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	rec, err := r.readProvider(id)
	if err != nil {
		return nil, err
	}
	key, err := r.resolveKey(ctx, id, rec.ApiKey)
	if err != nil {
		return nil, err
	}
	return decodeProvider(rec, key)
}

// resolveKey is the one dispatch over stored apiKey values: "env:NAME"
// reads the environment, "secret:<key>" hits the SecretStore, anything
// else — a literal or "" — passes through verbatim. Load treats a
// failure as fatal; Inspect reads the same decision non-fatally as
// ProviderSnapshot.KeyResolved.
func (r *ProviderRepository) resolveKey(ctx context.Context, id, stored string) (string, error) {
	key := stored
	if name, ok := strings.CutPrefix(key, valueobject.KeyRefEnv); ok {
		if key = os.Getenv(name); key == "" {
			return "", fmt.Errorf("provider %q: apiKey references env %q which is unset", id, name)
		}
	} else if sk, ok := strings.CutPrefix(key, valueobject.KeyRefSecret); ok {
		if r.secrets == nil {
			return "", fmt.Errorf("provider %q: apiKey references %q but no secret store is configured", id, sk)
		}
		resolved, err := r.secrets.Get(ctx, sk)
		if err != nil {
			// A missing key is record-level — the stored ref dangles and
			// List skips it like any undecodable file. Anything else is
			// a store outage, not data damage: mark it unreadable so
			// List propagates rather than silently dropping a provider
			// whose key is temporarily unreachable.
			if apperror.As(err).StatusCode() != 404 {
				return "", fmt.Errorf("provider %q: %w: %v", id, errUnreadable, err)
			}
			return "", fmt.Errorf("provider %q: %w", id, err)
		}
		key = resolved
	}
	return key, nil
}

// keyResolves probes the stored value through resolveKey — Inspect's
// resolution flag, the same decision Load enforces. A record-level
// failure (a dangling ref: unset env, missing secret) reports
// unresolved; an operational store failure carries errUnreadable and
// propagates, so a vault outage never looks like every provider is
// broken data.
func (r *ProviderRepository) keyResolves(ctx context.Context, id, stored string) (bool, error) {
	_, err := r.resolveKey(ctx, id, stored)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errUnreadable) {
		return false, err
	}
	return false, nil
}
