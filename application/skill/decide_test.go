package skill

import (
	"context"
	"fmt"
	"sort"
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
	// The hooks cancel a request at the exact step a client disconnect can
	// reach, so the doubles can prove what still commits afterwards.
	afterWrite        func()
	afterArchive      func()
	afterOwnershipGet func()
	// elsewhere names a skill that is live only in some other workspace
	// folder, so the focus catalog does not carry it.
	elsewhere map[string]string
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
		EvidenceAt: p.EvidenceAt(),
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

// Declined mirrors the store's read: the owner's dismissals of proposals that
// were open when they closed, newest first, within the window. A retirement is
// not a refusal.
func (d decideProposals) Declined(ctx context.Context, since time.Time, limit int) ([]*aggregate.SkillProposal, error) {
	var out []*aggregate.SkillProposal
	for _, p := range d.w.proposals {
		if p.Status() != enum.SkillProposalDismissed || !p.DecidedOpen() || p.DecidedAt().Before(since) {
			continue
		}
		if strings.HasPrefix(p.DismissReason(), aggregate.SupersededReasonPrefix) {
			continue
		}
		q, err := d.Get(ctx, p.ID())
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DecidedAt().Equal(out[j].DecidedAt()) {
			return out[i].DecidedAt().After(out[j].DecidedAt())
		}
		return out[i].ID() > out[j].ID()
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
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
		// The store holds a name for a decision of its op when the proposal was
		// accepted, or dismissed while it was open. A stale dismissal holds nothing.
		holds := p.Status() == enum.SkillProposalAccepted || p.DecidedOpen()
		if p.Op() == op && !p.DecidedAt().Before(since) && !superseded && holds {
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
		copied, err := aggregate.NewSkillOwnership(row.Root(), row.Name(), row.Level(), row.Digest())
		if d.w.afterOwnershipGet != nil {
			d.w.afterOwnershipGet()
		}
		return copied, err
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
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.w.commitFail {
		d.w.commitFail = false
		return fmt.Errorf("a simulated commit failure")
	}
	if row != nil {
		key := string(row.Root()) + "/" + row.Name()
		cur, exists := d.w.rows[key]
		if p.Op() == enum.SkillProposalCreate {
			if exists {
				return apperror.New(409, "the skill's name was taken while the decision was written", nil)
			}
		} else if !exists || cur.Digest() != p.TargetDigest() || cur.Level() != p.TargetLevel() {
			return apperror.New(409, "the ownership row moved while the decision was written", nil)
		}
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
		if d.w.afterWrite != nil {
			d.w.afterWrite()
		}
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
	if d.w.afterWrite != nil {
		d.w.afterWrite()
	}
	return written, nil
}
func (d decideWriter) Digest(doc repository.SkillDocument) (string, error) {
	return skillDigestString(decideRender(doc)), nil
}

// Archive moves the file out only while it still holds the digest the caller
// read, as the real writer does, and it records the move in the call order.
func (d decideWriter) Archive(ctx context.Context, root enum.SkillRoot, name, expectDigest string) error {
	key := string(root) + "/" + name
	cur, ok := d.w.files[key]
	if !ok {
		return apperror.NewNotFoundError("the skill "+name, nil)
	}
	if skillDigestString(cur) != expectDigest {
		return apperror.New(409, "the skill "+name+" changed since it was read", nil)
	}
	d.w.writeOrder("archive")
	delete(d.w.files, key)
	if d.w.afterArchive != nil {
		d.w.afterArchive()
	}
	return nil
}

type decideCatalog struct{ w *decideWorld }

// List is the catalog the fake writer's files belong to. A nusashell file is a
// live skill with the digest it holds, as on disk. It replaces an authored
// nusashell entry of its name and a builtin one, and a global or workspace
// skill of its name hides it, as the roots override one another.
func (d decideCatalog) List(ctx context.Context, workspace string) (repository.SkillCatalog, error) {
	var names []string
	onFile := map[string]bool{}
	for key := range d.w.files {
		if name, ok := strings.CutPrefix(key, "nusashell/"); ok {
			names = append(names, name)
			onFile[name] = true
		}
	}
	sort.Strings(names)
	higher := map[string]bool{}
	for _, s := range d.w.catalog {
		if s.Source == "global" || s.Source == "workspace" {
			higher[s.Name] = true
		}
	}
	var skills []repository.Skill
	for _, s := range d.w.catalog {
		if onFile[s.Name] && (s.Source == "nusashell" || s.Source == "builtin") {
			continue
		}
		skills = append(skills, s)
	}
	for _, name := range names {
		if higher[name] {
			continue
		}
		content := d.w.files["nusashell/"+name]
		skills = append(skills, repository.Skill{
			Name: name, Description: decideDescription(content), Source: "nusashell",
			Path: "nusashell/" + name + "/SKILL.md", Digest: skillDigestString(content),
		})
	}
	return repository.SkillCatalog{Skills: skills}, nil
}

// decideDescription reads the description back out of a fake rendered skill,
// which is its second line.
func decideDescription(content string) string {
	lines := strings.SplitN(content, "\n", 3)
	if len(lines) < 2 {
		return ""
	}
	return lines[1]
}
func (d decideCatalog) ListAcross(ctx context.Context, focus string, others []string) (repository.SkillCatalog, map[string]string, error) {
	cat, err := d.List(ctx, focus)
	if err != nil {
		return cat, nil, err
	}
	// Taken holds every holder, as the real read does: a holder outside
	// nusashell is preferred, and an authored entry the effective list no
	// longer shows (a builtin under a learned file) still counts.
	taken := map[string]string{}
	hold := func(name, root string) {
		if cur, ok := taken[name]; !ok || (cur == "nusashell" && root != "nusashell") {
			taken[name] = root
		}
	}
	for _, s := range cat.Skills {
		hold(s.Name, s.Source)
	}
	for _, s := range d.w.catalog {
		hold(s.Name, s.Source)
	}
	for name, root := range d.w.elsewhere {
		hold(name, root)
	}
	return cat, taken, nil
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

func TestDecideAcceptCommitSurvivesClientCancellation(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	ctx, cancel := context.WithCancel(context.Background())
	w.afterWrite = cancel
	if err := Decide(ctx, deps, decideInput(p.ID(), "accept")); err != nil {
		t.Fatalf("accept with a client that disconnected after the write: %v", err)
	}
	if got := w.proposals[p.ID()].Status(); got != enum.SkillProposalAccepted {
		t.Fatalf("proposal status = %s after the client disconnected; the decision must still commit", got)
	}
}

func TestDecideDismissCommitSurvivesClientCancellation(t *testing.T) {
	w, deps, p := decideLeftover(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.afterArchive = cancel
	in := decideInput(p.ID(), "dismiss")
	in.Reason = "not needed"
	if err := Decide(ctx, deps, in); err != nil {
		t.Fatalf("dismiss with a client that disconnected after the archive: %v", err)
	}
	if got := w.proposals[p.ID()].Status(); got != enum.SkillProposalDismissed {
		t.Fatalf("proposal status = %s after the client disconnected; the decision must still commit", got)
	}
}

func TestDecideDismissCommitFailureStatesTheArchiveConditionally(t *testing.T) {
	w, deps, p := decideLeftover(t)
	w.commitFail = true
	in := decideInput(p.ID(), "dismiss")
	in.Reason = "not needed"
	err := Decide(context.Background(), deps, in)
	if apperror.As(err).StatusCode() != 500 {
		t.Fatalf("dismiss commit failure = %v, want a 500", err)
	}
	for _, want := range []string{"archived", "exactly as the loop wrote it", "decision was not recorded", "trying the dismissal again is safe"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("dismiss failure missing %q: %q", want, err.Error())
		}
	}
}

func TestDecideDismissCommitFailureDoesNotPromiseArchiveForEditedFile(t *testing.T) {
	w, deps, p := decideLeftover(t)
	w.files["nusashell/"+p.Name()] += "owner edit"
	w.commitFail = true
	in := decideInput(p.ID(), "dismiss")
	in.Reason = "not needed"
	err := Decide(context.Background(), deps, in)
	if apperror.As(err).StatusCode() != 500 {
		t.Fatalf("dismiss commit failure = %v, want a 500", err)
	}
	for _, want := range []string{"left in place", "exactly as the loop wrote it", "decision was not recorded", "trying the dismissal again is safe"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("edited-file dismiss failure missing %q: %q", want, err.Error())
		}
	}
	if strings.Contains(err.Error(), "was archived because") {
		t.Errorf("an edited file must not be promised as archived: %q", err.Error())
	}
}

func TestDecideAcceptOwnershipRaceAfterWriteIsAPartialWrite(t *testing.T) {
	w, deps := decideSetup()
	decidePatchWorld(t, w)
	// The owner takes the skill back after the accept's checks and file write,
	// while the decision commit is still open.
	w.afterWrite = func() {
		row := w.rows["nusashell/release-notes"]
		w.rows["nusashell/release-notes"], _ = aggregate.NewSkillOwnership(row.Root(), row.Name(), enum.SkillLevelUser, row.Digest())
	}
	err := Decide(context.Background(), deps, decideInput("sprop_test", "accept"))
	if apperror.As(err).StatusCode() != 500 {
		t.Fatalf("accept racing a level move after the write = %v, want a partial-write 500", err)
	}
	if !strings.Contains(err.Error(), "wrote the skill file") || !strings.Contains(err.Error(), "dismiss it") || !strings.Contains(err.Error(), "level") {
		t.Errorf("partial-write error does not state the recovery path: %q", err.Error())
	}
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

// The refusal names how far over the budget the catalog would be, with the
// thousands separator the owner reads in a sentence.
func TestDecideOverBudgetNamesTheOverage(t *testing.T) {
	w, deps := decideSetup()
	const otherDesc = 1500
	w.catalog = append(w.catalog, repository.Skill{Name: "other", Description: strings.Repeat("d", otherDesc), Source: "builtin"})
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	total := len(p.Name()) + len(p.Description()) + len("other") + otherDesc
	deps.Budget = total - 1234
	err := Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	const want = "the catalog would be over its byte budget by 1,234 bytes; make room before accepting"
	if apperror.As(err).StatusCode() != 409 || err.Error() != want {
		t.Errorf("over budget: %v; want a 409 saying %q", err, want)
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
	if got := w.proposals[p.ID()].Status(); got != enum.SkillProposalAccepted {
		t.Errorf("the second accept did not decide the proposal: %s", got)
	}
}

func TestDecidePatchWithMovedTargetIsChanged(t *testing.T) {
	// the target the patch was read against is not in the live nusashell
	// catalog any more — deleted, or shadowed by an outer root
	for _, tc := range []struct {
		name       string
		catalog    []repository.Skill
		removeFile bool
	}{
		{"deleted", nil, true},
		{"shadowed by workspace", []repository.Skill{{Name: "release-notes", Description: "x", Source: "workspace"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, deps := decideSetup()
			decidePatchWorld(t, w)
			w.catalog = tc.catalog
			if tc.removeFile {
				delete(w.files, "nusashell/release-notes")
			}
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

// A name that is live only in another workspace folder is not in this
// workspace's catalog, and accept still refuses the create with that root.
func TestDecideCreateWhoseNameIsLiveOnlyInAnotherWorkspaceIsRefused(t *testing.T) {
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	w.elsewhere = map[string]string{"release-notes": "workspace"}
	err := Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	if err == nil || apperror.As(err).StatusCode() != 409 || !strings.Contains(err.Error(), "workspace") {
		t.Errorf("a create over another workspace's name: %v; want 409 naming the workspace root", err)
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

// decideLeftover makes the state a failed accept leaves: the file is written,
// no ownership row claims it, and the proposal is still pending.
func decideLeftover(t *testing.T) (*decideWorld, DecideDeps, *aggregate.SkillProposal) {
	t.Helper()
	w, deps := decideSetup()
	p := decideProposal(t, enum.SkillProposalCreate)
	w.proposals[p.ID()] = p
	deps.Decisions.(decideDecisions).w.commitFail = true
	if apperror.As(Decide(context.Background(), deps, decideInput(p.ID(), "accept"))).StatusCode() != 500 {
		t.Fatal("the first accept must fail at the commit")
	}
	if _, ok := w.files["nusashell/release-notes"]; !ok || w.rows["nusashell/release-notes"] != nil {
		t.Fatalf("a failed accept must leave the file and no row: files %v, rows %v", w.files, w.rows)
	}
	return w, deps, p
}

// Closing the loop's own leftover archives it: a live nusashell skill with no
// row is worse than an unaccepted proposal, and nothing else would remove it.
func TestDecideDismissArchivesTheLoopsOwnLeftover(t *testing.T) {
	w, deps, p := decideLeftover(t)
	in := decideInput(p.ID(), "dismiss")
	in.Reason = "the skill is too narrow"
	if err := Decide(context.Background(), deps, in); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if _, ok := w.files["nusashell/release-notes"]; ok {
		t.Error("the leftover survived the dismissal as a live skill with no row")
	}
	if n := len(w.calls); n < 2 || w.calls[n-2] != "archive" || w.calls[n-1] != "commit" {
		t.Errorf("the archive must come before the commit: %v", w.calls)
	}
	stored := w.proposals[p.ID()]
	if stored.Status() != enum.SkillProposalDismissed {
		t.Errorf("not dismissed: %s", stored.Status())
	}
	if !stored.DecidedOpen() {
		t.Error("a leftover is open until the owner closes it, so the dismissal should record it open")
	}
}

// A file the owner edited since the failed accept is not the loop's: the
// dismissal must leave it alone and record the proposal as stale.
func TestDecideDismissKeepsAFileTheOwnerEdited(t *testing.T) {
	w, deps, p := decideLeftover(t)
	const edit = "the owner's own edit of the same name"
	w.files["nusashell/release-notes"] = edit
	in := decideInput(p.ID(), "dismiss")
	in.Reason = "the skill is too narrow"
	if err := Decide(context.Background(), deps, in); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if w.files["nusashell/release-notes"] != edit {
		t.Errorf("the owner's edit was changed: %q", w.files["nusashell/release-notes"])
	}
	for _, call := range w.calls {
		if call == "archive" {
			t.Errorf("a dismissal archived a file the loop did not write: %v", w.calls)
		}
	}
	if w.proposals[p.ID()].DecidedOpen() {
		t.Error("a stale dismissal recorded as open would hold the name for nothing")
	}
}

// Accept applies the same rule: an edited file of the name is a collision, so
// the accept is refused with the nusashell root named and nothing is written.
func TestDecideAcceptRefusesAnEditedLeftover(t *testing.T) {
	w, deps, p := decideLeftover(t)
	w.files["nusashell/release-notes"] = "the owner's own edit of the same name"
	calls := len(w.calls)
	err := Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	if apperror.As(err).StatusCode() != 409 || !strings.Contains(err.Error(), "nusashell") {
		t.Errorf("an accept over an edited file: %v; want a 409 naming the nusashell root", err)
	}
	if len(w.calls) != calls {
		t.Errorf("the refused accept reached the writer: %v", w.calls)
	}
}

// The leftover's bytes are the incoming skill's, counted once: a budget that
// fits exactly one copy must let the retry finish.
func TestDecideCountsTheLoopsOwnLeftoverOnce(t *testing.T) {
	_, deps, p := decideLeftover(t)
	deps.Budget = len(p.Name()) + len(p.Description())
	if err := Decide(context.Background(), deps, decideInput(p.ID(), "accept")); err != nil {
		t.Fatalf("the retry counted the leftover twice: %v", err)
	}
}

// A patch whose commit failed has its new bytes on disk under the old row. Those
// bytes are not the file the patch read, so the proposal no longer applies: a
// second accept is refused before the writer runs, and the proposal stays
// pending for the owner to dismiss.
func TestDecidePatchCommitFailureLeavesAStaleProposal(t *testing.T) {
	w, deps := decideSetup()
	decidePatchWorld(t, w)
	p := w.proposals["sprop_test"]
	w.commitFail = true
	const failed = "the loop wrote the skill file, but the decision was not recorded; this proposal no longer applies to that file, so dismiss it and set the skill's level on the Skills page"
	if err := Decide(context.Background(), deps, decideInput(p.ID(), "accept")); err == nil || err.Error() != failed || apperror.As(err).StatusCode() != 500 {
		t.Fatalf("the first patch accept: %v; want a 500 saying %q", err, failed)
	}
	calls := len(w.calls)
	const refused = "the loop wrote this skill's file, but the decision was not recorded; this proposal no longer applies"
	err := Decide(context.Background(), deps, decideInput(p.ID(), "accept"))
	if apperror.As(err).StatusCode() != 409 || err.Error() != refused {
		t.Errorf("the second accept: %v; want a 409 saying %q", err, refused)
	}
	if len(w.calls) != calls {
		t.Errorf("the refused accept reached the writer: %v", w.calls)
	}
	if got := w.proposals[p.ID()].Status(); got != enum.SkillProposalPending {
		t.Errorf("the refused accept changed the proposal: %s", got)
	}
}

// Dismissing a patch's leftover keeps the bytes. They are the body the owner
// approved, but the row keeps the old digest, so the loop has no authority over
// them: the skill reads as the owner's, and the queue marks the proposal stale
// with its cause. Nothing archives the file, because the patch never created it.
func TestDecidePatchLeftoverDismissKeepsTheBytes(t *testing.T) {
	w, deps := decideSetup()
	decidePatchWorld(t, w)
	p := w.proposals["sprop_test"]
	w.commitFail = true
	if apperror.As(Decide(context.Background(), deps, decideInput(p.ID(), "accept"))).StatusCode() != 500 {
		t.Fatal("the first patch accept must fail at the commit")
	}
	leftover := w.files["nusashell/release-notes"]
	list := ListDeps{Catalog: deps.Catalog, Ownership: deps.Ownership, Proposals: deps.Proposals, Writer: deps.Writer}
	items, err := ListPending(context.Background(), list, "")
	const cause = "the loop wrote this skill's file, but the decision was not recorded"
	if err != nil || len(items) != 1 || items[0].Open || items[0].StaleCause != cause {
		t.Fatalf("the queue must show the leftover stale with its cause: %+v, %v", items, err)
	}
	in := decideInput(p.ID(), "dismiss")
	in.Reason = "the new steps are not what the owner asked for"
	if err := Decide(context.Background(), deps, in); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if w.files["nusashell/release-notes"] != leftover {
		t.Error("the dismissal changed the bytes the patch wrote")
	}
	for _, call := range w.calls {
		if call == "archive" {
			t.Errorf("a patch leftover was archived: %v", w.calls)
		}
	}
	row := w.rows["nusashell/release-notes"]
	if row == nil || row.Digest() != p.TargetDigest() {
		t.Fatalf("the row moved on a dismissal: %+v", row)
	}
	if level := aggregate.SkillStandingOf(row, skillDigestString(leftover)).Level; level != enum.SkillLevelUser {
		t.Errorf("the row still gives the loop %s over bytes it does not describe", level)
	}
	if w.proposals[p.ID()].DecidedOpen() {
		t.Error("a stale dismissal recorded as open would hold the name for nothing")
	}
}

// The stale cause names the loop's write only for its own failed commit: the file
// holds the bytes the patch would write, and the row still describes the file the
// patch read. A row that already records those bytes means the decision was
// recorded, and a file moved to other bytes is not the loop's write. Neither gets
// the loop's name, so the owner is not told a write was theirs or the loop's.
func TestPatchStaleCauseNamesTheLoopOnlyForItsOwnFailedCommit(t *testing.T) {
	w, deps := decideSetup()
	target, _ := decidePatchWorld(t, w)
	p := w.proposals["sprop_test"]
	w.commitFail = true
	if apperror.As(Decide(context.Background(), deps, decideInput(p.ID(), "accept"))).StatusCode() != 500 {
		t.Fatal("the first patch accept must fail at the commit")
	}
	leftover := w.files["nusashell/release-notes"]
	list := ListDeps{Catalog: deps.Catalog, Ownership: deps.Ownership, Proposals: deps.Proposals, Writer: deps.Writer}
	cause := func(t *testing.T) string {
		t.Helper()
		items, err := ListPending(context.Background(), list, "")
		if err != nil || len(items) != 1 || items[0].Open {
			t.Fatalf("the queue must show one stale proposal: %+v, %v", items, err)
		}
		return items[0].StaleCause
	}
	const loopWrote = "the loop wrote this skill's file, but the decision was not recorded"
	const changed = "the skill's file changed since this proposal read it"
	if got := cause(t); got != loopWrote {
		t.Errorf("the leftover's cause %q, want %q", got, loopWrote)
	}

	// The row now describes the bytes on disk: the decision was recorded for them.
	recorded, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, skillDigestString(leftover))
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	w.rows["nusashell/release-notes"] = recorded
	if got := cause(t); got != changed {
		t.Errorf("a recorded decision's cause %q, want %q", got, changed)
	}

	// The row is back on the file the patch read, and the file holds other bytes.
	read, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, target)
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	w.rows["nusashell/release-notes"] = read
	w.files["nusashell/release-notes"] = "a file the loop did not write"
	if got := cause(t); got != changed {
		t.Errorf("a file moved to other bytes: cause %q, want %q", got, changed)
	}
}

// A pass that retires a patch's leftover closes it through the same release as a
// dismissal. Only a create's leftover is archived, so the bytes stay.
func TestReleaseOwnFileKeepsAPatchLeftover(t *testing.T) {
	w, deps := decideSetup()
	decidePatchWorld(t, w)
	p := w.proposals["sprop_test"]
	w.commitFail = true
	if apperror.As(Decide(context.Background(), deps, decideInput(p.ID(), "accept"))).StatusCode() != 500 {
		t.Fatal("the first patch accept must fail at the commit")
	}
	st, err := learnedNow(context.Background(), deps.Catalog, deps.Folders, deps.Ownership, deps.Writer, "", []*aggregate.SkillProposal{p})
	if err != nil || !st.own[p.ID()] {
		t.Fatalf("the leftover must be the loop's own before release: own %v, %v", st.own[p.ID()], err)
	}
	if _, err := releaseOwnFile(context.Background(), deps.Writer, st, p); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, ok := w.files["nusashell/release-notes"]; !ok {
		t.Error("a patch's leftover was removed from the root")
	}
	for _, call := range w.calls {
		if call == "archive" {
			t.Errorf("a patch leftover was archived: %v", w.calls)
		}
	}
}

// An owner who moved the target to Only you has changed the level, not the
// file: the refusal names that move, not an edit the row failed to follow.
func TestDecidePatchTheOwnerMovedToOnlyYouSaysSo(t *testing.T) {
	w, deps := decideSetup()
	digest, _ := decidePatchWorld(t, w)
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelUser, digest)
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	w.rows["nusashell/release-notes"] = row
	err = Decide(context.Background(), deps, decideInput("sprop_test", "accept"))
	const want = "the owner moved it to Only you since it was proposed; this proposal no longer applies"
	if apperror.As(err).StatusCode() != 409 || err.Error() != want {
		t.Errorf("accept after the owner's move: %v; want a 409 saying %q", err, want)
	}
	if len(w.calls) != 0 {
		t.Errorf("the refusal must come before the write: %v", w.calls)
	}
}

// The queue reads the leftover by the same rule accept applies: open while it
// is the loop's own, and stale with its cause once the owner has edited it.
func TestQueueReadsTheLeftoverByTheAcceptRule(t *testing.T) {
	w, deps, _ := decideLeftover(t)
	list := ListDeps{Catalog: deps.Catalog, Ownership: deps.Ownership, Proposals: deps.Proposals, Writer: deps.Writer}
	items, err := ListPending(context.Background(), list, "")
	if err != nil || len(items) != 1 || !items[0].Open || items[0].StaleCause != "" {
		t.Fatalf("the leftover must be open in the queue: %+v, %v", items, err)
	}
	w.files["nusashell/release-notes"] = "the owner's own edit of the same name"
	items, err = ListPending(context.Background(), list, "")
	const cause = "the name is live in the nusashell root since it was proposed"
	if err != nil || len(items) != 1 || items[0].Open || items[0].StaleCause != cause {
		t.Errorf("the edited leftover must be stale with its cause: %+v, %v", items, err)
	}
}

// Retiring a stale create closes it like a dismissal, so it archives the
// loop's own leftover too; otherwise the pass would leave the same orphan.
func TestRetiredCreateArchivesTheLoopsOwnLeftover(t *testing.T) {
	w, deps, p := decideLeftover(t)
	w.elsewhere = map[string]string{"release-notes": "workspace"}
	cand := CandidateDeps{
		Catalog: deps.Catalog, Proposals: deps.Proposals, Ownership: deps.Ownership,
		Decisions: deps.Decisions, Writer: deps.Writer,
	}
	id, err := retireSuperseded(context.Background(), cand, "", "release-notes", validateTurnAt.AddDate(0, 0, 1))
	if err != nil || id != p.ID() {
		t.Fatalf("retire: id %q, err %v", id, err)
	}
	if _, ok := w.files["nusashell/release-notes"]; ok {
		t.Error("the retired create left its own leftover behind")
	}
	stored := w.proposals[p.ID()]
	if stored.Status() != enum.SkillProposalDismissed || !strings.HasPrefix(stored.DismissReason(), aggregate.SupersededReasonPrefix) {
		t.Errorf("not retired with a superseded reason: %s %q", stored.Status(), stored.DismissReason())
	}
}
