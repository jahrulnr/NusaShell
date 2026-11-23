package service

import (
	"context"
	"time"
)

// WebBackend is who serves web_search and web_fetch.
type WebBackend string

const (
	// WebBackendAuto uses the thread's own provider when its endpoint has
	// a native web surface, else the built-in metasearch.
	WebBackendAuto WebBackend = "auto"
	// WebBackendSearchwire always uses the built-in metasearch.
	WebBackendSearchwire WebBackend = "searchwire"
	// WebBackendProvider uses one chosen provider for every thread.
	WebBackendProvider WebBackend = "provider"
)

// WebSearchSettings are the live web tool settings with credentials
// resolved. Zero numbers and an empty user agent mean the search
// library's defaults; an empty key lets the library read its own
// environment variable.
type WebSearchSettings struct {
	Enabled          bool
	Backend          WebBackend
	Provider         WebProvider // set when Backend is WebBackendProvider
	Limit            int
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxRawBytes      int64
	UserAgent        string
	Brave            WebSearchSource
	Serper           WebSearchSource
	Tavily           WebSearchSource
	GitHub           WebSearchGitHub
}

type WebSearchSource struct {
	Enabled bool
	Key     string
}

type WebSearchGitHub struct {
	Enabled      bool
	SearchIssues bool
	Token        string
}

// WebSearchConfigurer answers the current web tool settings. Tools ask on
// every call, so a change in Settings applies at once.
type WebSearchConfigurer interface {
	WebSearchSettings(ctx context.Context) (WebSearchSettings, error)
}
