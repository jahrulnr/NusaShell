package skill

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// DecideDeps are the ports one owner decision needs. Budget is the catalog's
// byte budget, the same one validation enforces; the catalog may have grown
// since the draft was admitted, so it is checked again here.
type DecideDeps struct {
	Proposals repository.SkillProposals
	Decisions repository.SkillDecisions
	Ownership repository.SkillOwnership
	Catalog   repository.Skills
	Writer    repository.SkillWriter
	Log       repository.Logger
	Budget    int
	// Folders lists the other workspace folders a create's name must be free
	// in. Nil means the decision's workspace is the only one.
	Folders SkillFolders
}

// DecideInput is one owner decision on a proposal. Level is the owner's choice
// at accept and empty means agent, the default the use case applies — the
// domain still receives it explicitly. Reason is the owner's why at dismiss.
// Workspace selects the catalog the decision is read against.
type DecideInput struct {
	ID        string
	Action    string
	Level     string
	Reason    string
	Workspace string
	Now       time.Time
}

// Decide carries out one owner decision: accept writes the skill and records
// the ownership row and the decision in one transaction, dismiss records only
// the decision. A proposal that no longer matches the world — its target gone
// or changed, its name taken, the catalog full — stays pending with a 409 that
// names the state it found; the owner can dismiss it. The write happens before
// the commit and cannot be undone by it: when the commit fails the file stays,
// the failure is logged, and the 500 says the decision was not recorded.
func Decide(ctx context.Context, deps DecideDeps, in DecideInput) error {
	p, err := deps.Proposals.Get(ctx, in.ID)
	if err != nil {
		return err
	}
	if p.Status() != enum.SkillProposalPending {
		return apperror.New(409, "this proposal was already decided", nil)
	}
	switch in.Action {
	case "dismiss":
		// The row keeps whether the proposal was still open, so closing one that
		// had gone stale holds no name and no refusal. A create's own leftover is
		// archived before the decision is recorded: a failed archive leaves the
		// proposal pending, so the owner can try again. A patch's leftover stays.
		st, err := learnedNow(ctx, deps.Catalog, deps.Folders, deps.Ownership, deps.Writer, in.Workspace, []*aggregate.SkillProposal{p})
		if err != nil {
			return err
		}
		if err := p.Dismiss(in.Reason, in.Now, st.open(p)); err != nil {
			return apperror.New(400, err.Error(), nil)
		}
		release, err := releaseOwnFile(ctx, deps.Writer, st, p)
		if err != nil {
			return err
		}
		if err := deps.Decisions.Commit(context.WithoutCancel(ctx), p, nil); err != nil {
			decideLog(ctx, deps, "skill-decide-dismiss-commit-failed", p, err)
			return apperror.New(500, dismissCommitFailedText(p, release), err)
		}
		return nil
	case "accept":
	default:
		return apperror.New(400, `action must be "accept" or "dismiss"`, nil)
	}

	level := enum.SkillLevelAgent
	if strings.TrimSpace(in.Level) != "" {
		parsed, ok := enum.ParseSkillLevel(in.Level)
		if !ok {
			return apperror.New(400, "level must be user or agent", nil)
		}
		level = parsed
	}
	// Input validation ahead of the irreversible write: the domain re-decides
	// this in Accept, but by then the file would already be on disk.
	if p.Op() == enum.SkillProposalPatch && level != enum.SkillLevelAgent {
		return apperror.New(400, "a patch keeps the skill's level, so it is accepted at agent level", nil)
	}
	if in.Now.IsZero() {
		return apperror.New(400, "a decision carries when it was made", nil)
	}

	catalog, taken, err := catalogView(ctx, deps.Catalog, deps.Folders, in.Workspace)
	if err != nil {
		return err
	}
	targetName := p.TargetName()
	rowName := p.Name()
	if p.Op() == enum.SkillProposalPatch {
		rowName = targetName
	}
	var row *aggregate.SkillOwnership
	row, err = deps.Ownership.Get(ctx, enum.SkillRootNusashell, rowName)
	if err != nil {
		if apperror.As(err).StatusCode() != 404 {
			return err
		}
		row = nil
	}
	// A patch is read against a live learned skill whose file and row still
	// describe what the patch was read against. A refusal names the state the
	// queue shows for it, in the same words, so the owner sees one account.
	var rows []*aggregate.SkillOwnership
	if row != nil {
		rows = append(rows, row)
	}
	st, err := newLearnedState(catalog.Skills, rows, taken).withOwnDocs(deps.Writer, []*aggregate.SkillProposal{p})
	if err != nil {
		return err
	}
	if p.Op() == enum.SkillProposalPatch {
		// The same rule the queue reads: the file must still be the one the
		// patch read, and the row must still describe it. A file that differs is
		// refused here even when its bytes are the patch's own body, so a failed
		// commit's leftover is not accepted a second time.
		if !st.open(p) {
			return apperror.New(409, st.supersedeCause(p)+"; this proposal no longer applies", nil)
		}
	} else {
		// A create needs its name free in every root the owner can see, not only in
		// this workspace: the fixed roots and every other workspace folder. The
		// validator refuses a name live in any root at draft time, and any of them
		// may take the name afterwards. The one file that does not count is the
		// loop's own leftover, the nusashell file an accept whose commit failed
		// left behind: the write finds those exact bytes and the commit finishes.
		if root, held := st.elsewhere[p.Name()]; held {
			return apperror.New(409, fmt.Sprintf("the name %q is already live in the %s root; this proposal can only be dismissed", p.Name(), root), nil)
		}
		if _, onDisk := st.onDisk[p.Name()]; onDisk && !st.own[p.ID()] {
			return apperror.New(409, fmt.Sprintf("the name %q is already live in the %s root; this proposal can only be dismissed", p.Name(), string(enum.SkillRootNusashell)), nil)
		}
		if row != nil {
			return apperror.New(409, "this skill already has an ownership row", nil)
		}
	}

	// The catalog byte accounting is validation's, re-checked because the
	// catalog may have grown since: every live name and description, plus the
	// incoming skill's. A patch's target leaves with its old bytes, and so does
	// the loop's own leftover: the write replaces that file, so its bytes are
	// counted once, as the incoming skill's.
	total := len(p.Name()) + len(p.Description())
	for _, s := range catalog.Skills {
		if s.Source == string(enum.SkillRootNusashell) && s.Name == rowName && (p.Op() == enum.SkillProposalPatch || st.own[p.ID()]) {
			continue
		}
		total += len(s.Name) + len(s.Description)
	}
	switch {
	case deps.Budget <= 0:
		return apperror.New(409, "no catalog budget is set; the owner sets it", nil)
	case total > deps.Budget:
		return apperror.New(409, fmt.Sprintf("the catalog would be over its byte budget by %s bytes; make room before accepting", withCommas(total-deps.Budget)), nil)
	}

	expectDigest := ""
	if p.Op() == enum.SkillProposalPatch {
		expectDigest = p.TargetDigest()
	}
	doc := repository.SkillDocument{Name: p.Name(), Description: p.Description(), Body: p.Body()}
	written, err := deps.Writer.Write(ctx, enum.SkillRootNusashell, doc, expectDigest)
	if err != nil {
		// The writer's own refusal is the owner's answer: it names what moved
		// — the name taken on disk for a create, the file changed or gone
		// since a patch was read — and each case asks the owner for a
		// different next step. A blanket message here would blur them.
		return err
	}

	if err := p.Accept(written.Path, level, in.Now); err != nil {
		return apperror.New(409, err.Error(), nil)
	}
	newRow, err := aggregate.ApplySkillOwnership(row, p, written.Digest)
	if err != nil {
		// The file is already written, so this is a partial write, not a
		// refusal. Nothing flips the row by itself; only an owner move does.
		decideLog(ctx, deps, "skill-decide-row-refused", p, err)
		return apperror.New(500, commitFailedText(p), err)
	}
	if err := deps.Decisions.Commit(context.WithoutCancel(ctx), p, newRow); err != nil {
		decideLog(ctx, deps, "skill-decide-commit-failed", p, err)
		return apperror.New(500, commitFailedText(p), err)
	}
	return nil
}

// commitFailedText says what a failed commit left and the owner's next step. A
// create's leftover is the loop's own, so accepting again finishes it. A patch's
// new bytes sit under the old row, which no longer describes them: the proposal
// cannot be accepted again, so the owner dismisses it and sets the skill's level
// on the Skills page.
func commitFailedText(p *aggregate.SkillProposal) string {
	if p.Op() == enum.SkillProposalPatch {
		return "the loop wrote the skill file, but the decision was not recorded; this proposal no longer applies to that file, so dismiss it and set the skill's level on the Skills page"
	}
	return "the skill file is written but the decision was not recorded; accepting the proposal again finishes it"
}

// dismissCommitFailedText says exactly what happened to the file before the
// failed commit. The archive is conditional: only bytes still exactly as the
// loop wrote them move; owner-edited bytes stay where they are.
func dismissCommitFailedText(p *aggregate.SkillProposal, release ownFileRelease) string {
	const retry = "the decision was not recorded; trying the dismissal again is safe"
	switch release {
	case ownFileArchived:
		return "the skill file was archived because it was still exactly as the loop wrote it, but " + retry
	case ownFileAlreadyGone:
		return "the skill file was already gone, so nothing was archived; " + retry
	}
	if p.Op() == enum.SkillProposalPatch {
		return "the skill file was left in place because a patch did not create it; " + retry
	}
	return "the skill file was left in place because it was not still exactly as the loop wrote it; " + retry
}

// ownFileRelease is what closing a proposal did to the file on disk.
type ownFileRelease int

const (
	ownFileLeft ownFileRelease = iota
	ownFileArchived
	ownFileAlreadyGone
)

// releaseOwnFile archives the loop's own leftover of a create before the create
// is closed. An accept whose commit failed leaves the file with no ownership row,
// and once the proposal is closed nothing would ever remove it: a live nusashell
// skill with no row is worse than an unaccepted proposal. A patch's leftover is
// not archived: its row still describes the file the patch read, and the owner
// settles its level on the Skills page. The archive is guarded by the digest the
// decision read, so a file edited since is refused, not moved. A file already
// gone is what closing wanted, so it is not an error.
func releaseOwnFile(ctx context.Context, writer repository.SkillWriter, st learnedState, p *aggregate.SkillProposal) (ownFileRelease, error) {
	if p.Op() != enum.SkillProposalCreate {
		return ownFileLeft, nil
	}
	if !st.own[p.ID()] {
		if _, present := st.onDisk[p.Name()]; !present {
			return ownFileAlreadyGone, nil
		}
		return ownFileLeft, nil
	}
	err := writer.Archive(ctx, enum.SkillRootNusashell, p.Name(), st.onDisk[p.Name()])
	if err != nil && apperror.As(err).StatusCode() == 404 {
		return ownFileAlreadyGone, nil
	}
	if err != nil {
		return ownFileLeft, err
	}
	return ownFileArchived, nil
}

// withCommas writes a byte count with thousands separators, the way the owner
// reads it in a sentence.
func withCommas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// decideLog records the decision failures that leave the file and the database
// inconsistent — the owner chose logging over rollback, because a rollback can
// overwrite a concurrent edit while the flip-to-user rule heals the rest.
func decideLog(ctx context.Context, deps DecideDeps, kind string, p *aggregate.SkillProposal, cause error) {
	if deps.Log == nil {
		return
	}
	_ = deps.Log.Append(ctx, kind, map[string]string{
		"proposal": p.ID(),
		"name":     p.Name(),
		"error":    cause.Error(),
	})
}
