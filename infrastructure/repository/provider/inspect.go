package repoprovider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Inspect reads the record and hydrates it WITHOUT resolving the stored
// apiKey — the aggregate carries no key material and the raw stored value
// comes back beside it, so the management plane renders records whose
// credential is unresolvable. Resolution is still answered: KeyResolved
// probes the same dispatch Load enforces, non-fatally. A file that can't
// even decode still surfaces as a snapshot with nil Provider rather than
// hiding; only a truly absent id is 404, and genuine I/O failures
// (permissions, IO errors) propagate — they are operational faults, not
// corruption. The snapshot's ID is always the filename — the addressable
// id — even when the record decodes with a mismatched embedded id.
func (r *ProviderRepository) Inspect(ctx context.Context, id string) (drepo.ProviderSnapshot, error) {
	if err := validID(id); err != nil {
		return drepo.ProviderSnapshot{}, err
	}
	raw, err := os.ReadFile(r.file(id))
	if errors.Is(err, os.ErrNotExist) {
		return drepo.ProviderSnapshot{}, apperror.NewNotFoundError("provider "+id, err)
	}
	if err != nil {
		return drepo.ProviderSnapshot{}, err
	}
	var rec providerRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		// Corrupt JSON — recover the apiKey ref best-effort so a repair
		// upsert can still clean up the vaulted material it points at.
		var stub struct {
			ApiKey string `json:"apiKey"`
		}
		_ = json.Unmarshal(raw, &stub)
		resolved, err := r.keyResolves(ctx, id, stub.ApiKey)
		if err != nil {
			return drepo.ProviderSnapshot{}, err
		}
		return drepo.ProviderSnapshot{ID: id, ApiKeyRef: stub.ApiKey,
			KeyResolved: resolved}, nil
	}
	resolved, err := r.keyResolves(ctx, id, rec.ApiKey)
	if err != nil {
		return drepo.ProviderSnapshot{}, err
	}
	g, err := decodeProvider(rec, "")
	if err != nil {
		return drepo.ProviderSnapshot{ID: id, ApiKeyRef: rec.ApiKey, KeyResolved: resolved}, nil
	}
	return drepo.ProviderSnapshot{ID: id, Provider: g, ApiKeyRef: rec.ApiKey, KeyResolved: resolved}, nil
}

// InspectList snapshots every <id>.json in the directory — including
// records Load would fail on — sorted by id for stable ordering.
func (r *ProviderRepository) InspectList(ctx context.Context) ([]drepo.ProviderSnapshot, error) {
	entries, err := os.ReadDir(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []drepo.ProviderSnapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		snap, err := r.Inspect(ctx, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			// An id that fails validID can't be addressed by any op —
			// the file is only fixable on disk, so skip it. A real I/O
			// failure propagates so `list` doesn't hide a fault.
			if apperror.As(err).StatusCode() == 400 {
				continue
			}
			return nil, err
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
