package repoagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
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
	return writeJSONAtomic(r.path, r.catalogFile(id), id+".catalog.json.tmp-*", cat)
}

func (r *Repository) commandCatalogFile(conversationID string) string {
	return filepath.Join(r.path, "conversations", conversationID+".commands.json")
}

// CommandCatalog returns the slash-command list one conversation's agent
// session last reported. A missing file is the zero value: the session never
// reported one.
func (r *Repository) CommandCatalog(ctx context.Context, conversationID string) (drepo.AgentCommandCatalog, error) {
	if err := valueobject.ValidID(conversationID); err != nil {
		return drepo.AgentCommandCatalog{}, apperror.New(400, "invalid conversation id", err)
	}
	var cat drepo.AgentCommandCatalog
	raw, err := os.ReadFile(r.commandCatalogFile(conversationID))
	if errors.Is(err, os.ErrNotExist) {
		return cat, nil
	}
	if err != nil {
		return cat, err
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		return cat, fmt.Errorf("conversation %q command catalog corrupt: %w", conversationID, err)
	}
	return cat, nil
}

// SaveCommandCatalog replaces the conversation's last reported command list
// atomically. An empty list is a real report and replaces what came before.
func (r *Repository) SaveCommandCatalog(ctx context.Context, conversationID string, cat drepo.AgentCommandCatalog) error {
	if err := valueobject.ValidID(conversationID); err != nil {
		return apperror.New(400, "invalid conversation id", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := filepath.Join(r.path, "conversations")
	return writeJSONAtomic(dir, r.commandCatalogFile(conversationID), conversationID+".commands.json.tmp-*", cat)
}

// DeleteCommandCatalog removes the conversation's command report. Missing is
// success because delete is idempotent from the caller's side.
func (r *Repository) DeleteCommandCatalog(ctx context.Context, conversationID string) error {
	if err := valueobject.ValidID(conversationID); err != nil {
		return apperror.New(400, "invalid conversation id", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.Remove(r.commandCatalogFile(conversationID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// writeJSONAtomic writes one small repository document through a unique temp
// file and rename, so a crash leaves the old document or none, never a torn one.
func writeJSONAtomic(dir, target, tmpPattern string, v any) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, tmpPattern)
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
	return os.Rename(tmp, target)
}
