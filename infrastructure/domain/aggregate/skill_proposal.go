package aggregate

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ErrSkillProposalDecided reports that the proposal already has its decision.
var ErrSkillProposalDecided = errors.New("this proposal was already decided")

// SkillProposal is a learned skill waiting for the owner: a new skill (create)
// or a rewrite of one the library holds (patch). A patch is read against the
// digest of the whole SKILL.md it rewrites, so accepting it never lands on a file
// that changed since.
type SkillProposal struct {
	id            string
	op            enum.SkillProposalOp
	kind          enum.SkillKind
	name          string
	description   string
	body          string
	reason        string
	evidence      string
	evidenceKind  enum.SkillEvidenceKind
	tool          string
	targetName    string
	targetDigest  string
	targetLevel   enum.SkillLevel
	status        enum.SkillProposalStatus
	acceptedPath  string
	acceptedLevel enum.SkillLevel
	dismissReason string
	createdAt     time.Time
	decidedAt     time.Time
}

// SkillProposalInput is what a skill proposal says it would do. TargetLevel is the
// level of the target as the caller read it; only an agent-level skill may be patched.
type SkillProposalInput struct {
	Op                enum.SkillProposalOp
	Kind              enum.SkillKind
	Tool              string
	Name, Description string
	Body, Evidence    string
	EvidenceKind      enum.SkillEvidenceKind
	Reason            string
	TargetName        string
	TargetDigest      string
	TargetLevel       enum.SkillLevel
}

// NewSkillProposal refuses a draft that breaks a skill rule, with every broken
// rule in the error. The op, kind, and evidence kind must parse first.
func NewSkillProposal(id string, in SkillProposalInput, now time.Time) (*SkillProposal, error) {
	vo, err := valueobject.NewID("sprop", id)
	if err != nil {
		return nil, err
	}
	op, ok := enum.ParseSkillProposalOp(string(in.Op))
	if !ok {
		return nil, fmt.Errorf("skill proposal op %q is not one of create, patch", in.Op)
	}
	kind, ok := enum.ParseSkillKind(string(in.Kind))
	if !ok {
		return nil, fmt.Errorf("skill kind %q is not one of tool, method", in.Kind)
	}
	evidenceKind, ok := enum.ParseSkillEvidenceKind(string(in.EvidenceKind))
	if !ok {
		return nil, fmt.Errorf("evidence kind %q is not one of quote, rewrite, repeat, call", in.EvidenceKind)
	}
	if now.IsZero() {
		return nil, errors.New("a proposal carries when it was created")
	}
	if vs := CheckSkillDraft(in); len(vs) > 0 {
		msgs := make([]string, len(vs))
		for i, v := range vs {
			msgs[i] = v.Message
		}
		return nil, errors.New(strings.Join(msgs, "; "))
	}
	return &SkillProposal{id: vo.String(), op: op, kind: kind, name: in.Name, description: in.Description, body: in.Body, evidence: in.Evidence, evidenceKind: evidenceKind, tool: in.Tool, reason: in.Reason, targetName: in.TargetName, targetDigest: in.TargetDigest, targetLevel: in.TargetLevel, status: enum.SkillProposalPending, createdAt: now}, nil
}

// SetStored restores a proposal from storage. Each status carries only its own
// decision: an accepted proposal keeps the path and level it was written to, a
// dismissed one keeps its reason, and a pending one keeps neither.
func (p *SkillProposal) SetStored(status enum.SkillProposalStatus, acceptedPath string, acceptedLevel enum.SkillLevel, dismissReason string, createdAt, decidedAt time.Time) error {
	acceptedPath, dismissReason = strings.TrimSpace(acceptedPath), strings.TrimSpace(dismissReason)
	if createdAt.IsZero() {
		return errors.New("a stored proposal carries when it was created")
	}
	switch status {
	case enum.SkillProposalPending:
		if acceptedPath != "" || acceptedLevel != "" || dismissReason != "" || !decidedAt.IsZero() {
			return errors.New("a pending proposal has no decision to restore")
		}
	case enum.SkillProposalAccepted:
		if acceptedPath == "" || decidedAt.IsZero() || dismissReason != "" {
			return errors.New("an accepted proposal names the path it was written to and when it was decided")
		}
		if _, ok := enum.ParseSkillLevel(string(acceptedLevel)); !ok {
			return fmt.Errorf("skill level %q is not one of user, agent", acceptedLevel)
		}
		// A stored state must be one Accept could produce — enforcement ahead
		// of advertisement: a patch is accepted only at agent level, so no
		// row hydrating one at the owner's level may come back to life.
		if p.op == enum.SkillProposalPatch && acceptedLevel != enum.SkillLevelAgent {
			return errors.New("an accepted patch is stored at agent level")
		}
	case enum.SkillProposalDismissed:
		if dismissReason == "" || decidedAt.IsZero() || acceptedPath != "" || acceptedLevel != "" {
			return errors.New("a dismissed proposal says why and when it was decided")
		}
	default:
		return fmt.Errorf("skill proposal status %q is not one of pending, accepted, dismissed", status)
	}
	p.status, p.acceptedPath, p.acceptedLevel, p.dismissReason = status, acceptedPath, acceptedLevel, dismissReason
	p.createdAt, p.decidedAt = createdAt, decidedAt
	return nil
}

// Accept records that the owner took the proposal, at the path it was written
// to and the level the owner chose. A patch keeps the skill's level, so it is
// accepted only at agent level; accepting it as the owner's would record a
// decision the write path cannot carry out.
func (p *SkillProposal) Accept(path string, level enum.SkillLevel, now time.Time) error {
	if p.status != enum.SkillProposalPending {
		return ErrSkillProposalDecided
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("an accepted skill is written to a path")
	}
	if _, ok := enum.ParseSkillLevel(string(level)); !ok {
		return fmt.Errorf("skill level %q is not one of user, agent", level)
	}
	if p.op == enum.SkillProposalPatch && level != enum.SkillLevelAgent {
		return errors.New("a patch keeps the skill's level, so it is accepted at agent level")
	}
	if now.IsZero() {
		return errors.New("a decision carries when it was made")
	}
	p.status, p.acceptedPath, p.acceptedLevel, p.decidedAt = enum.SkillProposalAccepted, path, level, now
	return nil
}

// SupersededReasonPrefix opens the reason the pass writes when it retires a
// pending proposal the owner can no longer accept. A retirement is not the
// owner's decision, so the cooldown does not hold a name for it; the store reads
// this prefix to tell the two apart. The owner's own reasons may not open with it.
const SupersededReasonPrefix = "superseded: "

// Dismiss records that the owner turned the proposal down, and why. The reason
// may not open with SupersededReasonPrefix, the prefix the pass writes itself.
func (p *SkillProposal) Dismiss(reason string, now time.Time) error {
	if p.status != enum.SkillProposalPending {
		return ErrSkillProposalDecided
	}
	if strings.HasPrefix(strings.TrimSpace(reason), SupersededReasonPrefix) {
		return errors.New(`a dismissal may not open with "superseded:", the reason the pass writes for the proposals it retires`)
	}
	return p.dismiss(reason, now)
}

// Supersede retires a pending proposal the pass has proved the owner can no
// longer accept: the state it was read against is no longer the state on disk,
// so accept would refuse it. The row stays, dismissed with the cause, so the
// owner can read what was proposed and why it fell away.
func (p *SkillProposal) Supersede(cause string, now time.Time) error {
	cause = strings.TrimSpace(cause)
	if cause == "" {
		return errors.New("a superseded proposal says what changed")
	}
	return p.dismiss(SupersededReasonPrefix+cause, now)
}

func (p *SkillProposal) dismiss(reason string, now time.Time) error {
	if p.status != enum.SkillProposalPending {
		return ErrSkillProposalDecided
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("a dismissal says why")
	}
	if now.IsZero() {
		return errors.New("a decision carries when it was made")
	}
	p.status, p.dismissReason, p.decidedAt = enum.SkillProposalDismissed, reason, now
	return nil
}

// Changes reports whether a patch's target, as the last observed ownership row
// records it, is no longer the file the patch was read against, or is no longer
// an agent-level skill the loop may rewrite. The row moves only when the owner's
// file is observed, so a change the row has not seen yet is not reported here.
// A create has no target and never changes.
func (p *SkillProposal) Changes(target *SkillOwnership) bool {
	if p.op != enum.SkillProposalPatch {
		return false
	}
	return target == nil || target.Root() != enum.SkillRootNusashell || target.Level() != enum.SkillLevelAgent || target.Name() != p.targetName || target.Digest() != p.targetDigest
}

func (p *SkillProposal) Tool() string { return p.tool }

func (p *SkillProposal) ID() string                           { return p.id }
func (p *SkillProposal) Op() enum.SkillProposalOp             { return p.op }
func (p *SkillProposal) Kind() enum.SkillKind                 { return p.kind }
func (p *SkillProposal) Name() string                         { return p.name }
func (p *SkillProposal) Description() string                  { return p.description }
func (p *SkillProposal) Body() string                         { return p.body }
func (p *SkillProposal) Reason() string                       { return p.reason }
func (p *SkillProposal) Evidence() string                     { return p.evidence }
func (p *SkillProposal) EvidenceKind() enum.SkillEvidenceKind { return p.evidenceKind }
func (p *SkillProposal) TargetName() string                   { return p.targetName }
func (p *SkillProposal) TargetDigest() string                 { return p.targetDigest }
func (p *SkillProposal) TargetLevel() enum.SkillLevel         { return p.targetLevel }
func (p *SkillProposal) Status() enum.SkillProposalStatus     { return p.status }
func (p *SkillProposal) AcceptedPath() string                 { return p.acceptedPath }
func (p *SkillProposal) AcceptedLevel() enum.SkillLevel       { return p.acceptedLevel }
func (p *SkillProposal) DismissReason() string                { return p.dismissReason }
func (p *SkillProposal) CreatedAt() time.Time                 { return p.createdAt }
func (p *SkillProposal) DecidedAt() time.Time                 { return p.decidedAt }
