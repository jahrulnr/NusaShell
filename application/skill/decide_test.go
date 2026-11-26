package skill

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// The doubles record their call order, because the orchestration is the
// contract: preconditions before the write, the write before the commit, no
// commit when any check refuses.
type decideWorld struct {
	proposals  map[string]*aggregate.SkillProposal
	rows       map[string]*aggregate.SkillOwnership
	files      map[string]string
	catalog    []repository.Skill
	calls      []string
	commitFail bool
}

func decideDoc() repository.SkillDocument {
	return repository.SkillDocument{Name: "release-notes", Description: "Use when writing release notes", Body: "## Steps\n1. read the log\n2. group by area"}
}

func decideRender(doc repository.SkillDocument) string {
	return doc.Name + "\n" + doc.Description + "\n" + doc.Body
}

func (w *decideWorld) Get(ctx context.Context, id string) (*aggregate.SkillProposal, error) {
	if p, ok := w.proposals[id]; ok {
		return p, nil
	}
	return nil, apperror.NewNotFoundError("proposal", nil)
}

func (w *decideWorld) GetOwnership() {}

func (w *decideWorld) writeOrder(mark string) { w.calls = append(w.calls, mark) }

type decideProposals struct{ w *decideWorld }

func (d decideProposals) Add(ctx context.Context, p *aggregate.SkillProposal) error {
	d.w.proposals[p.ID()] = p
	return nil
}
func (d decideProposals) Get(ctx context.Context, id string) (*aggregate.SkillProposal, error) {
	p, err := d.w.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// a copy, because the database knows nothing of in-memory mutations: a
	// failed commit must leave the stored proposal exactly as it was
	in := aggregate.SkillProposalInput{
		Op: p.Op(), Kind: p.Kind(), Tool: p.Tool(),
		Name: p.Name(), Description: p.Description(), Body: p.Body(),
		Evidence: p.Evidence(), EvidenceKind: p.EvidenceKind(), Reason: p.Reason(),
		TargetName: p.TargetName(), TargetDigest: p.TargetDigest(), TargetLevel: p.TargetLevel(),
	}
	q, err := aggregate.NewSkillProposal(p.ID(), in, p.CreatedAt())
	if err != nil {
		return nil, err
	}
	if err := q.SetStored(p.Status(), p.AcceptedPath(), p.AcceptedLevel(), p.DismissReason(), p.CreatedAt(), p.DecidedAt()); err != nil {
		return nil, err
	}
	return q, nil
}
func (d decideProposals) Pending(ctx context.Context) ([]*aggregate.SkillProposal, error) {
	var out []*aggregate.SkillProposal
	for _, p := range d.w.proposals {
		if p.Status() != enum.SkillProposalPending {
			continue
		}
		q, err := d.Get(ctx, p.ID())
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, nil
}

func (d decideProposals) Seen(ctx context.Context, name string, op enum.SkillProposalOp, since time.Time) (bool, error) {
	for _, p := range d.w.proposals {
		if p.Name() != name {
			continue
		}
		// Mirrors the store: a pending proposal holds the name whatever its op;
		// a decision holds it for its op, unless the pass retired the proposal.
		if p.Status() == enum.SkillProposalPending {
			return true, nil
		}
		superseded := strings.HasPrefix(p.DismissReason(), aggregate.SupersededReasonPrefix)
		if p.Op() == op && !p.DecidedAt().Before(since) && !superseded {
			return true, nil
		}
	}
	return false, nil
}

type decideOwnership struct{ w *decideWorld }

func (d decideOwnership) Get(ctx context.Context, root enum.SkillRoot, name string) (*aggregate.SkillOwnership, error) {
	if row, ok := d.w.rows[string(root)+"/"+name]; ok {
		// a copy, as the proposals double does: the database knows nothing of
		// in-memory mutation, and a double that aliases cannot detect it
		return aggregate.NewSkillOwnership(row.Root(), row.Name(), row.Level(), row.Digest())
	}
	return nil, apperror.NewNotFoundError("row", nil)
}
func (d decideOwnership) List(ctx context.Context) ([]*aggregate.SkillOwnership, error) {
	out := []*aggregate.SkillOwnership{}
	for _, row := range d.w.rows {
		c, err := aggregate.NewSkillOwnership(row.Root(), row.Name(), row.Level(), row.Digest())
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
func (d decideOwnership) Save(ctx context.Context, o *aggregate.SkillOwnership, expectDigest string) error {
	// The same compare-and-swap the SQL store keeps: a double that ignores
	// its arguments passes a caller that sends the wrong expectDigest, which
	// is exactly the bug a move must not be able to hide.
	key := string(o.Root()) + "/" + o.Name()
	if expectDigest == "" {
		if _, ok := d.w.rows[key]; ok {
			return apperror.New(409, "an ownership row for this skill already exists", nil)
		}
	} else if row, ok := d.w.rows[key]; !ok {
		return apperror.NewNotFoundError("row", nil)
	} else if row.Digest() != expectDigest {
		return apperror.New(409, "the ownership row moved since it was read", nil)
	}
	copied, err := aggregate.NewSkillOwnership(o.Root(), o.Name(), o.Level(), o.Digest())
	if err != nil {
		return err
	}
	d.w.rows[key] = copied
	return nil
}

type decideDecisions struct{ w *decideWorld }

func (d decideDecisions) Commit(ctx context.Context, p *aggregate.SkillProposal, row *aggregate.SkillOwnership) error {
	d.w.writeOrder("commit")
	if d.w.commitFail {
		d.w.commitFail = false
		return fmt.Errorf("a simulated commit failure")
	}
	d.w.proposals[p.ID()] = p
	if row != nil {
		copied, err := aggregate.NewSkillOwnership(row.Root(), row.Name(), row.Level(), row.Digest())
		if err != nil {
			return err
		}
		d.w.rows[string(row.Root())+"/"+row.Name()] = copied
	}
	return nil
}

type decideWriter struct{ w *decideWorld }

func (d decideWriter) Write(ctx context.Context, root enum.SkillRoot, doc repository.SkillDocument, expectDigest string) (repository.SkillWritten, error) {
	d.w.writeOrder("write")
	key := string(root) + "/" + doc.Name
	rendered := decideRender(doc)
	digest := skillDigestString(rendered)
	written := repository.SkillWritten{Path: key + "/SKILL.md", Digest: digest}
	cur, exists := d.w.files[key]
	if exists && skillDigestString(cur) == digest {
		return written, nil
	}
	if expectDigest == "" {
		if exists {
			return written, apperror.New(409, "exists", nil)
		}
	} else if !exists || skillDigestString(cur) != expectDigest {
		return written, apperror.New(409, "changed", nil)
	}
	d.w.files[key] = rendered
	return written, nil
}
func (d decideWriter) Archive(ctx context.Context, root enum.SkillRoot, name string) error {
	return nil
}

type decideCatalog struct{ w *decideWorld }

func (d decideCatalog) List(ctx context.Context, workspace string) (repository.SkillCatalog, error) {
	return repository.SkillCatalog{Skills: d.w.catalog}, nil
}
func (d decideCatalog) Read(ctx context.Context, workspace, name string) (repository.Skill, string, error) {
	return repository.Skill{}, "", nil
}

func skillDigestString(s string) string {
	return fmt.Sprintf("%x", len(s)) + "-" + s[:8]
}

func decideSetup() (*decideWorld, DecideDeps) {
	w := &decideWorld{
		proposals: map[string]*aggregate.SkillProposal{},
		rows:      map[string]*aggregate.SkillOwnership{},
		files:     map[string]string{},
	}
	deps := DecideDeps{
		Proposals: decideProposals{w},
		Decisions: decideDecisions{w},
		Ownership: decideOwnership{w},
		Catalog:   decideCatalog{w},
		Writer:    decideWriter{w},
		Budget:    1 << 20,
	}
	return w, deps
}

func decideProposal(t *testing.T, op enum.SkillProposalOp) *aggregate.SkillProposal {
	t.Helper()
	in := aggregate.SkillProposalInput{
		Op: op, Kind: enum.SkillKindMethod,
		Name: "release-notes", Description: "Use when writing release notes",
		Body:         "## Steps\n1. read the log\n2. group by area",
		Evidence:     "write release notes like the last one",
		EvidenceKind: enum.SkillEvidenceQuote,
		TargetName:   "",
		TargetDigest: "",
		TargetLevel:  "",
	}
	p, err := aggregate.NewSkillProposal("sprop_test", in, validateTurnAt)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	return p
}

func decideInput(id, action string) DecideInput {
	return DecideInput{ID: id, Action: action, Now: validateTurnAt.AddDate(0, 0, 1)}
}

func TestDecideAcceptsACreate(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	if err := Decide(context.Background(), deps, decideInput(p.ID(), "accept")); err != nil {
		t.Fatalf("accept: %v", err)
	}
	stored := w.proposals[p.ID()]
	if stored.Status() != enum.SkillProposalAccepted {
		t.Errorf("the proposal is not accepted: %s", stored.Status())
	}
	if len(w.files) != 1 {
		t.Errorf("no file written: %+v", w.files)
	}
	row := w.rows["nusashell/release-notes"]
	if row == nil || row.Level() != enum.SkillLevelAgent {
		t.Errorf("the ownership row is missing or wrong: %+v", row)
	}
	if len(w.calls) != 2 || w.calls[0] != "write" || w.calls[1] != "commit" {
		t.Errorf("the write must come before the commit: %v", w.calls)
	}
	if stored.AcceptedLevel() != enum.SkillLevelAgent {
		t.Errorf("the default level is not agent: %s", stored.AcceptedLevel())
	}
	if stored.AcceptedPath() == "" {
		t.Error("the accepted proposal records no path")
	}
}

func TestDecideDismiss(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	in := decideInput(p.ID(), "dismiss")
	in.Reason = "the skill is too narrow"
	if err := Decide(context.Background(), deps, in); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if stored := w.proposals[p.ID()]; stored.Status() != enum.SkillProposalDismissed {
		t.Errorf("not dismissed: %s", stored.Status())
	}
	if len(w.files) != 0 {
		t.Error("a dismissal wrote a file")
	}
	if len(w.rows) != 0 {
		t.Error("a dismissal left an ownership row")
	}
}

func TestDecideRefusesADecidedProposal(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	if err := Decide(context.Background(), deps, decideInput(p.ID(), "accept")); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if err := Decide(context.Background(), deps, decideInput(p.ID(), "accept")); apperror.As(err).StatusCode() != 409 {
		t.Errorf("a second decide of the same proposal: %v; want a 409", err)
	}
}

func TestDecideRefusesAForeignFile(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	// A file the loop never wrote appeared after validation: create-only refuses.
	w.files["nusashell/release-notes"] = "someone else's hand-written skill"
	err := Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	if apperror.As(err).StatusCode() != 409 {
		t.Errorf("a create over a foreign file: %v; want a 409", err)
	}
	if stored := w.proposals[p.ID()]; stored.Status() != enum.SkillProposalPending {
		t.Errorf("the proposal must stay pending: %s", stored.Status())
	}
	if len(w.rows) != 0 {
		t.Error("a row was recorded for a refused write")
	}
}

func TestDecideOverBudget(t *testing.T) {
	w, deps := decideSetup()
	deps.Budget = 1
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	err := Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	if apperror.As(err).StatusCode() != 409 || !strings.Contains(err.Error(), "budget") {
		t.Errorf("over budget: %v; want a 409 naming the budget", err)
	}
	if len(w.files) != 0 {
		t.Error("an over-budget accept wrote a file")
	}
}

func TestDecidePatchChangedTarget(t *testing.T) {
	w, deps := decideSetup()
	ws, patchedRow := decidePatchWorld(t, w)
	_ = ws
	// the row moved since the proposal was read: the owner edited the file and
	// the observation flipped it
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelUser, patchedRow)
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	w.rows["nusashell/release-notes"] = row
	p := w.proposals["sprop_test"]
	err = Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	if apperror.As(err).StatusCode() != 409 {
		t.Errorf("a patch whose target changed: %v; want a 409", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("a changed target must never reach the write: %v", w.calls)
	}
}

func decidePatchWorld(t *testing.T, w *decideWorld) (string, string) {
	t.Helper()
	original := decideDoc()
	rendered := decideRender(original)
	digest := skillDigestString(rendered)
	w.files["nusashell/release-notes"] = rendered
	w.catalog = []repository.Skill{{Name: "release-notes", Description: "Use when writing release notes", Source: "nusashell"}}
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, digest)
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	w.rows["nusashell/release-notes"] = row
	in := aggregate.SkillProposalInput{
		Op: enum.SkillProposalPatch, Kind: enum.SkillKindMethod,
		Name: "release-notes", Description: "Use when writing release notes",
		Body:         "## Steps\n1. read the log\n2. group by area and week",
		Evidence:     "write release notes like the last one",
		EvidenceKind: enum.SkillEvidenceQuote,
		TargetName:   "release-notes", TargetDigest: digest, TargetLevel: enum.SkillLevelAgent,
	}
	p, err := aggregate.NewSkillProposal("sprop_test", in, validateTurnAt)
	if err != nil {
		t.Fatalf("patch proposal: %v", err)
	}
	w.proposals[p.ID()] = p
	return digest, digest
}

func TestDecidePatchAcceptedAsUserIsRefused(t *testing.T) {
	w, deps := decideSetup()
	decidePatchWorld(t, w)
	in := decideInput("sprop_test", "accept")
	in.Level = "user"
	err := Decide(context.Background(), deps, in)
	if apperror.As(err).StatusCode() != 400 {
		t.Errorf("a patch accepted as user: %v; want a 400", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("the refusal must come before the write: %v", w.calls)
	}
}

func TestDecideAcceptsAPatch(t *testing.T) {
	w, deps := decideSetup()
	decidePatchWorld(t, w)
	if err := Decide(context.Background(), deps, decideInput("sprop_test", "accept")); err != nil {
		t.Fatalf("accept patch: %v", err)
	}
	row := w.rows["nusashell/release-notes"]
	if row == nil || row.Level() != enum.SkillLevelAgent {
		t.Errorf("a patch must keep the agent level: %+v", row)
	}
	p := w.proposals["sprop_test"]
	if p.Status() != enum.SkillProposalAccepted {
		t.Errorf("not accepted: %s", p.Status())
	}
	if row := w.rows["nusashell/release-notes"]; row.Digest() == "" {
		t.Error("the row records no written digest")
	}
}

func TestDecideCommitFailureAndSecondAcceptFinishes(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	// The first commit fails after the file is written: the file stays, the
	// store is untouched, and the error tells the owner a retry finishes it.
	decisions := deps.Decisions.(decideDecisions)
	decisions.w.commitFail = true
	err := Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	if apperror.As(err).StatusCode() != 500 {
		t.Errorf("a failed commit: %v; want a 500", err)
	}
	if len(w.files) != 1 {
		t.Error("the file must stay when the commit fails")
	}
	if len(w.rows) != 0 {
		t.Error("a failed commit left a row behind")
	}
	// The store still holds the pending proposal; the owner accepts again.
	pending := w.proposals[p.ID()]
	if pending.Status() != enum.SkillProposalPending {
		t.Fatalf("the stored proposal must still be pending, got %s", pending.Status())
	}
	if err := Decide(context.Background(), deps, decideInput(p.ID(), "accept")); err != nil {
		t.Fatalf("the second accept: %v", err)
	}
	if w.rows["nusashell/release-notes"] == nil {
		t.Error("the second accept did not finish the row")
	}
}

func TestDecidePatchWithMovedTargetIsChanged(t *testing.T) {
	// the target the patch was read against is not in the live nusashell
	// catalog any more — deleted, or shadowed by an outer root
	for _, tc := range []struct {
		name    string
		catalog []repository.Skill
	}{
		{"deleted", nil},
		{"shadowed by workspace", []repository.Skill{{Name: "release-notes", Description: "x", Source: "workspace"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, deps := decideSetup()
			decidePatchWorld(t, w)
			w.catalog = tc.catalog
			err := Decide(context.Background(), deps, decideInput("sprop_test", "accept"))
			if apperror.As(err).StatusCode() != 409 {
				t.Errorf("target moved: %v; want 409", err)
			}
			if len(w.files) != 1 {
				// the fixture file is still the only one — nothing written
				for f := range w.files {
					if f != "nusashell/release-notes" {
						t.Errorf("a write happened for a moved target: %s", f)
					}
				}
			}
			if w.proposals["sprop_test"].Status() != enum.SkillProposalPending {
				t.Error("the proposal must stay pending for a re-read")
			}
		})
	}
}

func TestDecideCreateWithAnExistingRowIsChanged(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d1")
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/release-notes"] = row
	err = Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	if apperror.As(err).StatusCode() != 409 {
		t.Errorf("a create over an existing row: %v; want 409", err)
	}
	if len(w.files) != 0 {
		t.Error("a write happened though the row already exists")
	}
}

// accept checks the name in every root: a create whose name a builtin skill took
// after the draft was admitted is refused with that root named, and nothing is
// written or committed.
func TestDecideCreateWhoseNameIsLiveInAnotherRootIsRefused(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	w.catalog = []repository.Skill{{
		Name: "release-notes", Description: "Use when writing release notes", Source: "builtin",
		Path: "/b/release-notes/SKILL.md", Digest: "bd1",
	}}
	err := Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	if err == nil || apperror.As(err).StatusCode() != 409 || !strings.Contains(err.Error(), "builtin") {
		t.Errorf("a create over a builtin's name: %v; want 409 naming the builtin root", err)
	}
	if len(w.files) != 0 || len(w.calls) != 0 {
		t.Errorf("a refused name was written or committed: files %v, calls %v", w.files, w.calls)
	}
	if got := w.proposals[p.ID()]; got.Status() != enum.SkillProposalPending {
		t.Errorf("the refused proposal is %s, want pending", got.Status())
	}
}

func TestDecideZeroTimeRefusedBeforeTheWrite(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	in := decideInput(p.ID(), "accept")
	in.Now = time.Time{}
	err := Decide(context.Background(), deps, in)
	if apperror.As(err).StatusCode() != 400 {
		t.Errorf("a zero decision time: %v; want 400", err)
	}
	if len(w.files) != 0 {
		t.Error("a write happened ahead of input validation")
	}
}
