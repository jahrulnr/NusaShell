package appbrowser

import (
	"context"
	"fmt"
	"testing"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

type historyBrowser struct {
	dservice.Browser
	onLoad func(dservice.BrowserLoaded)
}

func (b *historyBrowser) OnLoad(fn func(dservice.BrowserLoaded)) { b.onLoad = fn }
func (b *historyBrowser) Profiles() []string                     { return []string{"default", "work"} }

type memHistory struct {
	recorded []string
	searched []string
}

func (h *memHistory) Record(_ context.Context, profile, url, title string, _ time.Time) error {
	h.recorded = append(h.recorded, profile+" "+url+" "+title)
	return nil
}

func (h *memHistory) Search(_ context.Context, profile, query string, limit int) ([]drepo.BrowserVisit, error) {
	h.searched = append(h.searched, fmt.Sprintf("%s|%s|%d", profile, query, limit))
	return nil, nil
}

// Only web pages a tab loaded are kept, and suggestions read the
// default profile unless another is named.
func TestVisitedPages(t *testing.T) {
	b, h := &historyBrowser{}, &memHistory{}
	app := &App{browser: b}
	app.UseHistory(h)
	for _, l := range []dservice.BrowserLoaded{
		{Profile: "default", URL: "https://example.com/", Title: " Example "},
		{Profile: "default", URL: "about:blank"},
		{Profile: "default", URL: "data:text/html,hi"},
		{Profile: "default", URL: "file:///etc/hosts"},
		{Profile: "work", URL: "http://intranet.test/"},
	} {
		b.onLoad(l)
	}
	if len(h.recorded) != 2 || h.recorded[0] != "default https://example.com/ Example" || h.recorded[1] != "work http://intranet.test/ " {
		t.Fatalf("recorded = %q", h.recorded)
	}
	ctx := context.Background()
	_, _ = app.Visited(ctx, "", "ex", 0)
	_, _ = app.Visited(ctx, "work", "", 500)
	if h.searched[0] != "default|ex|8" || h.searched[1] != "work||50" {
		t.Fatalf("searched = %q", h.searched)
	}
}
