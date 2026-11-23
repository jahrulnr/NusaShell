package job

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// applyProject points a room that belongs to a project at the project's
// folders: the primary folder becomes the room's workspace, and the extra
// folders come back for runtimes that can work in several. A project that
// no longer exists releases the room instead of failing its turn.
func (d Deps) applyProject(ctx context.Context, conv *aggregate.Conversation) (extra []string, err error) {
	if conv.ProjectID() == "" || d.Projects == nil {
		return nil, nil
	}
	p, err := d.Projects.Get(ctx, conv.ProjectID())
	if apperror.As(err).StatusCode() == apperror.StatusNotFound {
		_ = conv.SetProject("")
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	conv.SetWorkspace(p.Primary())
	return p.Extra(), nil
}
