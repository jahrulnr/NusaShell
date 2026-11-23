package appconversation

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// UseShells sets how the owner's terminals start.
func (app *ConversationApp) UseShells(s dservice.Shells) {
	app.mu.Lock()
	app.shells = s
	app.mu.Unlock()
}

// OpenTerminal starts the owner's shell in one of the folders a turn at
// the place works in — the primary one unless dir names another or a
// folder inside one. Only a thread on this machine has a terminal here.
func (app *ConversationApp) OpenTerminal(ctx context.Context, p Place, dir string, cols, rows int) (dservice.Shell, string, error) {
	app.mu.Lock()
	shells := app.shells
	app.mu.Unlock()
	if shells == nil {
		return nil, "", apperror.New(503, "terminals are unavailable", nil)
	}
	if p.ConversationID != "" {
		conv, err := app.depsSnapshot().Convs.Load(ctx, p.ConversationID)
		if err != nil {
			return nil, "", err
		}
		if conv.Environment() != enum.EnvironmentLocal {
			return nil, "", apperror.New(400, "this thread works on another machine; its terminal is not here", nil)
		}
	}
	_, roots, err := app.Roots(ctx, p)
	if err != nil {
		return nil, "", err
	}
	if len(roots) == 0 {
		return nil, "", apperror.New(400, "this thread has no folder to open a terminal in", nil)
	}
	if dir == "" {
		dir = roots[0]
	}
	dir = filepath.Clean(dir)
	inside := false
	for _, root := range roots {
		if rel, err := filepath.Rel(root, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			inside = true
			break
		}
	}
	if !inside {
		return nil, "", apperror.New(400, "the terminal opens in the thread's folders only", nil)
	}
	sh, err := shells.Open(ctx, dservice.ShellCommand{Dir: dir}, cols, rows)
	if err != nil {
		return nil, "", apperror.New(500, "could not start the terminal", err)
	}
	return sh, dir, nil
}
