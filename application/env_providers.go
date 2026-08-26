package application

import (
	"fmt"
	"strings"
	"time"

	"nusashell/domain"
)

// envProviderSpec describes a provider whose API key can be supplied through
// an environment variable. NusaShell stores credentials only in the
// SQLite/form-input store; this lets an explicit, opt-in command copy a
// deploy-time secret (for example a Cloud Agent secret) into that store
// without manual UI entry. The server itself never reads these variables.
type envProviderSpec struct {
	ID      string
	EnvVar  string
	Kind    domain.ProviderKind
	Name    string
	BaseURL string
}

// envProviderSpecs is the curated registry of env-seedable providers. Each
// entry uses a stable ID so re-runs update the same record instead of
// creating duplicates. Only well-known endpoints with an unambiguous wire
// format and base URL are listed here.
var envProviderSpecs = []envProviderSpec{
	{
		ID:      "openrouter",
		EnvVar:  "OPENROUTER_API_KEY",
		Kind:    domain.ProviderChat,
		Name:    "OpenRouter",
		BaseURL: "https://openrouter.ai/api/v1",
	},
}

// SeedProvidersFromEnv creates or refreshes provider API keys from
// environment variables and returns a human-readable line per action taken.
// It is meant to be invoked explicitly (e.g. the `seed-providers`
// subcommand), never implicitly during normal server startup.
//
// It is idempotent and non-destructive: it never deletes or disables
// providers and never rewrites a user's custom name or base URL. On first
// run it creates the provider (enabled) and stores its key; on later runs it
// only rewrites the stored key when the env value is present and differs
// (supporting secret rotation). Providers whose key is already current are
// left untouched and produce no action line. Model import is left to the
// normal import flow. getenv is injected so the effect is testable.
func (a *App) SeedProvidersFromEnv(getenv func(string) string) []string {
	if a.Providers == nil || a.Credentials == nil {
		return nil
	}
	var actions []string
	for _, spec := range envProviderSpecs {
		key := strings.TrimSpace(getenv(spec.EnvVar))
		if key == "" {
			continue
		}
		existing, err := a.Providers.Get(spec.ID)
		if err != nil {
			if err := a.Credentials.Set(spec.ID, key); err != nil {
				a.log("warn", "ai", "env seed: failed to store %s credential: %v", spec.Name, err)
				continue
			}
			p := &domain.Provider{
				ID:        spec.ID,
				Kind:      spec.Kind,
				Name:      spec.Name,
				BaseURL:   spec.BaseURL,
				Enabled:   true,
				HasAPIKey: true,
				UpdatedAt: time.Now().UTC(),
			}
			if err := a.Providers.Save(p); err != nil {
				a.log("warn", "ai", "env seed: failed to save provider %s: %v", spec.Name, err)
				continue
			}
			a.log("info", "ai", "seeded provider %s from %s", spec.Name, spec.EnvVar)
			actions = append(actions, fmt.Sprintf("%s created from %s", spec.Name, spec.EnvVar))
			continue
		}
		cur, has, err := a.Credentials.Get(spec.ID)
		if err != nil {
			a.log("warn", "ai", "env seed: failed to read %s credential: %v", spec.Name, err)
			continue
		}
		if has && cur == key {
			continue
		}
		if err := a.Credentials.Set(spec.ID, key); err != nil {
			a.log("warn", "ai", "env seed: failed to update %s credential: %v", spec.Name, err)
			continue
		}
		existing.HasAPIKey = true
		existing.UpdatedAt = time.Now().UTC()
		if err := a.Providers.Save(existing); err != nil {
			a.log("warn", "ai", "env seed: failed to persist %s: %v", spec.Name, err)
			continue
		}
		a.log("info", "ai", "refreshed %s API key from %s", spec.Name, spec.EnvVar)
		actions = append(actions, fmt.Sprintf("%s API key refreshed from %s", spec.Name, spec.EnvVar))
	}
	return actions
}
