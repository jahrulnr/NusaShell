package repoconversation

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// Info projects meta.json and checks the archive filenames, without parsing messages.
func (r *ConversationRepository) Info(ctx context.Context, convID string) (drepo.ConversationInfo, error) {
	if err := validID(convID); err != nil {
		return drepo.ConversationInfo{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return drepo.ConversationInfo{}, err
	}
	defer unlock()
	meta, err := r.readMeta(convID)
	if err != nil {
		return drepo.ConversationInfo{}, err
	}
	return drepo.ConversationInfo{
		ID:             meta.ID,
		Title:          meta.Title,
		ProviderID:     meta.ProviderID,
		ModelID:        meta.ModelID,
		Effort:         meta.Effort,
		Upstream:       meta.Upstream,
		Workspace:      meta.Workspace,
		Environment:    enum.Environment(meta.Environment),
		AgentID:        meta.AgentID,
		ProjectID:      meta.ProjectID,
		Pinned:         meta.Pinned,
		MemoryOff:      meta.MemoryOff,
		Label:          meta.Label,
		AgentOptions:   meta.AgentOptions,
		AgentSession:   meta.AgentSession,
		Routing:        decodeRouting(meta.Routing),
		Usage:          mustUsage(meta.Usage),
		MessageCount:   meta.MessageCount,
		ActiveCount:    meta.ActiveCount,
		SealedSegments: meta.SealedSegments,
		CreatedAt:      meta.CreatedAt,
		UpdatedAt:      meta.UpdatedAt,
	}, nil
}
