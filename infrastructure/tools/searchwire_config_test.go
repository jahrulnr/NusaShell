package tools

import (
	"testing"

	"nusashell/domain"

	"github.com/jahrulnr/searchwire"
)

type swTestCreds struct {
	keys map[string]string
}

func (c *swTestCreds) Get(id string) (string, bool, error) {
	k, ok := c.keys[id]
	return k, ok, nil
}
func (c *swTestCreds) Set(id, key string) error                { return nil }
func (c *swTestCreds) Delete(id string) error                  { return nil }
func (c *swTestCreds) ListByPrefix(p string) ([]string, error) { return nil, nil }

type swTestProviders struct {
	items map[string]*domain.Provider
}

func (s *swTestProviders) List() []*domain.Provider {
	out := make([]*domain.Provider, 0, len(s.items))
	for _, p := range s.items {
		out = append(out, p)
	}
	return out
}
func (s *swTestProviders) Get(id string) (*domain.Provider, error) { return s.items[id], nil }
func (s *swTestProviders) Save(p *domain.Provider) error           { return nil }
func (s *swTestProviders) Delete(id string) error                  { return nil }

func TestSearchwireConfigFromProviders(t *testing.T) {
	providers := &swTestProviders{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
		"openai":     {ID: "openai", Enabled: true, Kind: domain.ProviderResponses, BaseURL: "https://api.openai.com/v1"},
		"anthropic":  {ID: "anthropic", Enabled: true, Kind: domain.ProviderMessages, BaseURL: "https://api.anthropic.com"},
		"perplexity": {ID: "perplexity", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://api.perplexity.ai"},
		"xai":        {ID: "xai", Enabled: true, Kind: domain.ProviderResponses, BaseURL: "https://api.x.ai/v1"},
		"disabled":   {ID: "disabled", Enabled: false, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
		"nokey":      {ID: "nokey", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
		"unknown":    {ID: "unknown", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://custom.example.com/v1"},
	}}
	creds := &swTestCreds{keys: map[string]string{
		"openrouter": "or-key",
		"openai":     "oai-key",
		"anthropic":  "ant-key",
		"perplexity": "ppx-key",
		"xai":        "xai-key",
		"disabled":   "dis-key",
		"unknown":    "unk-key",
	}}

	cfg := SearchwireConfigFromProviders(providers, creds)

	if cfg.OpenRouter.APIKey != "or-key" {
		t.Errorf("OpenRouter key = %q, want or-key", cfg.OpenRouter.APIKey)
	}
	if cfg.OpenAI.APIKey != "oai-key" {
		t.Errorf("OpenAI key = %q, want oai-key", cfg.OpenAI.APIKey)
	}
	if cfg.Anthropic.APIKey != "ant-key" {
		t.Errorf("Anthropic key = %q, want ant-key", cfg.Anthropic.APIKey)
	}
	if cfg.Perplexity.APIKey != "ppx-key" {
		t.Errorf("Perplexity key = %q, want ppx-key", cfg.Perplexity.APIKey)
	}
	if cfg.XAI.APIKey != "xai-key" {
		t.Errorf("XAI key = %q, want xai-key", cfg.XAI.APIKey)
	}
}

func TestSearchwireConfigFromProvidersDisabled(t *testing.T) {
	providers := &swTestProviders{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: false, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
	}}
	creds := &swTestCreds{keys: map[string]string{"openrouter": "or-key"}}

	cfg := SearchwireConfigFromProviders(providers, creds)
	if cfg.OpenRouter.APIKey != "" {
		t.Errorf("disabled provider should not contribute key, got %q", cfg.OpenRouter.APIKey)
	}
}

func TestSearchwireConfigFromProvidersNoKey(t *testing.T) {
	providers := &swTestProviders{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
	}}
	creds := &swTestCreds{keys: map[string]string{}}

	cfg := SearchwireConfigFromProviders(providers, creds)
	if cfg.OpenRouter.APIKey != "" {
		t.Errorf("provider without key should not contribute, got %q", cfg.OpenRouter.APIKey)
	}
}

func TestSearchwireConfigFromProvidersNil(t *testing.T) {
	cfg := SearchwireConfigFromProviders(nil, nil)
	if cfg.OpenRouter.APIKey != "" {
		t.Error("nil stores should produce empty config")
	}
}

func TestSearchwireSearchConfigWiresKeyedSources(t *testing.T) {
	creds := &swTestCreds{keys: map[string]string{
		CredentialBraveWebSearch:  "brave-key",
		CredentialSerperWebSearch: "serper-key",
		CredentialTavilyWebSearch: "tavily-key",
	}}
	cfg := SearchwireSearchConfig(creds)
	if cfg.Brave.APIKey != "brave-key" {
		t.Errorf("brave key = %q, want brave-key", cfg.Brave.APIKey)
	}
	if cfg.Serper.APIKey != "serper-key" || cfg.Serper.Enabled == nil || !*cfg.Serper.Enabled {
		t.Errorf("serper = %#v, want key serper-key and enabled", cfg.Serper)
	}
	if cfg.Tavily.APIKey != "tavily-key" || cfg.Tavily.Enabled == nil || !*cfg.Tavily.Enabled {
		t.Errorf("tavily = %#v, want key tavily-key and enabled", cfg.Tavily)
	}
}

func TestSearchwireSearchConfigDeclaresKeyedSourcesWithoutStoredKeys(t *testing.T) {
	cfg := SearchwireSearchConfig(&swTestCreds{keys: map[string]string{}})
	// Keys may be empty (env fallback), but Serper/Tavily must be declared
	// enabled so searchwire can pick up their env vars.
	if cfg.Brave.APIKey != "" || cfg.Serper.APIKey != "" || cfg.Tavily.APIKey != "" {
		t.Errorf("expected empty stored keys, got %#v", cfg)
	}
	if cfg.Serper.Enabled == nil || !*cfg.Serper.Enabled || cfg.Tavily.Enabled == nil || !*cfg.Tavily.Enabled {
		t.Errorf("serper/tavily must be declared enabled for env fallback: %#v", cfg)
	}
}

func TestSearchwireSearchConfigNilCreds(t *testing.T) {
	cfg := SearchwireSearchConfig(nil)
	if cfg.Brave.APIKey != "" || cfg.Serper.APIKey != "" || cfg.Tavily.APIKey != "" {
		t.Errorf("nil creds must yield empty keys, got %#v", cfg)
	}
}

func TestSearchwireSearchConfigEnvFallbackRegistersSources(t *testing.T) {
	// No stored keys; env vars alone must register Serper/Tavily.
	t.Setenv("SERPER_API_KEY", "env-serper")
	t.Setenv("TAVILY_API_KEY", "env-tavily")
	cfg := SearchwireSearchConfig(&swTestCreds{keys: map[string]string{}})
	names := searchwire.New(cfg).Sources()
	for _, want := range []string{"serper", "tavily"} {
		if !containsString(names, want) {
			t.Errorf("source %q not registered from env: %#v", want, names)
		}
	}
}
