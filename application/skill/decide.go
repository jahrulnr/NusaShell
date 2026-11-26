package skill

import (
	"context"
	"fmt"
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
// or changed, its name taken, the catalog full — stays pending with a 409, so
// the owner re-reads before deciding again. The write happens before the
// commit and cannot be undone by it: when the commit fails the file stays, the
// failure is logged, and a second accept finishes the row because writing the
// same bytes is a successful no-op.
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
		if err := p.Dismiss(in.Reason, in.Now); err != nil {
			return apperror.New(400, err.Error(), nil)
		}
		if err := deps.Decisions.Commit(ctx, p, nil); err != nil {
			return err
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

	catalog, err := deps.Catalog.List(ctx, in.Workspace)
	if err != nil {
		return err
	}
	// A patch is read against a live learned skill; a name that is not there any
	// more, or not there yet, means the target moved since validation.
	targetName := p.TargetName()
	if p.Op() == enum.SkillProposalPatch {
		live := false
		for _, s := range catalog.Skills {
			if s.Source == string(enum.SkillRootNusashell) && s.Name == targetName {
				live = true
			}
		}
		if !live {
			return apperror.New(409, "the patch's target is no longer a live learned skill — re-read it before deciding", nil)
		}
	}

	// A create needs its name free in every root, not only in nusashell: the
	// validator refuses a name live in any root at draft time, and any root may
	// take the name afterwards.
	if p.Op() == enum.SkillProposalCreate {
		for _, s := range catalog.Skills {
			if s.Name == p.Name() {
				return apperror.New(409, fmt.Sprintf("the name %q is already live in the %s root; this proposal can only be dismissed", p.Name(), s.Source), nil)
			}
		}
	}

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
	if p.Op() == enum.SkillProposalPatch {
		if p.Changes(row) {
			return apperror.New(409, "the skill changed since this proposal was read — re-read it before deciding", nil)
		}
	} else if row != nil {
		return apperror.New(409, "this skill already has an ownership row", nil)
	}

	// The catalog byte accounting is validation's, re-checked because the
	// catalog may have grown since: every live name and description, plus the
	// incoming skill's; a patch's target leaves with its old bytes.
	total := len(p.Name()) + len(p.Description())
	for _, s := range catalog.Skills {
		if p.Op() == enum.SkillProposalPatch && s.Source == string(enum.SkillRootNusashell) && s.Name == targetName {
			continue
		}
		total += len(s.Name) + len(s.Description)
	}
	switch {
	case deps.Budget <= 0:
		return apperror.New(409, "no catalog budget is set; the owner sets it", nil)
	case total > deps.Budget:
		return apperror.New(409, "the catalog is over its byte budget; make room before accepting", nil)
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
		// The file is already written; the observation pass will flip the row
		// to the owner, and nothing here may pretend the write did not happen.
		decideLog(ctx, deps, "skill-decide-row-refused", p, err)
		return apperror.New(409, err.Error(), nil)
	}
	if err := deps.Decisions.Commit(ctx, p, newRow); err != nil {
		decideLog(ctx, deps, "skill-decide-commit-failed", p, err)
		return apperror.New(500, "the skill file is written but the decision was not recorded; accepting the proposal again finishes it", err)
	}
	return nil
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
