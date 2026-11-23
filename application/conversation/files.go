package appconversation

import (
	"context"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// lineSuffix is an editor position ("main.go:12" or "main.go:12:4") an
// agent appends to a path it links.
var lineSuffix = regexp.MustCompile(`(?::[0-9]+){1,2}$`)

// OpenFile opens a local file a thread links to: an absolute path, or one
// relative to the place's primary folder. A path that is missing as
// written is retried without an editor :line suffix. The owner sees what
// the server can read — this serves the owner's own machine to them.
func (app *ConversationApp) OpenFile(ctx context.Context, p Place, path string) (io.ReadCloser, fs.FileInfo, string, error) {
	ws, roots, err := app.Roots(ctx, p)
	if err != nil {
		return nil, nil, "", err
	}
	if path == "" {
		return nil, nil, "", apperror.New(400, "a file path is required", nil)
	}
	if !filepath.IsAbs(path) {
		if len(roots) == 0 {
			return nil, nil, "", apperror.New(400, "a relative path needs a workspace", nil)
		}
		path = filepath.Join(roots[0], path)
	}
	path = filepath.Clean(path)
	rc, info, err := ws.OpenHost(ctx, path)
	if err != nil {
		if bare := lineSuffix.ReplaceAllString(path, ""); bare != path {
			path = bare
			rc, info, err = ws.OpenHost(ctx, path)
		}
	}
	if err != nil {
		return nil, nil, "", apperror.New(404, "the file is missing, unreadable, or not a regular file", err)
	}
	return rc, info, path, nil
}
