package appbrowser

import (
	"context"
	"net/url"
	"strings"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// Bounds of an address-bar suggestion list.
const (
	historyLimit    = 8
	maxHistoryLimit = 50
)

// UseHistory keeps the pages each profile's tabs load, for the address
// bar's suggestions.
func (a *App) UseHistory(h drepo.BrowserHistory) {
	a.history = h
	if a.browser != nil {
		a.browser.OnLoad(a.recordVisit)
	}
}

// recordVisit keeps a loaded web page. Only http and https pages are
// places the owner can go back to; blank tabs, data: and file: pages are
// not.
func (a *App) recordVisit(l dservice.BrowserLoaded) {
	u, err := url.Parse(l.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.history.Record(ctx, l.Profile, l.URL, strings.TrimSpace(l.Title), time.Now())
}

// Visited suggests pages a profile visited: the most recent for an empty
// query, else those matching it. An empty profile is the default one.
func (a *App) Visited(ctx context.Context, profile, query string, limit int) ([]drepo.BrowserVisit, error) {
	if a.history == nil {
		return nil, nil
	}
	if profile == "" && a.browser != nil {
		profile = a.browser.Profiles()[0]
	}
	if limit <= 0 {
		limit = historyLimit
	}
	return a.history.Search(ctx, profile, query, min(limit, maxHistoryLimit))
}
