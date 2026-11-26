package aggregate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// The ownership row's sentinel errors.
var (
	// ErrSkillMissing reports that the file is not on disk.
	ErrSkillMissing = errors.New("the skill's file is missing")
	// ErrSkillExists reports that a create would write a skill that already has a row.
	ErrSkillExists = errors.New("this skill already has an ownership row")
	// ErrSkillNoRow reports that a patch found no row to patch against.
	ErrSkillNoRow = errors.New("this skill has no ownership row")
	// ErrSkillChanged reports that the file is no longer the one the answer was given for.
	ErrSkillChanged = errors.New("the skill changed since this answer was given")
	// ErrSkillOwnedByOwner reports that the skill is the owner's, so the loop does not write it.
	ErrSkillOwnedByOwner = errors.New("this skill is the owner's")
)

// SkillOwnership records who one learned skill belongs to, keyed by its root and
// name. The digest is the whole SKILL.md the loop is authorized to rewrite — the
// bytes the last accept wrote, or the disk digest an owner re-grant saw. A row
// exists only for a learned skill, so the root is nusashell; keeping it in the
// key is what stops a same-named skill of another root answering for it.
type SkillOwnership struct {
	root   enum.SkillRoot
	name   string
	level  enum.SkillLevel
	digest string
}

func NewSkillOwnership(root enum.SkillRoot, name string, level enum.SkillLevel, digest string) (*SkillOwnership, error) {
	// Rows exist only for learned skills, so the root cannot be another one even
	// though the key carries it: a row of another root is not a state the model has.
	if root != enum.SkillRootNusashell {
		return nil, fmt.Errorf("an ownership row lives in the nusashell root, not %q", root)
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("an ownership row names its skill")
	}
	if _, ok := enum.ParseSkillLevel(string(level)); !ok {
		return nil, fmt.Errorf("skill level %q is not one of user, agent", level)
	}
	if strings.TrimSpace(digest) == "" {
		return nil, errors.New("an ownership row carries the digest it was written at")
	}
	return &SkillOwnership{root: root, name: name, level: level, digest: digest}, nil
}

// ApplySkillOwnership returns the ownership row an accept writes. current is the
// row as read before the write, nil when there is none; writtenDigest is the
// digest of the whole SKILL.md the accept wrote. The level is the one the accept
// recorded on the proposal: Accept already refuses an empty one, so no default
// is needed here. A create must not find a row; a patch must find the learned
// row still agent-level at the digest the proposal was read against, and keeps
// its level.
//
// The row's digest is the body the loop is authorized to rewrite — never the
// last digest observed on disk. Only two writers move it: an accept here, and an
// owner re-grant. Observe does not: it flips the level to user and leaves the
// number as history, because a patch that compared against the owner's
// hand-edited body could write over the owner's work. The changed check comes
// first for the same reason: a digest that no longer matches means the file is
// not the one this answer was given for, and that is the answer the owner needs.
func ApplySkillOwnership(current *SkillOwnership, p *SkillProposal, writtenDigest string) (*SkillOwnership, error) {
	if p.Status() != enum.SkillProposalAccepted {
		return nil, errors.New("an ownership row is written only for an accepted proposal")
	}
	if strings.TrimSpace(writtenDigest) == "" {
		return nil, errors.New("an ownership row carries the digest that was written")
	}
	name := p.Name()
	if p.Op() == enum.SkillProposalPatch {
		name = p.TargetName()
	}
	switch p.Op() {
	case enum.SkillProposalCreate:
		if current != nil {
			return nil, ErrSkillExists
		}
		return NewSkillOwnership(enum.SkillRootNusashell, name, p.AcceptedLevel(), writtenDigest)
	case enum.SkillProposalPatch:
		if current == nil || current.Root() != enum.SkillRootNusashell || current.Name() != name {
			return nil, ErrSkillNoRow
		}
		if current.Digest() != p.TargetDigest() {
			return nil, ErrSkillChanged
		}
		if current.Level() != enum.SkillLevelAgent {
			return nil, ErrSkillOwnedByOwner
		}
		if p.AcceptedLevel() != enum.SkillLevelAgent {
			return nil, errors.New("a patch keeps the skill's level, and only an agent-level skill may be patched")
		}
		return NewSkillOwnership(enum.SkillRootNusashell, name, current.Level(), writtenDigest)
	}
	return nil, fmt.Errorf("skill proposal op %q is not one of create, patch", p.Op())
}

// Observe records that the file on disk changed outside the loop's own writes:
// its digest no longer matches the row, so the skill is the owner's now. An
// empty disk digest means the file is missing, and that is a different fact the
// caller handles, so it is refused and the row does not move.
func (o *SkillOwnership) Observe(diskDigest string) error {
	if strings.TrimSpace(diskDigest) == "" {
		return ErrSkillMissing
	}
	if diskDigest != o.digest {
		o.level = enum.SkillLevelUser
	}
	return nil
}

// SkillStanding is who may change one learned skill now, read off its
// ownership row and the file on disk. The Skills page and the candidate pass
// both read it from SkillStandingOf, so neither can answer differently.
type SkillStanding struct {
	// Level is the effective level: user unless the row speaks for these bytes.
	Level enum.SkillLevel
	// Tracked reports that an ownership row exists for the skill.
	Tracked bool
	// Changed reports that the file on disk is not the one the row was written
	// for: the owner edited it outside the loop, so the row's level is void.
	Changed bool
}

// SkillStandingOf is the one rule for who may change a learned skill now. A
// skill with no row is the owner's. So is one whose disk digest no longer
// matches the row's digest: the row's level describes bytes that are gone. The
// row's level stands only while the digests agree. row is nil when no row
// exists; diskDigest is the digest of the whole SKILL.md on disk. Reading it
// writes nothing, and the reversal is persisted only by the next move.
func SkillStandingOf(row *SkillOwnership, diskDigest string) SkillStanding {
	if row == nil {
		return SkillStanding{Level: enum.SkillLevelUser}
	}
	if diskDigest != row.digest {
		return SkillStanding{Level: enum.SkillLevelUser, Tracked: true, Changed: true}
	}
	return SkillStanding{Level: row.level, Tracked: true}
}

// MoveLevel records the owner's decision about who the skill answers to,
// verified against the exact bytes the owner saw: the caller has already
// compared diskDigest with the page's digest in both directions — taking a
// skill back and handing it to the loop both refuse a file that moved since —
// so an empty one is refused here as a missing file. A move to agent re-grants
// the loop's authority over those bytes: the digest moves to them, because
// they are what the owner just read and approved. A move to user revokes it:
// the digest stays as history, same as an observed hand edit — a later
// re-grant decides again at whatever the owner sees then.
func (o *SkillOwnership) MoveLevel(level enum.SkillLevel, diskDigest string) error {
	if _, ok := enum.ParseSkillLevel(string(level)); !ok {
		return fmt.Errorf("skill level %q is not one of user, agent", level)
	}
	if strings.TrimSpace(diskDigest) == "" {
		return ErrSkillMissing
	}
	o.level = level
	if level == enum.SkillLevelAgent {
		o.digest = diskDigest
	}
	return nil
}

func (o *SkillOwnership) Root() enum.SkillRoot   { return o.root }
func (o *SkillOwnership) Name() string           { return o.name }
func (o *SkillOwnership) Level() enum.SkillLevel { return o.level }
func (o *SkillOwnership) Digest() string         { return o.digest }
