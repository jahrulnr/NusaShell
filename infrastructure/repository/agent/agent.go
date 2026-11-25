// Package repoagent implements the Agent repository port as one JSON
// document per agent: <path>/<agentID>.json, written atomically.
//
// Launch environment values never sit in the file as plaintext: "env:NAME"
// and "secret:<key>" refs are kept verbatim, and a literal value is moved
// into the injected SecretStore first so the record only carries its
// "secret:agent/<id>/env/<NAME>" reference. Load resolves refs for launch;
// Inspect and List leave them unresolved.
package repoagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

type agentRecord struct {
	ID      string            `json:"id"`
	Name    string            `json:"name,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Policy  string            `json:"policy"`
	// AutoStart and Defaults are the owner's settings for the agent's
	// process and for the session options its rooms start with.
	AutoStart bool                         `json:"autoStart,omitempty"`
	Defaults  valueobject.ConfigSelections `json:"defaults,omitempty"`
}

// Repository is the JSON-file Agent store. The mutex serializes writes.
type Repository struct {
	path    string
	secrets drepo.SecretStore
	mu      sync.Mutex
}

// New returns the store rooted at path. secrets is required for literal
// environment values; without it Save refuses them.
func New(path string, secrets drepo.SecretStore) *Repository {
	return &Repository{path: path, secrets: secrets}
}

func (r *Repository) file(id string) string { return filepath.Join(r.path, id+".json") }

func validID(id string) error {
	if err := valueobject.ValidID(id); err != nil {
		return apperror.New(400, "invalid agent id", fmt.Errorf("id %q: %w", id, err))
	}
	// An id ending in the catalog suffix writes <id>.json — the file List
	// skips as a catalog document — so the agent could never be listed.
	if strings.HasSuffix(id, ".catalog") {
		return apperror.New(400, "invalid agent id", fmt.Errorf("id %q ends in the catalog suffix", id))
	}
	return nil
}

func (r *Repository) read(id string) (agentRecord, error) {
	raw, err := os.ReadFile(r.file(id))
	if errors.Is(err, os.ErrNotExist) {
		return agentRecord{}, apperror.NewNotFoundError("agent "+id, err)
	}
	if err != nil {
		return agentRecord{}, err
	}
	var rec agentRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return agentRecord{}, fmt.Errorf("agent %q corrupt: %w", id, err)
	}
	if rec.ID != id {
		return agentRecord{}, fmt.Errorf("agent %q corrupt: id %q does not match its file", id, rec.ID)
	}
	return rec, nil
}

func decode(rec agentRecord, env map[string]string) (*aggregate.Agent, error) {
	a, err := aggregate.NewAgent(rec.ID, rec.Name, rec.Command, rec.Args, enum.ApprovalPolicy(rec.Policy))
	if err != nil {
		return nil, fmt.Errorf("agent %q corrupt: %w", rec.ID, err)
	}
	if err := a.SetEnv(env); err != nil {
		return nil, fmt.Errorf("agent %q corrupt: %w", rec.ID, err)
	}
	if err := a.SetDefaults(rec.Defaults); err != nil {
		return nil, fmt.Errorf("agent %q corrupt: %w", rec.ID, err)
	}
	a.SetAutoStart(rec.AutoStart)
	return &a, nil
}

// Save upserts the record atomically. Literal environment values move into
// the secret store; secrets the agent no longer names are deleted.
func (r *Repository) Save(ctx context.Context, a *aggregate.Agent) error {
	if err := validID(a.ID()); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	prev, prevErr := r.read(a.ID())
	rec := agentRecord{ID: a.ID(), Name: a.Name(), Command: a.Command(), Args: a.Args(), Policy: a.Policy().String(),
		AutoStart: a.AutoStart(), Defaults: a.Defaults()}
	env := a.Env()
	if len(env) > 0 {
		rec.Env = make(map[string]string, len(env))
	}
	for name, v := range env {
		if valueobject.IsKeyRef(v) {
			rec.Env[name] = v
			continue
		}
		if r.secrets == nil {
			return apperror.NewInternalError("agent secret storage unconfigured",
				fmt.Errorf("literal env %s for %q requires a secret store", name, a.ID()))
		}
		key := drepo.AgentSecretKey(a.ID(), name)
		if err := r.secrets.Set(ctx, key, v); err != nil {
			return err
		}
		rec.Env[name] = valueobject.SecretRef(key)
	}
	if err := os.MkdirAll(r.path, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(r.path, a.ID()+".json.tmp-*")
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
	if err := os.Rename(tmp, r.file(a.ID())); err != nil {
		return err
	}
	if prevErr == nil {
		for name, v := range prev.Env {
			if _, still := rec.Env[name]; still {
				continue
			}
			r.dropOwnedSecret(ctx, a.ID(), name, v)
		}
	}
	return nil
}

// dropOwnedSecret deletes a vault entry the record owned — only the
// canonical key for this agent and variable, never a shared ref.
func (r *Repository) dropOwnedSecret(ctx context.Context, id, name, ref string) {
	if r.secrets == nil {
		return
	}
	key := drepo.AgentSecretKey(id, name)
	if ref == valueobject.SecretRef(key) {
		_ = r.secrets.Delete(ctx, key)
	}
}

// Load returns the agent with every environment ref resolved. An unset
// "env:" variable or a missing secret fails the load: launching with a
// silently empty credential fails later and less clearly.
func (r *Repository) Load(ctx context.Context, id string) (*aggregate.Agent, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	rec, err := r.read(id)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(rec.Env))
	for name, v := range rec.Env {
		switch {
		case strings.HasPrefix(v, valueobject.KeyRefEnv):
			ref, _ := valueobject.KeyRefName(v)
			val, ok := os.LookupEnv(ref)
			if !ok {
				return nil, apperror.New(400, "agent environment unresolved", fmt.Errorf("agent %q: %s references unset variable %s", id, name, ref))
			}
			env[name] = val
		case strings.HasPrefix(v, valueobject.KeyRefSecret):
			if r.secrets == nil {
				return nil, apperror.NewInternalError("agent secret storage unconfigured", fmt.Errorf("agent %q env %s", id, name))
			}
			key, _ := valueobject.KeyRefName(v)
			val, err := r.secrets.Get(ctx, key)
			if err != nil {
				return nil, err
			}
			env[name] = val
		default:
			env[name] = v
		}
	}
	return decode(rec, env)
}

// Inspect returns the agent with environment refs unresolved.
func (r *Repository) Inspect(_ context.Context, id string) (*aggregate.Agent, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	rec, err := r.read(id)
	if err != nil {
		return nil, err
	}
	return decode(rec, rec.Env)
}

// List returns every agent sorted by id, refs unresolved.
func (r *Repository) List(ctx context.Context) ([]aggregate.Agent, error) {
	matches, err := filepath.Glob(filepath.Join(r.path, "*.json"))
	if err != nil {
		return nil, err
	}
	slices.Sort(matches)
	out := make([]aggregate.Agent, 0, len(matches))
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasSuffix(base, catalogSuffix) {
			continue
		}
		a, err := r.Inspect(ctx, strings.TrimSuffix(base, ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, nil
}

// Delete removes the record and the secrets it owns.
func (r *Repository) Delete(ctx context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, err := r.read(id)
	if err != nil {
		return err
	}
	if err := os.Remove(r.file(id)); err != nil {
		return err
	}
	if err := os.Remove(r.catalogFile(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for name, v := range rec.Env {
		r.dropOwnedSecret(ctx, id, name, v)
	}
	return nil
}

var _ drepo.Agent = (*Repository)(nil)
var _ drepo.AgentCatalogStore = (*Repository)(nil)
