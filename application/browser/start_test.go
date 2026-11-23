package appbrowser

import (
	"context"
	"testing"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// startSession and startBrowser implement only what Start calls; the
// embedded interfaces are never reached.
type startSession struct {
	dservice.BrowserSession
	navigated []string
}

func (s *startSession) Navigate(_ context.Context, url string) error {
	s.navigated = append(s.navigated, url)
	return nil
}

type startBrowser struct {
	dservice.Browser
	session *startSession
	opened  []string
	err     error
}

func (b *startBrowser) Open(_ context.Context, convID string) (dservice.BrowserSession, error) {
	b.opened = append(b.opened, convID)
	if b.err != nil {
		return nil, b.err
	}
	return b.session, nil
}

func TestStart(t *testing.T) {
	ctx := context.Background()
	newApp := func() (*App, *startBrowser) {
		b := &startBrowser{session: &startSession{}}
		return &App{browser: b}, b
	}

	t.Run("opens a blank tab when no address is given", func(t *testing.T) {
		app, b := newApp()
		if err := app.Start(ctx, "c_1", "  "); err != nil {
			t.Fatal(err)
		}
		if len(b.opened) != 1 || b.opened[0] != "c_1" || len(b.session.navigated) != 0 {
			t.Fatalf("opened=%v navigated=%v", b.opened, b.session.navigated)
		}
	})

	t.Run("goes to the address, adding https to a bare host", func(t *testing.T) {
		app, b := newApp()
		if err := app.Start(ctx, "c_1", "example.com/docs"); err != nil {
			t.Fatal(err)
		}
		if got := b.session.navigated; len(got) != 1 || got[0] != "https://example.com/docs" {
			t.Fatalf("navigated = %v", got)
		}
	})

	t.Run("refuses an address that is not http or https before opening anything", func(t *testing.T) {
		app, b := newApp()
		err := app.Start(ctx, "c_1", "file:///etc/passwd")
		if got := apperror.As(err).StatusCode(); got != 400 {
			t.Fatalf("status = %d, err = %v", got, err)
		}
		if len(b.opened) != 0 {
			t.Fatalf("a browser was opened for a refused address: %v", b.opened)
		}
	})

	t.Run("needs a thread", func(t *testing.T) {
		app, _ := newApp()
		if got := apperror.As(app.Start(ctx, "", "")).StatusCode(); got != 400 {
			t.Fatalf("status = %d", got)
		}
	})

	t.Run("passes on a browser that cannot start", func(t *testing.T) {
		app, b := newApp()
		b.err = apperror.New(503, "no browser found", nil)
		if got := apperror.As(app.Start(ctx, "c_1", "")).StatusCode(); got != 503 {
			t.Fatalf("status = %d", got)
		}
	})
}
