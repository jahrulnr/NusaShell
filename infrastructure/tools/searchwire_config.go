// Package tools searchwire_config.go builds a searchwire.Config from the
// providers and credentials already configured in NusaShell, so web_answer
// works without separate environment variables.
package tools

import (
	"os"
	"strings"

	"nusashell/application"

	"github.com/jahrulnr/searchwire"
)

// SearchwireConfigFromProviders builds a searchwire.Config using API keys
// from NusaShell's configured providers. When a provider's BaseURL matches a
// known vendor (OpenRouter, OpenAI, Anthropic, Perplexity, xAI), its API key
// is passed to searchwire so web_answer works without separate env vars.
//
// Brave is not a chat provider in NusaShell, so it still relies on the
// BRAVE_SEARCH_API_KEY env var if the user wants the Brave answer provider.
func SearchwireConfigFromProviders(providers application.ProviderStore, creds application.CredentialStore) searchwire.Config {
	cfg := searchwire.Config{}
	if providers == nil || creds == nil {
		return cfg
	}
	for _, p := range providers.List() {
		if !p.Enabled {
			continue
		}
		key, has, _ := creds.Get(p.ID)
		if !has || strings.TrimSpace(key) == "" {
			continue
		}
		host := strings.ToLower(p.BaseURL)
		switch {
		case strings.Contains(host, "openrouter.ai"):
			cfg.OpenRouter = searchwire.OpenRouterConfig{APIKey: key}
		case strings.Contains(host, "api.openai.com"):
			cfg.OpenAI = searchwire.OpenAIConfig{APIKey: key}
		case strings.Contains(host, "anthropic.com"):
			cfg.Anthropic = searchwire.AnthropicConfig{APIKey: key}
		case strings.Contains(host, "perplexity.ai"):
			cfg.Perplexity = searchwire.PerplexityConfig{APIKey: key}
		case strings.Contains(host, "api.x.ai") || strings.Contains(host, "xai"):
			cfg.XAI = searchwire.XAIConfig{APIKey: key}
		}
	}
	return cfg
}

// CredentialStore ids that hold web_search provider API keys. They are
// write-only (set via Settings → Web Search, never returned by
// settings.get); an empty stored value falls back to the standard
// searchwire environment variables (BRAVE_SEARCH_API_KEY, SERPER_API_KEY,
// TAVILY_API_KEY).
const (
	CredentialBraveWebSearch  = "web_search_brave"
	CredentialSerperWebSearch = "web_search_serper"
	CredentialTavilyWebSearch = "web_search_tavily"
)

// SearchwireSearchConfig builds the searchwire.Config for the web_search
// tool. Brave, Serper, and Tavily are always declared with the stored API
// key (possibly empty); searchwire falls back to the standard environment
// variables (BRAVE_SEARCH_API_KEY, SERPER_API_KEY, TAVILY_API_KEY) for any
// provider without a stored key. Serper and Tavily are only *registered*
// when a key resolves (stored or env); Brave stays registered by default
// (public HTML results), matching zero-config searchwire.
func SearchwireSearchConfig(creds application.CredentialStore) searchwire.Config {
	cfg := searchwire.Config{}
	keyOf := func(id string) string {
		if creds == nil {
			return ""
		}
		key, has, _ := creds.Get(id)
		if !has {
			return ""
		}
		return strings.TrimSpace(key)
	}
	enabled := true
	cfg.Brave = searchwire.BraveConfig{APIKey: keyOf(CredentialBraveWebSearch)}
	cfg.Serper = searchwire.SerperConfig{Enabled: &enabled, APIKey: keyOf(CredentialSerperWebSearch)}
	cfg.Tavily = searchwire.TavilyConfig{Enabled: &enabled, APIKey: keyOf(CredentialTavilyWebSearch)}
	return cfg
}

// webSearchKeyEnv maps each API-keyed web search provider to its
// CredentialStore id and standard searchwire environment variable. The
// round-robin/random rotation uses this to decide which providers carry a
// real key.
var webSearchKeyEnv = []struct {
	name string
	id   string
	env  string
}{
	{name: "brave", id: CredentialBraveWebSearch, env: "BRAVE_SEARCH_API_KEY"},
	{name: "serper", id: CredentialSerperWebSearch, env: "SERPER_API_KEY"},
	{name: "tavily", id: CredentialTavilyWebSearch, env: "TAVILY_API_KEY"},
}

// webSearchResolvedKey returns the provider key from the credential store,
// falling back to the standard environment variable when not stored.
func webSearchResolvedKey(creds application.CredentialStore, id, env string) string {
	if creds != nil {
		if key, has, _ := creds.Get(id); has {
			if key = strings.TrimSpace(key); key != "" {
				return key
			}
		}
	}
	return strings.TrimSpace(os.Getenv(env))
}
