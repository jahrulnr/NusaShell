package repoagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// catalogSuffix marks the second JSON document per agent — the wire
// catalog. One directory holds two document kinds: plain <id>.json is the
// agent record, <id>+catalogSuffix is the catalog — List must read only
// the first kind.
const catalogSuffix = ".catalog.json"

// The agent catalog — the offered surface the agent last reported — is a
// second JSON document per agent, <path>/<agentID>.catalog.json, written
// atomically. It carries only wire data (option/model names and values,
// capabilities): no launch environment, no secrets.

func (r *Repository) catalogFile(id string) string {
	return filepath.Join(r.path, id+catalogSuffix)
}

// Catalog returns the agent's last seen offered surface; a missing catalog
// is the zero value, not an error — nothing was ever seen.
func (r *Repository) Catalog(ctx context.Context, id string) (drepo.AgentCatalog, error) {
	if err := validID(id); err != nil {
		return drepo.AgentCatalog{}, err
	}
	var cat drepo.AgentCatalog
	raw, err := os.ReadFile(r.catalogFile(id))
	if errors.Is(err, os.ErrNotExist) {
		return cat, nil
	}
	if err != nil {
		return drepo.AgentCatalog{}, err
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		return drepo.AgentCatalog{}, fmt.Errorf("agent %q catalog corrupt: %w", id, err)
	}
	return cat, nil
}

// SaveCatalog upserts the catalog atomically. The record holds wire values
// only — callers must not put launch environment or key material in it.
func (r *Repository) SaveCatalog(ctx context.Context, id string, cat drepo.AgentCatalog) error {
	if err := validID(id); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.MkdirAll(r.path, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(r.path, id+".catalog.json.tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_ = f.Chmod(0o600)
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, r.catalogFile(id))
}
