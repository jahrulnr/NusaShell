package aggregate

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ErrProjectChanged rejects a write based on an out-of-date read of a
// project: someone saved it in between.
var ErrProjectChanged = errors.New("project was changed elsewhere")

// Project groups conversations that work on the same folders. Its first
// folder is the primary one — the working directory of every room in the
// project; the others are extra folders those rooms may also reach. A
// conversation belongs to at most one project, and removing a project
// removes no conversation.
type Project struct {
	id        string
	name      string
	folders   []string
	pinned    bool
	createdAt time.Time
	updatedAt time.Time
}

// NewProject builds a project from already-resolved absolute folders. An
// empty id mints one; an empty name takes the primary folder's base name.
func NewProject(id, name string, folders []string, now time.Time) (*Project, error) {
	vo, err := valueobject.NewID("p", id)
	if err != nil {
		return nil, err
	}
	p := &Project{id: vo.String(), createdAt: now, updatedAt: now}
	if err := p.SetFolders(folders); err != nil {
		return nil, err
	}
	p.Rename(name)
	return p, nil
}

// SetFolders replaces the folder list. Folders must be absolute; duplicates
// collapse onto their first position, and at least one must remain.
func (p *Project) SetFolders(folders []string) error {
	out := make([]string, 0, len(folders))
	for _, f := range folders {
		f = strings.TrimSpace(f)
		if f == "" || !filepath.IsAbs(f) {
			return errors.New("project folders must be absolute paths")
		}
		f = filepath.Clean(f)
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return errors.New("a project needs at least one folder")
	}
	p.folders = out
	return nil
}

// Rename sets the display name; an empty name falls back to the primary
// folder's base name.
func (p *Project) Rename(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(p.Primary())
	}
	p.name = name
}

// SetPinned keeps the project at the top of the list.
func (p *Project) SetPinned(pinned bool) { p.pinned = pinned }

// Touch records a save at now. A save based on a read whose updatedAt is
// not the stored one is refused with ErrProjectChanged by the repository.
func (p *Project) Touch(now time.Time) { p.updatedAt = now }

// Restore hydrates timestamps from storage.
func (p *Project) Restore(createdAt, updatedAt time.Time) {
	p.createdAt, p.updatedAt = createdAt, updatedAt
}

func (p Project) ID() string           { return p.id }
func (p Project) Name() string         { return p.name }
func (p Project) Folders() []string    { return slices.Clone(p.folders) }
func (p Project) Primary() string      { return p.folders[0] }
func (p Project) Pinned() bool         { return p.pinned }
func (p Project) CreatedAt() time.Time { return p.createdAt }
func (p Project) UpdatedAt() time.Time { return p.updatedAt }

// Extra lists the folders after the primary one.
func (p Project) Extra() []string { return slices.Clone(p.folders[1:]) }

// Handle is how a message names the project after "@": its name in lower
// case with each run of spaces as one hyphen. The web composer inserts the
// same token.
func (p Project) Handle() string {
	return strings.Join(strings.Fields(strings.ToLower(p.name)), "-")
}
