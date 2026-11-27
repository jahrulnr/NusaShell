package skill

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// FolderSources are the places a workspace skill can be named. Either store
// may be nil. Fallback is the environment root a turn uses when a room binds
// no workspace of its own.
type FolderSources struct {
	Conversations interface {
		List(context.Context) ([]repository.ConversationInfo, error)
	}
	Projects interface {
		List(context.Context) ([]aggregate.Project, error)
	}
	Fallback string
}

// Folders implements SkillFolders. The folder a call is looking at is not
// included; ListAcross reads that one as its focus.
func (s FolderSources) Folders(ctx context.Context) ([]string, error) {
	return VisibleFolders(ctx, s, "")
}

// VisibleFolders is every folder that can hold a workspace skill: each
// conversation's own workspace, every project folder, the environment
// fallback, and also — the folder this call is looking at. A room in a
// project works in the project's folders; its own workspace binding is still
// included, because the folder can hold a skill either way. Duplicates drop.
func VisibleFolders(ctx context.Context, s FolderSources, also string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		out = append(out, dir)
	}
	if s.Conversations != nil {
		infos, err := s.Conversations.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, info := range infos {
			add(info.Workspace)
		}
	}
	if s.Projects != nil {
		projects, err := s.Projects.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range projects {
			for _, folder := range p.Folders() {
				add(folder)
			}
		}
	}
	add(s.Fallback)
	add(also)
	sort.Strings(out)
	return out, nil
}
