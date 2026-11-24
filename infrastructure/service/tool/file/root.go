package toolfile

import (
	"context"
	"errors"
	"fmt"
	"os"
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
// symlinks and `..` from the folder it opened. The caller owns Close.
func callRoot(ctx context.Context, store drepo.Workspace, fallback, path string) (drepo.WorkspaceSession, string, error) {
	root, rel, err := placeOf(ctx, fallback, path)
	if err != nil {
		return nil, "", err
	}
	s, err := store.Open(ctx, root)
	return s, rel, err
}

// placeOf answers the folder path lives in and path relative to it. The
// path is expanded first (see expandPath): file: URLs, a leading ~ and, on
// Windows, %VAR% become real paths before any folder decision. A relative
// path then resolves in the turn-scoped workspace dir (the ctx value the
// application sets), which wins over the tool's configured directory. An
// absolute path inside the workspace dir or one of the turn's extra folders
// (a project's others) resolves there; an absolute path anywhere else
// anchors at its own deepest existing folder — a plan under ~/.memory, a
// file in /tmp — so naming a path outside the thread's folders is a normal
// call, not a refusal.
func placeOf(ctx context.Context, fallback, path string) (string, string, error) {
	expanded, err := expandPath(path)
	if err != nil {
		return "", "", err
	}
	path = expanded
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
	return anchorOf(clean)
}

// anchorOf answers the deepest existing folder at or above an absolute
// path, and path relative to it. The path itself wins when it is a
// folder (a search over a tree stays rooted there); otherwise the walk
// climbs to the first folder that exists, so a file several missing
// levels deep still opens — write_file creates those levels inside the
// anchor.
func anchorOf(path string) (string, string, error) {
	root := path
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		root = filepath.Dir(root)
	}
	for {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return "", "", err
			}
			return root, rel, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", "", fmt.Errorf("no existing folder above %q", path)
		}
		root = parent
	}
}
