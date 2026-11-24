// Package symbol holds the shared mechanics for service.SymbolIndex
// adapters: the skip policy every adapter applies, a repository.Workspace
// adapter into service.SymbolRoot, and the bounded map renderer. Engine
// adapters live in the subpackages (gotreesitter, regex, ctags).
package symbol

import (
	"context"
	"io/fs"
	"path"
	"strings"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// MaxFileBytes bounds a single indexable file. Larger files are skipped
// outright: they are disproportionately generated bundles and minified
// sources whose parse cost dwarfs their outline value.
const MaxFileBytes = 256 * 1024

// skipDirNames are dependency and build-output folders never worth indexing.
// Dot-directories are skipped wholesale — .git, .idea, .venv all hide under
// one rule.
var skipDirNames = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	"out":          true,
	"__pycache__":  true,
}

// ShouldIndex reports whether rel is worth indexing under the default
// size cap. It is the one place the vendor/generated/oversize policy
// lives; every SymbolIndex adapter applies it so consumers see a uniform
// map regardless of engine.
func ShouldIndex(rel string, size int64) bool {
	return ShouldIndexWithin(rel, size, MaxFileBytes)
}

// ShouldIndexWithin is ShouldIndex with a caller-chosen byte cap — the
// symbols.max_file_bytes setting flows through here so the map's file
// filter and the consumer's change detector agree on the same boundary.
func ShouldIndexWithin(rel string, size int64, maxBytes int64) bool {
	return SkipReason(rel, size, maxBytes) == ""
}

// WorkspaceRoot adapts a repository.Workspace session into a
// service.SymbolRoot. The session stays open for the root's lifetime;
// Close it when done indexing.
type WorkspaceRoot struct {
	dir  string
	ws   drepo.Workspace
	sess drepo.WorkspaceSession
}

// OpenWorkspaceRoot opens dir on ws and returns it as a SymbolRoot.
func OpenWorkspaceRoot(ctx context.Context, ws drepo.Workspace, dir string) (*WorkspaceRoot, error) {
	dir, err := ws.CheckRoot(dir)
	if err != nil {
		return nil, err
	}
	sess, err := ws.Open(ctx, dir)
	if err != nil {
		return nil, err
	}
	return &WorkspaceRoot{dir: dir, ws: ws, sess: sess}, nil
}

// Name returns the workspace directory the root was opened on — used by
// tooling that keys per-repo data on the root's basename.
func (r *WorkspaceRoot) Name() string { return r.dir }

func (r *WorkspaceRoot) Close() error {
	if r.sess != nil {
		return r.sess.Close()
	}
	return nil
}

func (r *WorkspaceRoot) Walk(ctx context.Context, visit func(rel string, size int64) error) error {
	fsRoot := isFilesystemRoot(r.dir)
	return r.ws.Entries(ctx, r.dir, func(rel string, isDir bool) error {
		if isDir {
			base := path.Base(rel)
			if strings.HasPrefix(base, ".") || skipDirNames[base] || skipAtFsRoot(rel, true, fsRoot) {
				return fs.SkipDir
			}
			return nil
		}
		info, err := r.sess.Stat(rel)
		if err != nil {
			return nil
		}
		return visit(rel, info.Size())
	})
}

func (r *WorkspaceRoot) ReadFile(_ context.Context, rel string) ([]byte, error) {
	return r.sess.ReadFile(rel)
}

var _ service.SymbolRoot = (*WorkspaceRoot)(nil)
