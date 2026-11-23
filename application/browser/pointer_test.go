package appbrowser

import (
	"context"
	"fmt"
	"testing"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

type pointerSession struct {
	dservice.BrowserSession
	log []string
}

func (s *pointerSession) Page() dservice.BrowserPage {
	return dservice.BrowserPage{Width: 1000, Height: 800}
}
func (s *pointerSession) ShowAgentPointer(x, y float64, action string) {
	s.log = append(s.log, fmt.Sprintf("show %s %g,%g", action, x, y))
}
func (s *pointerSession) Click(_ context.Context, x, y float64, _ string, _ int) error {
	s.log = append(s.log, fmt.Sprintf("click %g,%g", x, y))
	return nil
}
func (s *pointerSession) Move(_ context.Context, x, y float64, _ int) error {
	s.log = append(s.log, fmt.Sprintf("move %g,%g", x, y))
	return nil
}
func (s *pointerSession) Scroll(_ context.Context, x, y, _, _ float64) error {
	s.log = append(s.log, fmt.Sprintf("scroll %g,%g", x, y))
	return nil
}

type pointerBrowser struct {
	dservice.Browser
	s *pointerSession
}

func (b *pointerBrowser) Open(context.Context, string) (dservice.BrowserSession, error) {
	return b.s, nil
}

// Viewers see where the agent acts before it does.
func TestAgentPointerLeadsItsAction(t *testing.T) {
	ctx := context.Background()
	s := &pointerSession{}
	app := &App{browser: &pointerBrowser{s: s}}
	half, x := 0.5, 300.0
	if _, err := app.Click(ctx, "c", ClickInput{Point: Point{X: &half, Y: &half}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Hover(ctx, "c", Point{X: &x, Y: &x}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Scroll(ctx, "c", ScrollInput{DeltaY: 100}); err != nil {
		t.Fatal(err)
	}
	want := []string{"show click 500,400", "click 500,400", "show hover 300,300", "move 300,300", "show scroll 500,400", "scroll 500,400"}
	if fmt.Sprint(s.log) != fmt.Sprint(want) {
		t.Fatalf("log = %q", s.log)
	}
}
