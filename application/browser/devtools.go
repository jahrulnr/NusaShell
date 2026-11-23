package appbrowser

import (
	"context"
	"encoding/json"
	"strings"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Bounds of the owner's console: a script typed there, and the answer it
// may bring back.
const (
	maxScript      = 64 << 10
	maxScriptValue = 256 << 10
)

// ConsoleEntry is a line the page logged, or an error it threw.
type ConsoleEntry struct {
	Level string `json:"level"`
	Text  string `json:"text"`
	At    int64  `json:"ts"` // Unix ms
}

// PageConsole is what the thread's open page logged since it loaded,
// oldest first. The owner's console reads it; it never opens a browser.
func (a *App) PageConsole(ctx context.Context, convID string) ([]ConsoleEntry, error) {
	s, ok := a.browser.Session(convID)
	if !ok {
		return nil, apperror.New(404, "the thread's browser is not open", nil)
	}
	raw, err := s.Console(ctx)
	if err != nil {
		return nil, err
	}
	out := []ConsoleEntry{}
	if json.Unmarshal(raw, &out) != nil {
		return nil, apperror.New(502, "the page's console could not be read", nil)
	}
	return out, nil
}

// RunScript runs JavaScript the owner typed in the page and answers its
// value as JSON, awaiting a promise. Like the console, it needs the
// thread's browser open already.
func (a *App) RunScript(ctx context.Context, convID, expression string) (json.RawMessage, error) {
	expr := strings.TrimSpace(expression)
	if expr == "" {
		return nil, apperror.New(400, "type something to run", nil)
	}
	if len(expr) > maxScript {
		return nil, apperror.New(413, "the script is too long", nil)
	}
	s, ok := a.browser.Session(convID)
	if !ok {
		return nil, apperror.New(404, "the thread's browser is not open", nil)
	}
	raw, err := s.Eval(ctx, expr)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxScriptValue {
		return nil, apperror.New(413, "the result is too large to show", nil)
	}
	return raw, nil
}
