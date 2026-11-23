package aggregate

import (
	"fmt"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ApprovalRule remembers the owner's "always allow" for one kind of action
// in one room: the room's agent asks no more for that kind.
type ApprovalRule struct {
	id             string
	conversationID string
	agentID        string
	action         enum.ToolAction
	createdAt      time.Time
}

// NewApprovalRule builds a rule; an empty id mints one.
func NewApprovalRule(id, conversationID, agentID string, action enum.ToolAction, now time.Time) (*ApprovalRule, error) {
	vo, err := valueobject.NewID("rule", id)
	if err != nil {
		return nil, err
	}
	if err := valueobject.ValidID(conversationID); err != nil {
		return nil, fmt.Errorf("conversation id: %w", err)
	}
	if action == "" {
		return nil, fmt.Errorf("an approval rule needs an action kind")
	}
	return &ApprovalRule{id: vo.String(), conversationID: conversationID, agentID: agentID, action: action, createdAt: now}, nil
}

func (r ApprovalRule) ID() string              { return r.id }
func (r ApprovalRule) ConversationID() string  { return r.conversationID }
func (r ApprovalRule) AgentID() string         { return r.agentID }
func (r ApprovalRule) Action() enum.ToolAction { return r.action }
func (r ApprovalRule) CreatedAt() time.Time    { return r.createdAt }
