package job

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// hydrateGate rules on the turn root before any one-shot hydration record
// is rendered: the root must not be the driver's fallback (a home that
// happens to be a dotfiles repo is not the room's project) and must carry
// a project marker at its top level. A project binding is not evidence —
// the marker at the root is. The room is never consulted for its project:
// the gate is root-based, so every room follows the same rule.
//
// The verdict is remembered on the room as the root it refused, so a
// gated-out room pays one probe per episode instead of one per step. The
// value is keyed by root: a rebind to another root re-evaluates by
// comparison (no write needed), and Compact, ClearHistory, and Fork drop it
// because those transitions make the old verdict meaningless. The fallback
// check is free, so a fallback refusal is never stored. A failed gate writes
// nothing else: no record, no skip line, no probe of the map.
//
// Only proof counts as an answer: "no marker here" is a verdict, an
// unreadable root is not. When the session cannot be opened, a marker stat
// fails for any reason other than absence, or the root cannot be listed, the
// step is skipped without judging and without remembering — a permission
// error, an I/O failure, or a dropped link says nothing about the tree, and a
// verdict frozen from one would outlive the fault.
func (d Deps) hydrateGate(ctx context.Context, conv *aggregate.Conversation, driver EnvironmentDriver, root string) bool {
	if root == "" || root == driver.FallbackRoot() {
		return false
	}
	// A remembered refusal of this root: no probe this step. A different
	// root is a new question, and the probe below answers it.
	if conv.SymbolMapRefusedAt() == root {
		return false
	}
	store := driver.Workspace()
	if store == nil {
		return false
	}
	session, err := store.Open(ctx, root)
	if err != nil {
		return false
	}
	defer session.Close()
	// Stat by name: ".git" never appears in a directory listing the
	// workspace port returns, so a repository root answers only here. An
	// absent name is evidence; any other error is a failed probe.
	allowed := false
	for _, name := range dservice.ProjectMarkers() {
		_, err := session.Stat(name)
		if err == nil {
			allowed = true
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return false
		}
	}
	// One listing covers the glob markers: no fixed name can express
	// "any .csproj/.sln/build.gradle.* at the root".
	if !allowed {
		fsys := session.FS()
		if fsys == nil {
			return false
		}
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			return false
		}
		globs := dservice.ProjectMarkerGlobs()
		for _, entry := range entries {
			if slices.ContainsFunc(globs, func(glob string) bool {
				ok, _ := filepath.Match(glob, entry.Name())
				return ok
			}) {
				allowed = true
				break
			}
		}
	}
	// Remember the answer on the room so the next step does not pay for the
	// same probe. Saving now — not with the turn's later snapshot — keeps
	// it through a turn that dies early; the save is best-effort because
	// the verdict is a cache of a deterministic probe, so a failure costs
	// one re-probe, never correctness.
	changed := false
	switch {
	case allowed && conv.SymbolMapRefusedAt() != "":
		conv.AllowSymbolMap()
		changed = true
	case !allowed:
		conv.RefuseSymbolMap(root)
		changed = true
	}
	if changed && d.Convs != nil {
		_ = d.Convs.Save(ctx, conv)
	}
	return allowed
}
