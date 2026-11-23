package appconversation_test

import (
	"context"
	"path/filepath"
	"testing"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	appproject "github.com/jahrulnr/nusashell/application/project"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoproject "github.com/jahrulnr/nusashell/infrastructure/repository/project"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	"github.com/jahrulnr/nusashell/migration"
)

func TestRoomsWorkInTheirProjectFolders(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	projects := repoproject.New(db)
	app, rt := appconversation.AgentFixtureForTest(t, appconversation.WithProjects(projects))
	pa := appproject.New(projects, repoworkspace.NewLocal(), app)

	primary, extra := t.TempDir(), t.TempDir()
	if _, err := pa.Create(ctx, "", []string{filepath.Join(primary, "missing")}, false); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("a missing folder must be refused, got %v", err)
	}
	p, err := pa.Create(ctx, "", []string{primary, extra, primary}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != filepath.Base(primary) || len(p.Folders) != 2 {
		t.Fatalf("project = %+v", p)
	}

	if _, err := app.Send(ctx, "c_proj1", "", "", "look", nil, appjob.SendOptions{Agent: "fake", Project: p.ID}, nil); err != nil {
		t.Fatal(err)
	}
	got := rt.Prompts()[0]
	if real(got.Workspace) != real(primary) || len(got.ExtraFolders) != 1 || real(got.ExtraFolders[0]) != real(extra) {
		t.Fatalf("prompt folders = %q + %q", got.Workspace, got.ExtraFolders)
	}
	info, _ := app.Info(ctx, "c_proj1")
	if info.ProjectID != p.ID {
		t.Fatalf("room project = %q", info.ProjectID)
	}

	// A stale edit is refused; a fresh one swaps the primary folder.
	if _, err := pa.Update(ctx, p.ID, "renamed", []string{extra}, true, p.UpdatedAt.Add(-1)); apperror.As(err).StatusCode() != 409 {
		t.Fatalf("stale edit err = %v, want 409", err)
	}
	p2, err := pa.Update(ctx, p.ID, "renamed", []string{extra}, true, p.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(ctx, "c_proj1", "", "", "again", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := rt.Prompts()[1]; real(got.Workspace) != real(extra) || len(got.ExtraFolders) != 0 {
		t.Fatalf("after edit folders = %q + %q", got.Workspace, got.ExtraFolders)
	}

	// Pin a room; delete the project — the room stays, outside any project.
	pinned := true
	if err := app.Annotate(ctx, "c_proj1", appconversation.Annotation{Pinned: &pinned}); err != nil {
		t.Fatal(err)
	}
	bogus := "p_nope"
	if err := app.Annotate(ctx, "c_proj1", appconversation.Annotation{ProjectID: &bogus}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("moving into an unknown project err = %v", err)
	}
	if _, err := app.Send(ctx, "c_proj2", "", "", "second", nil, appjob.SendOptions{Agent: "fake", Project: p2.ID}, nil); err != nil {
		t.Fatal(err)
	}
	released, err := pa.Delete(ctx, p2.ID)
	if err != nil || len(released) != 2 {
		t.Fatalf("released = %v, %v", released, err)
	}
	info, err = app.Info(ctx, "c_proj1")
	if err != nil || info.ProjectID != "" || !info.Pinned {
		t.Fatalf("after delete: %+v, %v", info, err)
	}

	// Undo: the project returns under its id with the rooms still outside
	// any project; one moved elsewhere since, and one never seen, stay out.
	other, err := pa.Create(ctx, "other", []string{primary}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Annotate(ctx, "c_proj2", appconversation.Annotation{ProjectID: &other.ID}); err != nil {
		t.Fatal(err)
	}
	back, rejoined, err := pa.Restore(ctx, p2.ID, p2.Name, p2.Folders, p2.Pinned, append(released, "c_gone"))
	if err != nil || back.ID != p2.ID || !back.Pinned || len(rejoined) != 1 || rejoined[0] != "c_proj1" {
		t.Fatalf("restore = %+v %v %v", back, rejoined, err)
	}
	if info, _ := app.Info(ctx, "c_proj1"); info.ProjectID != p2.ID {
		t.Fatalf("c_proj1 project = %q", info.ProjectID)
	}
	if info, _ := app.Info(ctx, "c_proj2"); info.ProjectID != other.ID {
		t.Fatalf("c_proj2 left its new project: %q", info.ProjectID)
	}
	if _, _, err := pa.Restore(ctx, p2.ID, p2.Name, p2.Folders, false, nil); apperror.As(err).StatusCode() != 409 {
		t.Fatalf("restoring a live project err = %v, want 409", err)
	}
}

func real(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return filepath.Clean(r)
}
