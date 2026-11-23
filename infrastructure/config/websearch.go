package config

import (
	"fmt"
	"strings"
	"time"
)

// WebSearchConfig tunes web_search and web_fetch. Zero values mean the
// search library's own default, so an upgraded library's defaults apply
// without a stale copy here. Switches are "on"/"off" strings — an explicit
// false would read as unset and fall back to the default.
//
// Keys are never stored here: a source's key_ref is "env:NAME" or
// "secret:<key>" (the vault), and a literal is refused.
type WebSearchConfig struct {
	Enabled string `json:"enabled" default:"on"`
	// Backend picks who serves the tools: "auto" uses the thread's own
	// provider when it has a native web surface (OmniRoute, OpenCode) and
	// the built-in metasearch otherwise; "searchwire" always uses the
	// metasearch; "provider" uses Provider (and Model) for every thread,
	// falling back to the metasearch when it fails.
	Backend  string `json:"backend" default:"auto"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Limit is the default result count; a call may ask for another.
	Limit            int      `json:"limit"`
	Timeout          Duration `json:"timeout"`
	MaxResponseBytes int64    `json:"max_response_bytes"`
	MaxRawBytes      int64    `json:"max_raw_bytes"`
	UserAgent        string   `json:"user_agent"`

	Brave  WebSearchSource `json:"brave"`
	Serper WebSearchSource `json:"serper"`
	Tavily WebSearchSource `json:"tavily"`
	GitHub WebSearchGitHub `json:"github"`
}

// WebSearchSource is one search source: on or off, and where its key lives.
type WebSearchSource struct {
	Enabled string `json:"enabled"`
	KeyRef  string `json:"key_ref"`
}

// WebSearchGitHub is the GitHub source; its token is optional and raises
// the rate limit.
type WebSearchGitHub struct {
	Enabled      string `json:"enabled" default:"on"`
	SearchIssues string `json:"search_issues" default:"on"`
	TokenRef     string `json:"token_ref"`
}

// Web search bounds. Zero is always allowed and means the default.
const (
	WebSearchLimitMax       = 50
	WebSearchTimeoutMin     = time.Second
	WebSearchTimeoutMax     = 2 * time.Minute
	WebSearchResponseMin    = 1000
	WebSearchResponseMax    = 1 << 20
	WebSearchRawMin         = 64 << 10
	WebSearchRawMax         = 100 << 20
	WebSearchUserAgentMax   = 300
	webSearchBraveDefault   = "on"
	webSearchKeyedDefault   = "off"
	webSearchRefEnvPrefix   = "env:"
	webSearchRefVaultPrefix = "secret:"
)

func (c *WebSearchConfig) Normalize() {
	ApplyDefaults(c)
	// The sources share one type, so their defaults differ by hand: Brave
	// works without a key, Serper and Tavily do nothing without one.
	if c.Brave.Enabled == "" {
		c.Brave.Enabled = webSearchBraveDefault
	}
	if c.Serper.Enabled == "" {
		c.Serper.Enabled = webSearchKeyedDefault
	}
	if c.Tavily.Enabled == "" {
		c.Tavily.Enabled = webSearchKeyedDefault
	}
	c.Provider = strings.TrimSpace(c.Provider)
	c.Model = strings.TrimSpace(c.Model)
	c.UserAgent = strings.TrimSpace(c.UserAgent)
}

func (c WebSearchConfig) validate() error {
	onOff := map[string]string{
		"websearch.enabled": c.Enabled, "websearch.brave.enabled": c.Brave.Enabled,
		"websearch.serper.enabled": c.Serper.Enabled, "websearch.tavily.enabled": c.Tavily.Enabled,
		"websearch.github.enabled": c.GitHub.Enabled, "websearch.github.search_issues": c.GitHub.SearchIssues,
	}
	for key, v := range onOff {
		if v != "on" && v != "off" {
			return fmt.Errorf("%s must be on or off, got %q", key, v)
		}
	}
	switch c.Backend {
	case "auto", "searchwire":
	case "provider":
		if c.Provider == "" {
			return fmt.Errorf("websearch.backend provider needs websearch.provider")
		}
	default:
		return fmt.Errorf("websearch.backend must be auto, searchwire, or provider, got %q", c.Backend)
	}
	if c.Limit < 0 || c.Limit > WebSearchLimitMax {
		return fmt.Errorf("websearch.limit must be 0 (default) to %d, got %d", WebSearchLimitMax, c.Limit)
	}
	if d := time.Duration(c.Timeout); d != 0 && (d < WebSearchTimeoutMin || d > WebSearchTimeoutMax) {
		return fmt.Errorf("websearch.timeout must be 0 (default) or %s to %s, got %s", WebSearchTimeoutMin, WebSearchTimeoutMax, d)
	}
	if n := c.MaxResponseBytes; n != 0 && (n < WebSearchResponseMin || n > WebSearchResponseMax) {
		return fmt.Errorf("websearch.max_response_bytes must be 0 (default) or %d to %d, got %d", WebSearchResponseMin, WebSearchResponseMax, n)
	}
	if n := c.MaxRawBytes; n != 0 && (n < WebSearchRawMin || n > WebSearchRawMax) {
		return fmt.Errorf("websearch.max_raw_bytes must be 0 (default) or %d to %d, got %d", WebSearchRawMin, WebSearchRawMax, n)
	}
	if len(c.UserAgent) > WebSearchUserAgentMax || strings.ContainsAny(c.UserAgent, "\r\n") {
		return fmt.Errorf("websearch.user_agent must be one line of at most %d characters", WebSearchUserAgentMax)
	}
	refs := map[string]string{
		"websearch.brave.key_ref": c.Brave.KeyRef, "websearch.serper.key_ref": c.Serper.KeyRef,
		"websearch.tavily.key_ref": c.Tavily.KeyRef, "websearch.github.token_ref": c.GitHub.TokenRef,
	}
	for key, ref := range refs {
		if ref != "" && !strings.HasPrefix(ref, webSearchRefEnvPrefix) && !strings.HasPrefix(ref, webSearchRefVaultPrefix) {
			return fmt.Errorf("%s must be env:NAME or a vault reference — save the key in Settings → Web search, not in the config file", key)
		}
	}
	return nil
}

// On reports whether the web tools are offered.
func (c WebSearchConfig) On() bool { return c.Enabled != "off" }
