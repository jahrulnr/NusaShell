// Package appproject manages projects: named groups of conversations that
// work on the same folders. Folder paths are checked through the workspace
// port before they are stored, because every room in the project — and
// every agent it runs — will work in them.
package appproject

import (
	"context"
	"errors"
	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"slices"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Rooms is what this use case needs from the conversation broker: the room
// list, and moving a room out of a project through the room's own worker.
// *appconversation.ConversationApp satisfies it.
type Rooms interface {
	List(ctx context.Context) ([]drepo.ConversationInfo, error)
	Annotate(ctx context.Context, convID string, a appconversation.Annotation) error
}

// ProjectApp is the project management use case.
type ProjectApp struct {
	projects  drepo.Project
	workspace drepo.Workspace
	rooms     Rooms
	now       func() time.Time
	mu        sync.Mutex // serializes read-modify-write per process
	// deleted and restored hear of a project leaving and coming back, for
	// use cases that keep state per project (memory).
	deleted, restored []func(ctx context.Context, projectID string)
}

// OnDeleted adds fn to run after a project is deleted.
func (a *ProjectApp) OnDeleted(fn func(ctx context.Context, projectID string)) {
	a.deleted = append(a.deleted, fn)
}

// OnRestored adds fn to run after a deleted project is restored.
func (a *ProjectApp) OnRestored(fn func(ctx context.Context, projectID string)) {
	a.restored = append(a.restored, fn)
}

// New wires the use case.
func New(projects drepo.Project, workspace drepo.Workspace, rooms Rooms) *ProjectApp {
	return &ProjectApp{projects: projects, workspace: workspace, rooms: rooms, now: time.Now}
}

// View is the project projection.
type View struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Folders []string `json:"folders"`
	Pinned  bool     `json:"pinned"`
	// Threads counts the project's threads; List fills it.
	Threads   int       `json:"threads"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func view(p aggregate.Project) View {
	return View{ID: p.ID(), Name: p.Name(), Folders: p.Folders(), Pinned: p.Pinned(), CreatedAt: p.CreatedAt(), UpdatedAt: p.UpdatedAt()}
}

// List returns every project, pinned first, then by name.
func (a *ProjectApp) List(ctx context.Context) ([]View, error) {
	ps, err := a.projects.List(ctx)
	if err != nil {
		return nil, err
	}
	infos, err := a.rooms.List(ctx)
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for _, info := range infos {
		if info.ProjectID != "" {
			count[info.ProjectID]++
		}
	}
	out := make([]View, 0, len(ps))
	for _, p := range ps {
		v := view(p)
		v.Threads = count[p.ID()]
		out = append(out, v)
	}
	slices.SortStableFunc(out, func(x, y View) int {
		switch {
		case x.Pinned && !y.Pinned:
			return -1
		case !x.Pinned && y.Pinned:
			return 1
		}
		return 0
	})
	return out, nil
}

// Get returns one project.
func (a *ProjectApp) Get(ctx context.Context, id string) (View, error) {
	p, err := a.projects.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	return view(*p), nil
}

// resolve checks each folder is an existing directory and returns its
// canonical absolute path.
func (a *ProjectApp) resolve(folders []string) ([]string, error) {
	if len(folders) == 0 {
		return nil, apperror.New(400, "a project needs at least one folder", nil)
	}
	out := make([]string, 0, len(folders))
	for _, f := range folders {
		root, err := a.workspace.CheckRoot(f)
		if err != nil {
			return nil, apperror.New(400, "folder does not exist or is not a directory: "+f, err)
		}
		out = append(out, root)
	}
	return out, nil
}

// Create adds a project. An empty name takes the primary folder's name.
func (a *ProjectApp) Create(ctx context.Context, name string, folders []string, pinned bool) (View, error) {
	roots, err := a.resolve(folders)
	if err != nil {
		return View{}, err
	}
	p, err := aggregate.NewProject("", name, roots, a.now())
	if err != nil {
		return View{}, apperror.New(400, "invalid project", err)
	}
	p.SetPinned(pinned)
	if err := a.projects.Create(ctx, p); err != nil {
		return View{}, err
	}
	return view(*p), nil
}

// Update is a full edit of name, folders, and pin. readAt is the
// updatedAt the edit was based on; a project saved since is refused (409)
// so one tab cannot silently undo another's folder change.
func (a *ProjectApp) Update(ctx context.Context, id, name string, folders []string, pinned bool, readAt time.Time) (View, error) {
	roots, err := a.resolve(folders)
	if err != nil {
		return View{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.projects.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if err := p.SetFolders(roots); err != nil {
		return View{}, apperror.New(400, "invalid project", err)
	}
	p.Rename(name)
	p.SetPinned(pinned)
	p.Touch(a.now())
	if err := a.projects.Update(ctx, p, readAt); err != nil {
		return View{}, err
	}
	return view(*p), nil
}

// SetPinned pins or unpins without a full edit.
func (a *ProjectApp) SetPinned(ctx context.Context, id string, pinned bool) (View, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.projects.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	readAt := p.UpdatedAt()
	p.SetPinned(pinned)
	p.Touch(a.now())
	if err := a.projects.Update(ctx, p, readAt); err != nil {
		return View{}, err
	}
	return view(*p), nil
}

// Delete removes the project and takes its rooms out of it. The rooms and
// their history stay. It returns the ids of the rooms it released.
func (a *ProjectApp) Delete(ctx context.Context, id string) ([]string, error) {
	if err := a.projects.Delete(ctx, id); err != nil {
		return nil, err
	}
	for _, fn := range a.deleted {
		fn(ctx, id)
	}
	infos, err := a.rooms.List(ctx)
	if err != nil {
		return nil, err
	}
	none := ""
	var released []string
	var errs []error
	for _, info := range infos {
		if info.ProjectID != id {
			continue
		}
		err := a.rooms.Annotate(ctx, info.ID, appconversation.Annotation{ProjectID: &none, FromProjectID: &id})
		if apperror.As(err).StatusCode() == apperror.StatusConflict {
			continue // moved to another project meanwhile
		}
		if err != nil {
			// A room left pointing at the deleted project is released on
			// its next turn anyway; report it without stopping the rest.
			errs = append(errs, err)
			continue
		}
		released = append(released, info.ID)
	}
	return released, errors.Join(errs...)
}

// Restore undoes Delete: it recreates the project under its old id and
// brings back the rooms Delete released. A room rejoins only while it is
// still outside any project, so one moved elsewhere since stays there; a
// room deleted since is skipped. It returns the ids of the rooms that
// rejoined. A project with that id already existing is refused (409).
func (a *ProjectApp) Restore(ctx context.Context, id, name string, folders []string, pinned bool, roomIDs []string) (View, []string, error) {
	if id == "" {
		return View{}, nil, apperror.New(400, "a restored project needs its id", nil)
	}
	roots, err := a.resolve(folders)
	if err != nil {
		return View{}, nil, err
	}
	p, err := aggregate.NewProject(id, name, roots, a.now())
	if err != nil {
		return View{}, nil, apperror.New(400, "invalid project", err)
	}
	p.SetPinned(pinned)
	if err := a.projects.Create(ctx, p); err != nil {
		return View{}, nil, err
	}
	for _, fn := range a.restored {
		fn(ctx, id)
	}
	none := ""
	var rejoined []string
	var errs []error
	for _, roomID := range roomIDs {
		err := a.rooms.Annotate(ctx, roomID, appconversation.Annotation{ProjectID: &id, FromProjectID: &none})
		switch apperror.As(err).StatusCode() {
		case apperror.StatusConflict, apperror.StatusNotFound:
			continue // joined another project, or deleted, since
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rejoined = append(rejoined, roomID)
	}
	return view(*p), rejoined, errors.Join(errs...)
}
