package jsonstore

import (
	"os"
	"path/filepath"
	"testing"

	"nusashell/domain"
)

func TestProviderMigrationNormalizesBuiltInDrivers(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `[
		{"ID":"anthropic","Kind":"anthropic","Name":"Anthropic","BaseURL":"https://api.anthropic.com"},
		{"ID":"openai","Kind":"openai","Name":"OpenAI","BaseURL":"https://api.openai.com/v1"},
		{"ID":"openrouter","Kind":"chat","Name":"OpenRouter","BaseURL":"https://openrouter.ai/api/v1"}
	]`
	if err := os.WriteFile(filepath.Join(configDir, "providers.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]struct {
		driver domain.ProviderDriver
		kind   domain.ProviderKind
	}{
		"anthropic":  {driver: domain.ProviderDriverAnthropic, kind: domain.ProviderMessages},
		"openai":     {driver: domain.ProviderDriverOpenAI, kind: domain.ProviderResponses},
		"openrouter": {driver: domain.ProviderDriverOpenRouter, kind: domain.ProviderChat},
	}
	for _, provider := range store.ListProviders() {
		expected, ok := want[provider.ID]
		if !ok {
			t.Fatalf("unexpected provider %q", provider.ID)
		}
		if provider.Driver != expected.driver || provider.Kind != expected.kind {
			t.Fatalf("provider %q = driver %q, kind %q; want driver %q, kind %q",
				provider.ID, provider.Driver, provider.Kind, expected.driver, expected.kind)
		}
		delete(want, provider.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing migrated providers: %v", want)
	}
}
