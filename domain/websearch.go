package domain

// Web search provider routing for the web_search tool. The strategy lives
// in Settings (WebSearchStrategy) and is resolved per query by the toolbox
// into a searchwire source restriction. Providers are the search sources
// searchwire can register; the keyed ones (Brave, Serper, Tavily) need an
// API key and are the targets of the round-robin/random rotation.
const (
	// WebSearchStrategyAuto merges every registered source into one
	// metasearch result (the zero-config default).
	WebSearchStrategyAuto = "auto"
	// WebSearchStrategyRoundRobin rotates one API-keyed provider per
	// query, spreading quota across Brave/Serper/Tavily in registration
	// order. Falls back to all sources when no keyed provider is
	// registered.
	WebSearchStrategyRoundRobin = "round_robin"
	// WebSearchStrategyRandom picks one API-keyed provider per query at
	// random. Falls back to all sources when no keyed provider is
	// registered.
	WebSearchStrategyRandom = "random"
)

// WebSearchSourceNames lists every searchwire source the web_search tool
// can be pinned to via a bare strategy value ("provider A/B/…"). Order is
// the settings select order.
var WebSearchSourceNames = []string{"brave", "serper", "tavily", "startpage", "wikipedia", "github"}

// ValidWebSearchStrategy reports whether v is an accepted
// web_search_strategy value. Empty and "auto" are equivalent.
func ValidWebSearchStrategy(v string) bool {
	switch v {
	case "", WebSearchStrategyAuto, WebSearchStrategyRoundRobin, WebSearchStrategyRandom:
		return true
	}
	for _, name := range WebSearchSourceNames {
		if v == name {
			return true
		}
	}
	return false
}
