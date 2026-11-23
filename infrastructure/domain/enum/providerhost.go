package enum

import (
	"net/url"
	"strings"
)

// ProviderHost enumerates known provider hosts as the anchor for per-provider
// wire characteristics: adapters branch on provider identity for quirks
// like OpenCode's x-opencode-session header or OpenRouter's attribution
// headers and prompt_cache_key. The constants are consumed through
// MatchesEndpoint; ParseProviderHost is for adapters that need the branch
// explicitly. Keep this enum even while usage is thin — it is the
// registry new provider quirks are expected to key off.
type ProviderHost string

const (
	OpenAI      ProviderHost = "api.openai.com"
	OpenRouter  ProviderHost = "openrouter.ai"
	OpenCode    ProviderHost = "opencode.ai"
	Anthropic   ProviderHost = "api.anthropic.com"
	Deepseek    ProviderHost = "api.deepseek.com"
	Gemini      ProviderHost = "generativelanguage.googleapis.com"
	GLM         ProviderHost = "api.z.ai"
	TokenRouter ProviderHost = "api.tokenrouter.com"
)

func (r ProviderHost) String() string {
	return string(r)
}

// MatchesEndpoint reports whether a provider endpoint's host is this
// provider. DNS hosts are case-insensitive, so the match must be too —
// an endpoint saved as HTTPS://OPENROUTER.AI/API is still OpenRouter.
// The host is the identity: a lookalike (notopencode.ai), a foreign
// subdomain (openrouter.ai.evil.com), or the provider string sitting in
// a path are different origins and must not inherit this provider's
// headers or quirks.
func (r ProviderHost) MatchesEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), string(r))
}

func ParseProviderHost(v string) (ProviderHost, bool) {
	switch r := ProviderHost(v); r {
	case OpenAI, OpenRouter, OpenCode, Anthropic, Deepseek, Gemini, GLM, TokenRouter:
		return r, true
	}
	return "unknown", false
}
