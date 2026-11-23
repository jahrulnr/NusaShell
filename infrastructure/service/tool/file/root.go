package toolfile

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// checkDir validates the tool's configured sandbox directory at
// construction — the root itself opens per call.
func checkDir(store drepo.Workspace, root string) (string, error) {
	if store == nil {
		return "", errors.New("workspace repository is required")
	}
	return store.CheckRoot(root)
}

// callRoot opens the workspace session path lives in, and returns path
// relative to it (see placeOf). The session's os.Root still confines
// symlinks. The caller owns Close.
func callRoot(ctx context.Context, store drepo.Workspace, fallback, path string) (drepo.WorkspaceSession, string, error) {
	root, rel, err := placeOf(ctx, fallback, path)
	if err != nil {
		return nil, "", err
	}
	s, err := store.Open(ctx, root)
	return s, rel, err
}

// placeOf answers the folder path lives in and path relative to it. A
// relative path resolves in the turn-scoped workspace dir (the ctx value
// the application sets), which wins over the tool's configured directory.
// An absolute path must sit inside the workspace dir or one of the turn's
// extra folders (a project's others); anything else is refused.
func placeOf(ctx context.Context, fallback, path string) (string, string, error) {
	dir := service.WorkspaceDir(ctx)
	if dir == "" {
		dir = fallback
	}
	if !filepath.IsAbs(path) {
		return dir, path, nil
	}
	clean := filepath.Clean(path)
	for _, root := range append([]string{dir}, service.ExtraFolders(ctx)...) {
		rel, err := filepath.Rel(root, clean)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return root, rel, nil
	}
	return "", "", fmt.Errorf("%q is outside the thread's folders", path)
}
