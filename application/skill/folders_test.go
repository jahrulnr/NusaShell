package skill

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

type folderConvs struct {
	infos []repository.ConversationInfo
	err   error
}

func (f folderConvs) List(context.Context) ([]repository.ConversationInfo, error) {
	return f.infos, f.err
}

type folderProjects struct {
	projects []aggregate.Project
	err      error
}

func (f folderProjects) List(context.Context) ([]aggregate.Project, error) {
	return f.projects, f.err
}

func TestVisibleFoldersUnionsEveryPlaceASkillCanLive(t *testing.T) {
	project, err := aggregate.NewProject("p_one", "demo", []string{"/work/primary", "/work/extra"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	got, err := VisibleFolders(context.Background(), FolderSources{
		Conversations: folderConvs{infos: []repository.ConversationInfo{
			{Workspace: "/rooms/one"},
			{Workspace: ""},
			{Workspace: "/work/primary/"},
		}},
		Projects: folderProjects{projects: []aggregate.Project{*project}},
		Fallback: "/home/owner",
	}, "/asked")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/asked", "/home/owner", "/rooms/one", "/work/extra", "/work/primary"}
	if len(got) != len(want) {
		t.Fatalf("folders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("folders = %v, want %v", got, want)
		}
	}
}

func TestVisibleFoldersFailsClosedWhenAStoreFails(t *testing.T) {
	_, err := VisibleFolders(context.Background(), FolderSources{
		Conversations: folderConvs{err: errors.New("store down")},
	}, "/asked")
	if err == nil {
		t.Fatal("a failed conversation list still produced folders")
	}
}
