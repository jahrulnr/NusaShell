package aggregate

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// MemoryEvidenceMax bounds the owner's words a suggestion quotes.
const MemoryEvidenceMax = 500

// MemoryProposal is a memory change read from a thread's sealed history
// and waiting for the owner: add a memory, rewrite one, or mark one stale.
// It quotes the owner's own words as evidence, since a memory about the
// owner comes only from what they said. An update or stale names its
// target by id and by the time the target last changed, so accepting it
// never lands on a memory that moved on since.
type MemoryProposal struct {
	id              string
	conversationID  string
	messageID       string // the owner's message the evidence is from
	op              enum.MemoryProposalOp
	scope           valueobject.MemoryScope
	mtype           enum.MemoryType
	name            string
	description     string
	body            string
	reason          string // why a memory is stale
	evidence        string
	targetID        string
	targetUpdatedAt time.Time
	similar         []string // live memories an add overlaps, by id
	status          enum.MemoryProposalStatus
	acceptedID      string // the memory accepting it wrote
	createdAt       time.Time
	decidedAt       time.Time
}

// MemoryProposalInput is what a suggestion says.
type MemoryProposalInput struct {
	ConversationID, MessageID string
	Op                        enum.MemoryProposalOp
	Scope                     valueobject.MemoryScope
	Type                      enum.MemoryType
	Name                      string
	Description, Body         string
	Reason                    string
	Evidence                  string
	TargetID                  string
	TargetUpdatedAt           time.Time
}

// NewMemoryProposal builds a pending suggestion. What it would write obeys
// the memory's own rules: an add and an update carry a type, a
// description, and a body; a stale carries a reason; an update and a
// stale name their target.
func NewMemoryProposal(id string, in MemoryProposalInput, now time.Time) (*MemoryProposal, error) {
	vo, err := valueobject.NewID("mprop", id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.ConversationID) == "" {
		return nil, errors.New("a memory suggestion needs the thread it came from")
	}
	if in.Scope.IsZero() {
		return nil, errors.New("memory scope is required")
	}
	name, err := memoryNameOf(in.Name)
	if err != nil {
		return nil, err
	}
	evidence := strings.Join(strings.Fields(in.Evidence), " ")
	switch {
	case evidence == "":
		return nil, errors.New("a memory suggestion quotes the owner's words")
	case utf8.RuneCountInString(evidence) > MemoryEvidenceMax:
		return nil, fmt.Errorf("the quoted words are at most %d characters", MemoryEvidenceMax)
	}
	p := &MemoryProposal{
		id: vo.String(), conversationID: in.ConversationID, messageID: in.MessageID, op: in.Op, scope: in.Scope,
		mtype: in.Type, name: name, evidence: evidence, status: enum.MemoryProposalPending, createdAt: now,
	}
	switch in.Op {
	case enum.MemoryProposalAdd, enum.MemoryProposalUpdate:
		if _, ok := enum.ParseMemoryType(string(in.Type)); !ok {
			return nil, fmt.Errorf("memory type %q is not one of user, feedback, project, reference", in.Type)
		}
		if p.description, p.body, err = memoryText(in.Description, in.Body); err != nil {
			return nil, err
		}
	case enum.MemoryProposalStale:
		if p.reason, err = memoryReason(in.Reason); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("memory suggestion op %q is not one of add, update, stale", in.Op)
	}
	if (in.Op == enum.MemoryProposalAdd) != (in.TargetID == "") {
		return nil, errors.New("an add names no memory; an update or stale names the memory it changes")
	}
	p.targetID, p.targetUpdatedAt = in.TargetID, in.TargetUpdatedAt
	return p, nil
}

// Accept records that the owner took the suggestion, and the memory it
// wrote.
func (p *MemoryProposal) Accept(memoryID string, now time.Time) error {
	if p.status != enum.MemoryProposalPending {
		return errors.New("this suggestion was already decided")
	}
	p.status, p.acceptedID, p.decidedAt = enum.MemoryProposalAccepted, memoryID, now
	return nil
}

// Dismiss records that the owner, or the end of what it was about,
// turned the suggestion down.
func (p *MemoryProposal) Dismiss(now time.Time) error {
	if p.status != enum.MemoryProposalPending {
		return errors.New("this suggestion was already decided")
	}
	p.status, p.decidedAt = enum.MemoryProposalDismissed, now
	return nil
}

// Changes reports whether the target, as it stands now, is no longer what
// the suggestion was read against: forgotten, or changed since.
func (p *MemoryProposal) Changes(target *Memory) bool {
	return target == nil || !target.Live() || !target.UpdatedAt().Equal(p.targetUpdatedAt)
}

// SetSimilar lists the live memories an add overlaps.
func (p *MemoryProposal) SetSimilar(ids []string) { p.similar = ids }

// SetStored hydrates the persisted decision — the repository's path back
// from storage, after NewMemoryProposal rebuilt the content.
func (p *MemoryProposal) SetStored(status enum.MemoryProposalStatus, acceptedID string, createdAt, decidedAt time.Time) {
	p.status, p.acceptedID, p.createdAt, p.decidedAt = status, acceptedID, createdAt, decidedAt
}

func (p *MemoryProposal) ID() string                        { return p.id }
func (p *MemoryProposal) ConversationID() string            { return p.conversationID }
func (p *MemoryProposal) MessageID() string                 { return p.messageID }
func (p *MemoryProposal) Op() enum.MemoryProposalOp         { return p.op }
func (p *MemoryProposal) Scope() valueobject.MemoryScope    { return p.scope }
func (p *MemoryProposal) Type() enum.MemoryType             { return p.mtype }
func (p *MemoryProposal) Name() string                      { return p.name }
func (p *MemoryProposal) Description() string               { return p.description }
func (p *MemoryProposal) Body() string                      { return p.body }
func (p *MemoryProposal) Reason() string                    { return p.reason }
func (p *MemoryProposal) Evidence() string                  { return p.evidence }
func (p *MemoryProposal) TargetID() string                  { return p.targetID }
func (p *MemoryProposal) TargetUpdatedAt() time.Time        { return p.targetUpdatedAt }
func (p *MemoryProposal) Similar() []string                 { return p.similar }
func (p *MemoryProposal) Status() enum.MemoryProposalStatus { return p.status }
func (p *MemoryProposal) AcceptedID() string                { return p.acceptedID }
func (p *MemoryProposal) CreatedAt() time.Time              { return p.createdAt }
func (p *MemoryProposal) DecidedAt() time.Time              { return p.decidedAt }
