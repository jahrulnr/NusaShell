package skill

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// SkillFolders lists every folder that can hold a workspace skill besides the
// one the caller is looking at. That folder is passed separately and is always
// read, so a page opened on a folder no conversation uses still sees it. Nil
// means the caller is looking at the only folder.
type SkillFolders interface {
	Folders(ctx context.Context) ([]string, error)
}

// catalogView is the one read behind the page label and accept. The catalog
// is focus's effective view. Taken also names a skill that lives only in
// another workspace folder, which does not change the rows focus shows.
func catalogView(ctx context.Context, catalog repository.Skills, folders SkillFolders, focus string) (repository.SkillCatalog, map[string]string, error) {
	var others []string
	if folders != nil {
		var err error
		others, err = folders.Folders(ctx)
		if err != nil {
			return repository.SkillCatalog{}, nil, err
		}
	}
	cat, taken, err := catalog.ListAcross(ctx, focus, others)
	if err != nil {
		return repository.SkillCatalog{}, nil, err
	}
	if taken == nil {
		taken = map[string]string{}
	}
	for _, s := range cat.Skills {
		if _, ok := taken[s.Name]; !ok {
			taken[s.Name] = s.Source
		}
	}
	return cat, taken, nil
}
