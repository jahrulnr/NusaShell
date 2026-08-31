package tools

import (
	"strings"
	"testing"

	"github.com/jahrulnr/searchwire"
)

// webSearchTestSearcher builds a real searchwire searcher from the given
// credential store so the strategy resolver sees genuine registration.
func webSearchTestSearcher(creds *swTestCreds) *searchwire.Searcher {
	return searchwire.New(SearchwireSearchConfig(creds))
}

func TestWebSearchSourcesAutoAndEmpty(t *testing.T) {
	tb := &Toolbox{}
	s := searchwire.New(SearchwireSearchConfig(nil))
	for _, strategy := range []string{"", "auto"} {
		if got := tb.webSearchSources(strategy, s); got != nil {
			t.Fatalf("strategy %q: got %#v, want nil (all sources)", strategy, got)
		}
	}
}

func TestWebSearchSourcesRoundRobinCyclesKeyedProviders(t *testing.T) {
	creds := &swTestCreds{keys: map[string]string{
		CredentialSerperWebSearch: "serper-key",
		CredentialTavilyWebSearch: "tavily-key",
	}}
	tb := &Toolbox{Credentials: creds}
	s := webSearchTestSearcher(creds)

	var got []string
	for i := 0; i < 4; i++ {
		got = append(got, tb.webSearchSources("round_robin", s)[0])
	}
	want := []string{"serper", "tavily", "serper", "tavily"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rotation = %#v, want %#v", got, want)
		}
	}
}

func TestWebSearchSourcesRoundRobinNoKeyedProviders(t *testing.T) {
	tb := &Toolbox{}
	s := webSearchTestSearcher(&swTestCreds{keys: map[string]string{}})
	if got := tb.webSearchSources("round_robin", s); got != nil {
		t.Fatalf("got %#v, want nil (all sources)", got)
	}
	// Even with env-only keys the provider joins the rotation. The
	// searcher must be built after the env var is set: searchwire reads
	// env vars at construction time.
	t.Setenv("TAVILY_API_KEY", "env-tavily")
	tb = &Toolbox{}
	s = webSearchTestSearcher(&swTestCreds{keys: map[string]string{}})
	if got := tb.webSearchSources("round_robin", s); len(got) != 1 || got[0] != "tavily" {
		t.Fatalf("env-key rotation = %#v, want [tavily]", got)
	}
}

func TestWebSearchSourcesRandomStaysInKeyedPool(t *testing.T) {
	creds := &swTestCreds{keys: map[string]string{
		CredentialBraveWebSearch:  "brave-key",
		CredentialSerperWebSearch: "serper-key",
		CredentialTavilyWebSearch: "tavily-key",
	}}
	tb := &Toolbox{Credentials: creds}
	s := webSearchTestSearcher(creds)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		got := tb.webSearchSources("random", s)
		if len(got) != 1 {
			t.Fatalf("random returned %#v", got)
		}
		if got[0] != "brave" && got[0] != "serper" && got[0] != "tavily" {
			t.Fatalf("random picked %q outside keyed pool", got[0])
		}
		seen[got[0]] = true
	}
	if !seen["brave"] || !seen["serper"] || !seen["tavily"] {
		t.Fatalf("random never covered the full pool: %#v", seen)
	}
}

func TestWebSearchSourcesFixedRegisteredSource(t *testing.T) {
	creds := &swTestCreds{keys: map[string]string{CredentialSerperWebSearch: "serper-key"}}
	tb := &Toolbox{Credentials: creds}
	s := webSearchTestSearcher(creds)
	if got := tb.webSearchSources("serper", s); len(got) != 1 || got[0] != "serper" {
		t.Fatalf("fixed serper = %#v", got)
	}
	// Free sources can be pinned without any key.
	if got := tb.webSearchSources("wikipedia", s); len(got) != 1 || got[0] != "wikipedia" {
		t.Fatalf("fixed wikipedia = %#v", got)
	}
}

func TestWebSearchSourcesFixedUnregisteredFallsBackToAll(t *testing.T) {
	// Serper has no key (stored or env) → not registered → fall back to
	// all sources instead of failing the search.
	tb := &Toolbox{}
	s := webSearchTestSearcher(&swTestCreds{keys: map[string]string{}})
	if got := tb.webSearchSources("serper", s); got != nil {
		t.Fatalf("got %#v, want nil (all sources)", got)
	}
}

func TestWebSearchSourcesNilSearcher(t *testing.T) {
	tb := &Toolbox{}
	if got := tb.webSearchSources("round_robin", nil); got != nil {
		t.Fatalf("got %#v, want nil", got)
	}
	if got := tb.webSearchSources("brave", nil); got != nil {
		t.Fatalf("got %#v, want nil", got)
	}
}

func TestWebSearchResolvedKeyPrefersStoredKeyOverEnv(t *testing.T) {
	t.Setenv("SERPER_API_KEY", "from-env")
	creds := &swTestCreds{keys: map[string]string{CredentialSerperWebSearch: "from-store"}}
	if got := webSearchResolvedKey(creds, CredentialSerperWebSearch, "SERPER_API_KEY"); got != "from-store" {
		t.Fatalf("key = %q, want from-store", got)
	}
	if got := webSearchResolvedKey(nil, CredentialSerperWebSearch, "SERPER_API_KEY"); got != "from-env" {
		t.Fatalf("env key = %q, want from-env", got)
	}
	if got := webSearchResolvedKey(nil, CredentialSerperWebSearch, "SERPER_API_KEY"); strings.TrimSpace(got) == "" {
		t.Fatal("expected non-empty env key")
	}
}
