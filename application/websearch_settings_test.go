package application

import (
	"encoding/json"
	"strings"
	"testing"

	"nusashell/contracts"
)

// webSearchFakeCreds is a CredentialStore with real Set/Delete for the
// settings roundtrip tests (the shared fakeCreds no-ops mutations).
type webSearchFakeCreds struct {
	keys map[string]string
}

func (c *webSearchFakeCreds) Get(id string) (string, bool, error) {
	k, ok := c.keys[id]
	return k, ok, nil
}
func (c *webSearchFakeCreds) Set(id, key string) error { c.keys[id] = key; return nil }
func (c *webSearchFakeCreds) Delete(id string) error   { delete(c.keys, id); return nil }
func (c *webSearchFakeCreds) ListByPrefix(p string) ([]string, error) {
	out := []string{}
	for id := range c.keys {
		if strings.HasPrefix(id, p) {
			out = append(out, id)
		}
	}
	return out, nil
}

func TestHandleSettingsSetWebSearchStrategy(t *testing.T) {
	app := NewApp(Deps{Settings: &fakeSettings{}})

	bad := "all-at-once"
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{WebSearchStrategy: &bad}); err == nil {
		t.Fatal("invalid strategy must fail validation")
	}

	for _, ok := range []string{"", "auto", "round_robin", "random", "brave", "serper", "tavily", "startpage", "wikipedia", "github"} {
		v := ok
		if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{WebSearchStrategy: &v}); err != nil {
			t.Fatalf("strategy %q must be accepted: %v", v, err)
		}
		if got := app.Settings.Get().WebSearchStrategy; got != v {
			t.Fatalf("stored strategy = %q, want %q", got, v)
		}
	}

	// The value round-trips through settings.get untainted (no key fields).
	result, rpcErr := app.handleSettingsGet()
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if got := result.(contracts.SettingsGetResult).Settings.WebSearchStrategy; got != "github" {
		t.Fatalf("DTO strategy = %q, want github", got)
	}
}

func TestHandleSettingsSetWebSearchAPIKeys(t *testing.T) {
	app := NewApp(Deps{
		Settings:    &fakeSettings{},
		Credentials: &webSearchFakeCreds{keys: map[string]string{}},
	})

	brave := "brave-secret"
	serper := "serper-secret"
	tavily := "tavily-secret"
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{
		WebSearchBraveAPIKey:  &brave,
		WebSearchSerperAPIKey: &serper,
		WebSearchTavilyAPIKey: &tavily,
	}); err != nil {
		t.Fatal(err)
	}
	fc := app.Credentials.(*webSearchFakeCreds)
	if fc.keys["web_search_brave"] != "brave-secret" ||
		fc.keys["web_search_serper"] != "serper-secret" ||
		fc.keys["web_search_tavily"] != "tavily-secret" {
		t.Fatalf("stored keys = %#v", fc.keys)
	}
	// Keys must never leak into settings.json / settings.get.
	if strings.Contains(mustMarshalSettings(t, app), "brave-secret") {
		t.Fatal("API key leaked into stored settings")
	}

	// Empty value clears the stored key (env fallback takes over).
	empty := ""
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{WebSearchSerperAPIKey: &empty}); err != nil {
		t.Fatal(err)
	}
	if _, ok := fc.keys["web_search_serper"]; ok {
		t.Fatal("empty key must delete the stored credential")
	}
}

func mustMarshalSettings(t *testing.T, app *App) string {
	t.Helper()
	data, err := json.Marshal(app.Settings.Get())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
