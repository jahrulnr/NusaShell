package repository

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// ApprovalRule stores rooms' "always allow" rules, one per room and
// action kind.
type ApprovalRule interface {
	// Create stores r; an existing rule for the same room and kind stays.
	Create(ctx context.Context, r *aggregate.ApprovalRule) error
	// Allows reports a rule for the room and kind.
	Allows(ctx context.Context, conversationID string, action enum.ToolAction) (bool, error)
	List(ctx context.Context, conversationID string) ([]aggregate.ApprovalRule, error)
	Delete(ctx context.Context, conversationID, id string) error
	DeleteByConversation(ctx context.Context, conversationID string) error
}
