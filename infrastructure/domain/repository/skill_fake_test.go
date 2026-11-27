package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/error"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

func skillKey(root enum.SkillRoot, name string) string { return string(root) + "/" + name }

// skillDB is the state the fake port views share, so a refused Commit leaves
// neither of its writes behind, the way one database transaction would.
type skillDB struct {
	proposals map[string]*aggregate.SkillProposal
	rows      map[string]*aggregate.SkillOwnership
	files     map[string]string
	archived  map[string]string
}

type skillProposalStore struct{ db *skillDB }
type skillOwnershipStore struct{ db *skillDB }
type skillDecisionStore struct{ db *skillDB }
type skillWriter struct{ db *skillDB }

func newSkillFakes() (*skillProposalStore, *skillOwnershipStore, *skillDecisionStore, *skillWriter) {
	db := &skillDB{
		proposals: map[string]*aggregate.SkillProposal{},
		rows:      map[string]*aggregate.SkillOwnership{},
		files:     map[string]string{},
		archived:  map[string]string{},
	}
	return &skillProposalStore{db}, &skillOwnershipStore{db}, &skillDecisionStore{db}, &skillWriter{db}
}

func copySkillProposal(p *aggregate.SkillProposal) (*aggregate.SkillProposal, error) {
	in := aggregate.SkillProposalInput{
		Op: p.Op(), Kind: p.Kind(), Tool: p.Tool(),
		Name: p.Name(), Description: p.Description(), Body: p.Body(),
		Evidence: p.Evidence(), EvidenceKind: p.EvidenceKind(), EvidenceAt: p.EvidenceAt(), Reason: p.Reason(),
		TargetName: p.TargetName(), TargetDigest: p.TargetDigest(), TargetLevel: p.TargetLevel(),
	}
	q, err := aggregate.NewSkillProposal(p.ID(), in, p.CreatedAt())
	if err != nil {
		return nil, err
	}
	if err := q.SetStored(p.Status(), p.AcceptedPath(), p.AcceptedLevel(), p.DismissReason(), p.CreatedAt(), p.DecidedAt(), p.DecidedOpen()); err != nil {
		return nil, err
	}
	return q, nil
}

func copySkillOwnership(o *aggregate.SkillOwnership) *aggregate.SkillOwnership {
	c, err := aggregate.NewSkillOwnership(o.Root(), o.Name(), o.Level(), o.Digest())
	if err != nil {
		panic(fmt.Sprintf("a stored ownership row does not reparse: %v", err))
	}
	return c
}

func (f *skillProposalStore) Add(_ context.Context, p *aggregate.SkillProposal) error {
	if p.Status() != enum.SkillProposalPending {
		return apperror.New(apperror.StatusBadRequest, "only a pending proposal is added; a decided one is a commit", nil)
	}
	if _, ok := f.db.proposals[p.ID()]; ok {
		return apperror.New(apperror.StatusConflict, "skill proposal "+p.ID()+" already exists", nil)
	}
	for _, q := range f.db.proposals {
		if q.Name() == p.Name() && q.Status() == enum.SkillProposalPending {
			return apperror.New(apperror.StatusConflict, "a pending proposal for this skill already exists", nil)
		}
	}
	c, err := copySkillProposal(p)
	if err != nil {
		return err
	}
	f.db.proposals[p.ID()] = c
	return nil
}

func (f *skillProposalStore) Get(_ context.Context, id string) (*aggregate.SkillProposal, error) {
	p, ok := f.db.proposals[id]
	if !ok {
		return nil, apperror.NewNotFoundError("skill proposal "+id, nil)
	}
	return copySkillProposal(p)
}

func (f *skillProposalStore) Pending(_ context.Context) ([]*aggregate.SkillProposal, error) {
	out := []*aggregate.SkillProposal{}
	for _, p := range f.db.proposals {
		if p.Status() != enum.SkillProposalPending {
			continue
		}
		c, err := copySkillProposal(p)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *skillOwnershipStore) Get(_ context.Context, root enum.SkillRoot, name string) (*aggregate.SkillOwnership, error) {
	o, ok := f.db.rows[skillKey(root, name)]
	if !ok {
		return nil, apperror.NewNotFoundError("the ownership row of "+skillKey(root, name), nil)
	}
	return copySkillOwnership(o), nil
}

func (f *skillOwnershipStore) List(_ context.Context) ([]*aggregate.SkillOwnership, error) {
	out := []*aggregate.SkillOwnership{}
	for _, o := range f.db.rows {
		out = append(out, copySkillOwnership(o))
	}
	return out, nil
}

func (f *skillOwnershipStore) Save(_ context.Context, o *aggregate.SkillOwnership, expectDigest string) error {
	key := skillKey(o.Root(), o.Name())
	cur, exists := f.db.rows[key]
	if expectDigest == "" {
		if exists {
			return apperror.New(apperror.StatusConflict, "the ownership row of "+key+" already exists", nil)
		}
	} else if !exists {
		return apperror.NewNotFoundError("the ownership row of "+key, nil)
	} else if cur.Digest() != expectDigest {
		return apperror.New(apperror.StatusConflict, "the ownership row of "+key+" changed since it was read", nil)
	}
	f.db.rows[key] = copySkillOwnership(o)
	return nil
}

// Commit stores the decision and the row together, rechecking inside the write
// what the domain checked when the row was computed: the store is the last
// compare-and-swap, because the row may have moved since it was read.
func (f *skillDecisionStore) Commit(_ context.Context, p *aggregate.SkillProposal, row *aggregate.SkillOwnership) error {
	stored, ok := f.db.proposals[p.ID()]
	if !ok {
		return apperror.NewNotFoundError("skill proposal "+p.ID(), nil)
	}
	if stored.Status() != enum.SkillProposalPending {
		return apperror.New(apperror.StatusConflict, "skill proposal "+p.ID()+" was already decided", nil)
	}
	switch p.Status() {
	case enum.SkillProposalAccepted:
		if row == nil {
			return apperror.New(apperror.StatusBadRequest, "an accepted proposal commits with its ownership row", nil)
		}
	case enum.SkillProposalDismissed:
		if row != nil {
			return apperror.New(apperror.StatusBadRequest, "only an accepted proposal commits an ownership row", nil)
		}
	default:
		return apperror.New(apperror.StatusBadRequest, "a pending proposal has no decision to commit", nil)
	}
	if row != nil {
		name := p.Name()
		if p.Op() == enum.SkillProposalPatch {
			name = p.TargetName()
		}
		switch {
		case row.Root() != enum.SkillRootNusashell:
			return apperror.New(apperror.StatusBadRequest, "a learned skill's row lives in the nusashell root, not "+string(row.Root()), nil)
		case row.Name() != name:
			return apperror.New(apperror.StatusBadRequest, "the ownership row is not for the skill this proposal names", nil)
		}
		if row.Level() != p.AcceptedLevel() {
			return apperror.New(apperror.StatusBadRequest, "the ownership row's level is not the level the owner accepted", nil)
		}
		cur, exists := f.db.rows[skillKey(row.Root(), row.Name())]
		switch p.Op() {
		case enum.SkillProposalCreate:
			if exists {
				return apperror.New(apperror.StatusConflict, "the skill "+row.Name()+" already has an ownership row", nil)
			}
		case enum.SkillProposalPatch:
			switch {
			case !exists:
				return apperror.New(apperror.StatusConflict, "the skill "+row.Name()+" has no ownership row to patch against", nil)
			case cur.Digest() != p.TargetDigest():
				return apperror.New(apperror.StatusConflict, "the skill "+row.Name()+" changed since the proposal was read", nil)
			case cur.Level() != enum.SkillLevelAgent:
				return apperror.New(apperror.StatusConflict, "the skill "+row.Name()+" is the owner's now", nil)
			}
		}
	}
	c, err := copySkillProposal(p)
	if err != nil {
		return err
	}
	f.db.proposals[p.ID()] = c
	if row != nil {
		f.db.rows[skillKey(row.Root(), row.Name())] = copySkillOwnership(row)
	}
	return nil
}

// renderSkillDoc is the fake's canonical rendering — deterministic, so a digest
// of it is stable. The real adapter renders YAML frontmatter; the contract the
// fake pins is rendering ownership, not the format.
func renderSkillDoc(doc SkillDocument) string {
	return doc.Name + "\n" + doc.Description + "\n" + doc.Body
}

func (f *skillWriter) Write(_ context.Context, root enum.SkillRoot, doc SkillDocument, expectDigest string) (SkillWritten, error) {
	written := SkillWritten{Path: string(root) + "/" + doc.Name + "/SKILL.md"}
	if err := learnedSkillRoot(root); err != nil {
		return written, err
	}
	key := skillKey(root, doc.Name)
	rendered := renderSkillDoc(doc)
	digest := valueobject.SkillDigest(rendered)
	written.Digest = digest
	cur, exists := f.db.files[key]
	if exists && valueobject.SkillDigest(cur) == digest {
		// The exact bytes are already there: a retry of an accept whose commit
		// failed finishes without a second copy.
		return written, nil
	}
	if expectDigest == "" {
		if exists {
			return written, apperror.New(apperror.StatusConflict, "the skill "+key+" already exists", nil)
		}
	} else if !exists || valueobject.SkillDigest(cur) != expectDigest {
		return written, apperror.New(apperror.StatusConflict, "the skill "+key+" changed since it was read", nil)
	}
	f.db.files[key] = rendered
	return written, nil
}

func (f *skillWriter) Digest(doc SkillDocument) (string, error) {
	return valueobject.SkillDigest(renderSkillDoc(doc)), nil
}

func (f *skillWriter) Archive(_ context.Context, root enum.SkillRoot, name, expectDigest string) error {
	if err := learnedSkillRoot(root); err != nil {
		return err
	}
	key := skillKey(root, name)
	cur, ok := f.db.files[key]
	if !ok {
		return apperror.NewNotFoundError("the skill "+key, nil)
	}
	if valueobject.SkillDigest(cur) != expectDigest {
		return apperror.New(apperror.StatusConflict, "the skill "+key+" changed since it was read", nil)
	}
	delete(f.db.files, key)
	f.db.archived[key] = cur
	return nil
}

func learnedSkillRoot(root enum.SkillRoot) error {
	if root != enum.SkillRootNusashell {
		return apperror.New(apperror.StatusBadRequest, "learned skills live in the nusashell root, not "+string(root), nil)
	}
	return nil
}

func skillStatus(err error) int {
	if err == nil {
		return 0
	}
	return apperror.As(err).StatusCode()
}

func skillNow() time.Time { return time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) }

func newProposal(t *testing.T, id, name string) *aggregate.SkillProposal {
	t.Helper()
	p, err := aggregate.NewSkillProposal(id, aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: name, Description: "Use when writing release notes from merged changes",
		Body:         "## Steps\n1. read the log\n2. group by area",
		EvidenceKind: enum.SkillEvidenceQuote, Evidence: "write release notes",
	}, skillNow())
	if err != nil {
		t.Fatalf("proposal fixture: %v", err)
	}
	return p
}

func newPatchProposal(t *testing.T, id, name, digest string) *aggregate.SkillProposal {
	t.Helper()
	p, err := aggregate.NewSkillProposal(id, aggregate.SkillProposalInput{
		Op: enum.SkillProposalPatch, Kind: enum.SkillKindMethod,
		Name: name, Description: "Use when writing release notes from merged changes",
		Body:         "## Steps\n1. read the log\n2. group by area",
		EvidenceKind: enum.SkillEvidenceQuote, Evidence: "write release notes",
		TargetName: name, TargetDigest: digest, TargetLevel: enum.SkillLevelAgent,
	}, skillNow())
	if err != nil {
		t.Fatalf("patch proposal fixture: %v", err)
	}
	return p
}

func learnedRow(t *testing.T, name, digest string) *aggregate.SkillOwnership {
	t.Helper()
	o, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, name, enum.SkillLevelAgent, digest)
	if err != nil {
		t.Fatalf("ownership fixture: %v", err)
	}
	return o
}

func acceptWith(t *testing.T, p *aggregate.SkillProposal, level enum.SkillLevel, cur *aggregate.SkillOwnership) *aggregate.SkillOwnership {
	t.Helper()
	if err := p.Accept("/root/skills/"+p.Name()+"/SKILL.md", level, skillNow()); err != nil {
		t.Fatalf("accept fixture: %v", err)
	}
	row, err := aggregate.ApplySkillOwnership(cur, p, "dw")
	if err != nil {
		t.Fatalf("apply fixture: %v", err)
	}
	return row
}

func TestProposalStoreAddGetPending(t *testing.T) {
	proposals, _, decisions, _ := newSkillFakes()
	ctx := context.Background()
	p := newProposal(t, "p1", "release-notes")
	if err := proposals.Add(ctx, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := proposals.Get(ctx, p.ID())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name() != "release-notes" || got.Status() != enum.SkillProposalPending {
		t.Errorf("got %s/%s, want a pending release-notes", got.Name(), got.Status())
	}
	if err := got.Dismiss("not a skill", skillNow(), true); err != nil {
		t.Fatalf("dismiss fixture: %v", err)
	}
	if err := decisions.Commit(ctx, got, nil); err != nil {
		t.Fatalf("commit the dismissal: %v", err)
	}
	after, err := proposals.Get(ctx, p.ID())
	if err != nil {
		t.Fatalf("get after the decision: %v", err)
	}
	if after.Status() != enum.SkillProposalDismissed {
		t.Errorf("status = %s, want dismissed", after.Status())
	}
	pending, err := proposals.Pending(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("a decided proposal still counts as pending: %d", len(pending))
	}
	if _, err := proposals.Get(ctx, "missing"); skillStatus(err) != 404 {
		t.Errorf("a missing proposal: %v; want a 404", err)
	}
}

func TestProposalStoreRefusesADuplicateAdd(t *testing.T) {
	proposals, _, _, _ := newSkillFakes()
	ctx := context.Background()
	p := newProposal(t, "p1", "release-notes")
	if err := proposals.Add(ctx, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := proposals.Add(ctx, newProposal(t, "p1", "release-notes")); skillStatus(err) != 409 {
		t.Errorf("a duplicate add: %v; want a 409", err)
	}
}

func TestCommitAcceptCreatesTheRowAndStoresTheDecision(t *testing.T) {
	proposals, ownership, decisions, _ := newSkillFakes()
	ctx := context.Background()
	p := newProposal(t, "p1", "release-notes")
	if err := proposals.Add(ctx, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	row := acceptWith(t, p, enum.SkillLevelUser, nil)
	if err := decisions.Commit(ctx, p, row); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, err := proposals.Get(ctx, p.ID())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status() != enum.SkillProposalAccepted || got.AcceptedPath() == "" {
		t.Errorf("status = %s path %q, want an accepted proposal with its path", got.Status(), got.AcceptedPath())
	}
	stored, err := ownership.Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatalf("the row is not stored: %v", err)
	}
	if stored.Level() != enum.SkillLevelUser || stored.Digest() != "dw" {
		t.Errorf("row = %s/%s, want user at the written digest", stored.Level(), stored.Digest())
	}
}

func TestCommitRefusesACreateOverAnExistingRow(t *testing.T) {
	proposals, ownership, decisions, _ := newSkillFakes()
	ctx := context.Background()
	if err := ownership.Save(ctx, learnedRow(t, "release-notes", "d0"), ""); err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	p := newProposal(t, "p1", "release-notes")
	if err := proposals.Add(ctx, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := p.Accept("/root/skills/release-notes/SKILL.md", enum.SkillLevelAgent, skillNow()); err != nil {
		t.Fatalf("accept fixture: %v", err)
	}
	row, rowErr := aggregate.ApplySkillOwnership(nil, p, "dw")
	if rowErr != nil {
		t.Fatalf("apply fixture: %v", rowErr)
	}
	err := decisions.Commit(ctx, p, row)
	if skillStatus(err) != 409 {
		t.Fatalf("a create over an existing row: %v; want a 409", err)
	}
	got, err := proposals.Get(ctx, p.ID())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status() != enum.SkillProposalPending {
		t.Error("the refused commit still stored the decision")
	}
	stored, err := ownership.Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	if stored.Digest() != "d0" {
		t.Error("the refused commit overwrote the existing row")
	}
}

func TestCommitPatchNeedsTheDigestAndLevelItRead(t *testing.T) {
	proposals, ownership, decisions, _ := newSkillFakes()
	ctx := context.Background()
	if err := ownership.Save(ctx, learnedRow(t, "release-notes", "d1"), ""); err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	p := newPatchProposal(t, "p1", "release-notes", "d1")
	if err := proposals.Add(ctx, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	cur, err := ownership.Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatalf("get fixture: %v", err)
	}
	row := acceptWith(t, p, enum.SkillLevelAgent, cur)
	if err := decisions.Commit(ctx, p, row); err != nil {
		t.Fatalf("commit: %v", err)
	}
	stored, err := ownership.Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	if stored.Digest() != "dw" || stored.Level() != enum.SkillLevelAgent {
		t.Errorf("row = %s/%s, want agent at the written digest", stored.Level(), stored.Digest())
	}

	proposals2, ownership2, decisions2, _ := newSkillFakes()
	if err := ownership2.Save(ctx, learnedRow(t, "release-notes", "d2"), ""); err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	stale := newPatchProposal(t, "p1", "release-notes", "d1")
	if err := proposals2.Add(ctx, stale); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := stale.Accept("/root/skills/release-notes/SKILL.md", enum.SkillLevelAgent, skillNow()); err != nil {
		t.Fatalf("accept fixture: %v", err)
	}
	row2, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "dw")
	if err != nil {
		t.Fatalf("row fixture: %v", err)
	}
	if err := decisions2.Commit(ctx, stale, row2); skillStatus(err) != 409 {
		t.Errorf("a patch against a moved digest: %v; want a 409", err)
	}
}

func TestCommitPatchRefusesAnOwnerLevelRow(t *testing.T) {
	proposals, ownership, decisions, _ := newSkillFakes()
	ctx := context.Background()
	ownerRow, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelUser, "d1")
	if err != nil {
		t.Fatalf("row fixture: %v", err)
	}
	if err := ownership.Save(ctx, ownerRow, ""); err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	p := newPatchProposal(t, "p1", "release-notes", "d1")
	if err := proposals.Add(ctx, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := p.Accept("/root/skills/release-notes/SKILL.md", enum.SkillLevelAgent, skillNow()); err != nil {
		t.Fatalf("accept fixture: %v", err)
	}
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "dw")
	if err != nil {
		t.Fatalf("row fixture: %v", err)
	}
	if err := decisions.Commit(ctx, p, row); skillStatus(err) != 409 {
		t.Errorf("a patch against the owner's row: %v; want a 409", err)
	}
}

func TestCommitRefusesADecisionMadeMeanwhile(t *testing.T) {
	proposals, ownership, decisions, _ := newSkillFakes()
	ctx := context.Background()
	p := newProposal(t, "p1", "release-notes")
	if err := proposals.Add(ctx, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	first, err := proposals.Get(ctx, p.ID())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := first.Dismiss("not a skill", skillNow(), true); err != nil {
		t.Fatalf("dismiss fixture: %v", err)
	}
	if err := decisions.Commit(ctx, first, nil); err != nil {
		t.Fatalf("commit the first decision: %v", err)
	}
	if err := p.Accept("/root/skills/release-notes/SKILL.md", enum.SkillLevelAgent, skillNow()); err != nil {
		t.Fatalf("accept fixture: %v", err)
	}
	row, rowErr := aggregate.ApplySkillOwnership(nil, p, "dw")
	if rowErr != nil {
		t.Fatalf("apply fixture: %v", rowErr)
	}
	if err := decisions.Commit(ctx, p, row); skillStatus(err) != 409 {
		t.Errorf("a second decision: %v; want a 409", err)
	}
	if _, err := ownership.Get(ctx, enum.SkillRootNusashell, "release-notes"); skillStatus(err) != 404 {
		t.Error("the refused commit still wrote the row")
	}
	if err := decisions.Commit(ctx, newProposal(t, "p9", "other-notes"), nil); skillStatus(err) != 404 {
		t.Errorf("a proposal that was never stored: %v; want a 404", err)
	}
}

func TestCommitNeedsARowExactlyWhenTheProposalIsAccepted(t *testing.T) {
	proposals, ownership, decisions, _ := newSkillFakes()
	ctx := context.Background()

	accepted := newProposal(t, "p1", "release-notes")
	if err := proposals.Add(ctx, accepted); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := accepted.Accept("/root/skills/release-notes/SKILL.md", enum.SkillLevelAgent, skillNow()); err != nil {
		t.Fatalf("accept fixture: %v", err)
	}
	if err := decisions.Commit(ctx, accepted, nil); skillStatus(err) != 400 {
		t.Errorf("an accept with no row: %v; want a 400", err)
	}

	dismissed := newProposal(t, "p2", "other-notes")
	if err := proposals.Add(ctx, dismissed); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := dismissed.Dismiss("not a skill", skillNow(), true); err != nil {
		t.Fatalf("dismiss fixture: %v", err)
	}
	if err := decisions.Commit(ctx, dismissed, learnedRow(t, "other-notes", "dw")); skillStatus(err) != 400 {
		t.Errorf("a dismissal with a row: %v; want a 400", err)
	}

	pending := newProposal(t, "p3", "third-notes")
	if err := proposals.Add(ctx, pending); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := decisions.Commit(ctx, pending, nil); skillStatus(err) != 400 {
		t.Errorf("a pending proposal: %v; want a 400", err)
	}
	if _, err := ownership.Get(ctx, enum.SkillRootNusashell, "release-notes"); skillStatus(err) != 404 {
		t.Error("a refused commit wrote a row")
	}
}

func TestCommitDismissalStoresNoRow(t *testing.T) {
	proposals, ownership, decisions, _ := newSkillFakes()
	ctx := context.Background()
	p := newProposal(t, "p1", "release-notes")
	if err := proposals.Add(ctx, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := p.Dismiss("not a skill", skillNow(), true); err != nil {
		t.Fatalf("dismiss fixture: %v", err)
	}
	if err := decisions.Commit(ctx, p, nil); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, err := proposals.Get(ctx, p.ID())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status() != enum.SkillProposalDismissed || got.DismissReason() != "not a skill" {
		t.Errorf("status = %s reason %q, want the dismissal", got.Status(), got.DismissReason())
	}
	if _, err := ownership.Get(ctx, enum.SkillRootNusashell, "release-notes"); skillStatus(err) != 404 {
		t.Error("a dismissal wrote a row")
	}
}

func TestSkillOwnershipIsKeyedByRootAndName(t *testing.T) {
	_, ownership, _, _ := newSkillFakes()
	ctx := context.Background()
	if err := ownership.Save(ctx, learnedRow(t, "release-notes", "d1"), ""); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := ownership.Get(ctx, enum.SkillRootGlobal, "release-notes"); skillStatus(err) != 404 {
		t.Errorf("a global lookup answered for the learned row: %v", err)
	}
	got, err := ownership.Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Digest() != "d1" {
		t.Errorf("digest = %q, want d1", got.Digest())
	}
}

func TestSkillOwnershipSaveKeepsACopyAndIsACompareAndSwap(t *testing.T) {
	_, ownership, _, _ := newSkillFakes()
	ctx := context.Background()
	row := learnedRow(t, "release-notes", "d1")
	if err := ownership.Save(ctx, row, ""); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := ownership.Save(ctx, row, ""); skillStatus(err) != 409 {
		t.Errorf("a create over an existing row: %v; want a 409", err)
	}
	if err := row.Observe("d2"); err != nil {
		t.Fatalf("observe fixture: %v", err)
	}
	got, err := ownership.Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Level() != enum.SkillLevelAgent {
		t.Error("the unsaved change reached the store")
	}
	if err := ownership.Save(ctx, row, "a-stale-digest"); skillStatus(err) != 409 {
		t.Errorf("a save against a stale digest: %v; want a 409", err)
	}
	if err := ownership.Save(ctx, row, "d1"); err != nil {
		t.Fatalf("a save against the current digest was refused: %v", err)
	}
	got, err = ownership.Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Level() != enum.SkillLevelUser {
		t.Error("the saved change did not reach the store")
	}
	if err := ownership.Save(ctx, learnedRow(t, "never-written", "d1"), "d1"); skillStatus(err) != 404 {
		t.Errorf("a conditional save against no row: %v; want a 404", err)
	}
}

func skillDoc() SkillDocument {
	return SkillDocument{Name: "release-notes", Description: "Use when writing release notes", Body: "## Steps\n1. read the log\n2. group by area"}
}

func TestSkillWriterRefusesACreateOverAnExistingFile(t *testing.T) {
	_, _, _, writer := newSkillFakes()
	ctx := context.Background()
	if _, err := writer.Write(ctx, enum.SkillRootNusashell, skillDoc(), ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	other := skillDoc()
	other.Body += "\n3. changed"
	if _, err := writer.Write(ctx, enum.SkillRootNusashell, other, ""); skillStatus(err) != 409 {
		t.Errorf("a create over an existing file with other content: %v; want a 409", err)
	}
}

func TestSkillWriterTheSameBytesAreASuccessfulNoOp(t *testing.T) {
	_, _, _, writer := newSkillFakes()
	ctx := context.Background()
	first, err := writer.Write(ctx, enum.SkillRootNusashell, skillDoc(), "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	// A retry of an accept whose commit failed finds the exact bytes it would
	// write: success without a second copy, so the row can still be finished.
	second, err := writer.Write(ctx, enum.SkillRootNusashell, skillDoc(), "")
	if err != nil {
		t.Fatalf("the idempotent rewrite was refused: %v", err)
	}
	if first.Digest == "" || second.Digest != first.Digest {
		t.Errorf("the digests differ across the same write: %q vs %q", first.Digest, second.Digest)
	}
	// and the same completion path holds for a patch whose body already landed
	patched := skillDoc()
	patched.Body += "\n3. group by week"
	if _, err := writer.Write(ctx, enum.SkillRootNusashell, patched, first.Digest); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if _, err := writer.Write(ctx, enum.SkillRootNusashell, patched, first.Digest); err != nil {
		t.Errorf("a patch retry against the old digest was refused though the bytes are already there: %v", err)
	}
}

func TestSkillWriterReplaceIsACompareAndSwap(t *testing.T) {
	_, _, _, writer := newSkillFakes()
	ctx := context.Background()
	first, err := writer.Write(ctx, enum.SkillRootNusashell, skillDoc(), "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	patched := skillDoc()
	patched.Body += "\n3. group by week"
	if _, err := writer.Write(ctx, enum.SkillRootNusashell, patched, "a-stale-digest"); skillStatus(err) != 409 {
		t.Errorf("a replace against a stale digest: %v; want a 409", err)
	}
	if _, err := writer.Write(ctx, enum.SkillRootNusashell, patched, first.Digest); err != nil {
		t.Errorf("a replace against the current digest was refused: %v", err)
	}
}

func TestSkillWriterArchive(t *testing.T) {
	_, _, _, writer := newSkillFakes()
	ctx := context.Background()
	if err := writer.Archive(ctx, enum.SkillRootNusashell, "missing", "x"); skillStatus(err) != 404 {
		t.Errorf("archiving a missing skill: %v; want a 404", err)
	}
	written, err := writer.Write(ctx, enum.SkillRootNusashell, skillDoc(), "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if digest, err := writer.Digest(skillDoc()); err != nil || digest != written.Digest {
		t.Errorf("Digest %q (%v) is not the digest Write recorded %q", digest, err, written.Digest)
	}
	if err := writer.Archive(ctx, enum.SkillRootNusashell, "release-notes", "not-the-digest"); skillStatus(err) != 409 {
		t.Errorf("archiving against a digest the file does not hold: %v; want a 409", err)
	}
	if err := writer.Archive(ctx, enum.SkillRootNusashell, "release-notes", written.Digest); err != nil {
		t.Errorf("archiving an existing skill was refused: %v", err)
	}
}

func TestSkillWriterRefusesAnyRootButNusashell(t *testing.T) {
	_, _, _, writer := newSkillFakes()
	ctx := context.Background()
	if _, err := writer.Write(ctx, enum.SkillRootGlobal, skillDoc(), ""); skillStatus(err) != 400 {
		t.Errorf("a write into the global root: %v; want a 400", err)
	}
	if err := writer.Archive(ctx, enum.SkillRootGlobal, "release-notes", "x"); skillStatus(err) != 400 {
		t.Errorf("an archive in the global root: %v; want a 400", err)
	}
}

func TestSkillFakesAreOneDatabase(t *testing.T) {
	proposals, ownership, decisions, writer := newSkillFakes()
	if proposals.db != ownership.db || proposals.db != decisions.db || proposals.db != writer.db {
		t.Error("the fake port views do not share one database")
	}
}
