package appbrowser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

type consoleSession struct {
	dservice.BrowserSession
	ran []string
}

func (s *consoleSession) Console(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`[{"level":"warn","text":"careful","ts":7}]`), nil
}

func (s *consoleSession) Eval(_ context.Context, expr string) (json.RawMessage, error) {
	s.ran = append(s.ran, expr)
	if expr == "big" {
		return json.RawMessage(`"` + strings.Repeat("x", maxScriptValue) + `"`), nil
	}
	return json.RawMessage(`2`), nil
}

type consoleBrowser struct {
	dservice.Browser
	s *consoleSession
}

func (b *consoleBrowser) Session(convID string) (dservice.BrowserSession, bool) {
	if convID != "c_open" {
		return nil, false
	}
	return b.s, true
}

// The owner's console reads and runs in an open page only, and keeps
// scripts and their answers to a size a page can show.
func TestOwnerConsole(t *testing.T) {
	ctx := context.Background()
	s := &consoleSession{}
	app := &App{browser: &consoleBrowser{s: s}}

	got, err := app.PageConsole(ctx, "c_open")
	if err != nil || len(got) != 1 || got[0] != (ConsoleEntry{Level: "warn", Text: "careful", At: 7}) {
		t.Fatalf("console = %+v %v", got, err)
	}
	if _, err := app.PageConsole(ctx, "c_closed"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("a closed browser has no console: %v", err)
	}
	if v, err := app.RunScript(ctx, "c_open", "  1 + 1 "); err != nil || string(v) != "2" || s.ran[0] != "1 + 1" {
		t.Fatalf("run = %s %v %q", v, err, s.ran)
	}
	for _, c := range []struct {
		conv, expr string
		status     int
	}{
		{"c_open", "   ", 400},
		{"c_open", strings.Repeat("x", maxScript+1), 413},
		{"c_open", "big", 413},
		{"c_closed", "1", 404},
	} {
		if _, err := app.RunScript(ctx, c.conv, c.expr); apperror.As(err).StatusCode() != c.status {
			t.Errorf("%s %.10q: %v, want %d", c.conv, c.expr, err, c.status)
		}
	}
}
