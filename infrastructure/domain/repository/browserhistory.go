package repository

import (
	"context"
	"time"
)

// BrowserVisit is a page a browser profile visited: how often, and when
// last.
type BrowserVisit struct {
	URL       string
	Title     string
	Visits    int
	VisitedAt time.Time
}

// BrowserHistory keeps the pages each browser profile visited.
type BrowserHistory interface {
	// Record counts a visit to url at at; an empty title keeps the one
	// known.
	Record(ctx context.Context, profile, url, title string, at time.Time) error
	// Search returns up to limit of a profile's pages. An empty query is
	// the most recent; otherwise pages whose url or title contains it,
	// those starting with it first, then the most visited.
	Search(ctx context.Context, profile, query string, limit int) ([]BrowserVisit, error)
}
