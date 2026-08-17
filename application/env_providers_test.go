package application

import (
	"testing"

	"nusashell/domain"
)

func newSeedTestApp() (*App, *fakeProviderStore, *memCreds) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{}}
	creds := &memCreds{m: map[string]string{}}
	app := &App{
		Providers:   providers,
		Credentials: creds,
		Logs:        &fakeLogStore{},
		Bus:         NewBus(),
	}
	return app, providers, creds
}

func TestSeedProvidersFromEnvCreatesProvider(t *testing.T) {
	app, providers, creds := newSeedTestApp()

	actions := app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-or-test"}))

	if len(actions) != 1 {
		t.Fatalf("expected 1 action line, got %v", actions)
	}

	p, err := providers.Get("openrouter")
	if err != nil {
		t.Fatalf("expected seeded provider, got error: %v", err)
	}
	if p.Kind != domain.ProviderChat {
		t.Errorf("kind = %q, want chat", p.Kind)
	}
	if p.Name != "OpenRouter" {
		t.Errorf("name = %q, want OpenRouter", p.Name)
	}
	if p.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("base url = %q", p.BaseURL)
	}
	if !p.Enabled {
		t.Error("seeded provider should be enabled")
	}
	if !p.HasAPIKey {
		t.Error("seeded provider should report HasAPIKey")
	}
	key, has, _ := creds.Get("openrouter")
	if !has || key != "sk-or-test" {
		t.Errorf("credential = %q (has=%v), want sk-or-test", key, has)
	}
}

func TestSeedProvidersFromEnvSkipsWhenUnset(t *testing.T) {
	app, providers, _ := newSeedTestApp()

	app.SeedProvidersFromEnv(mapEnv(map[string]string{}))

	if len(providers.List()) != 0 {
		t.Fatalf("no provider should be seeded without env, got %d", len(providers.List()))
	}
}

func TestSeedProvidersFromEnvSkipsBlankValue(t *testing.T) {
	app, providers, _ := newSeedTestApp()

	app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "   "}))

	if len(providers.List()) != 0 {
		t.Fatalf("blank env value must not seed a provider, got %d", len(providers.List()))
	}
}

func TestSeedProvidersFromEnvIsIdempotent(t *testing.T) {
	app, providers, creds := newSeedTestApp()
	env := mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-or-test"})

	app.SeedProvidersFromEnv(env)
	second := app.SeedProvidersFromEnv(env)

	if len(second) != 0 {
		t.Fatalf("second run with an unchanged key must report no action, got %v", second)
	}
	if n := len(providers.List()); n != 1 {
		t.Fatalf("running twice must not duplicate providers, got %d", n)
	}
	key, _, _ := creds.Get("openrouter")
	if key != "sk-or-test" {
		t.Errorf("credential = %q, want sk-or-test", key)
	}
}

func TestSeedProvidersFromEnvRefreshesRotatedKey(t *testing.T) {
	app, providers, creds := newSeedTestApp()
	app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-old"}))

	actions := app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-new"}))

	if len(actions) != 1 {
		t.Fatalf("rotation should report one refresh action, got %v", actions)
	}
	key, _, _ := creds.Get("openrouter")
	if key != "sk-new" {
		t.Errorf("credential = %q, want sk-new (rotated)", key)
	}
	if n := len(providers.List()); n != 1 {
		t.Fatalf("rotation must not duplicate providers, got %d", n)
	}
}

func TestSeedProvidersFromEnvPreservesUserEdits(t *testing.T) {
	app, providers, creds := newSeedTestApp()
	// A user who renamed the provider, pointed it at a custom gateway, and
	// disabled it — with the same key the env would supply.
	providers.Save(&domain.Provider{
		ID:      "openrouter",
		Kind:    domain.ProviderChat,
		Name:    "My Router",
		BaseURL: "https://gateway.internal/v1",
		Enabled: false,
	})
	creds.Set("openrouter", "sk-or-test")

	app.SeedProvidersFromEnv(mapEnv(map[string]string{"OPENROUTER_API_KEY": "sk-or-test"}))

	p, _ := providers.Get("openrouter")
	if p.Name != "My Router" || p.BaseURL != "https://gateway.internal/v1" || p.Enabled {
		t.Fatalf("seeder overwrote user edits: %+v", p)
	}
}

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}
