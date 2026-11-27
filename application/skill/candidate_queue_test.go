package skill

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// patchProposal is a pending patch of a learned skill, read against digest.
func patchProposal(t *testing.T, id, target, digest string) *aggregate.SkillProposal {
	t.Helper()
	p, err := aggregate.NewSkillProposal(id, aggregate.SkillProposalInput{
		Op: enum.SkillProposalPatch, Kind: enum.SkillKindMethod,
		Name: target, Description: "Use when " + target,
		Body:         "## Steps\n1. read the log\n2. group by area",
		Reason:       "the owner states this rule explicitly",
		Evidence:     "write release notes like the last one",
		EvidenceKind: enum.SkillEvidenceQuote,
		TargetName:   target, TargetDigest: digest, TargetLevel: enum.SkillLevelAgent,
	}, validateTurnAt)
	if err != nil {
		t.Fatalf("proposal %s: %v", id, err)
	}
	return p
}

// staleLearned adds a learned skill the owner edited by hand after its row was
// written, with one pending patch read against the row's digest. accept can
// never write that patch: the file on disk is no longer the one it was read from.
func staleLearned(t *testing.T, w *decideWorld, name string) {
	t.Helper()
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, name, enum.SkillLevelAgent, "old")
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/"+name] = row
	w.catalog = append(w.catalog, repository.Skill{
		Name: name, Description: "Use when " + name, Source: "nusashell",
		Path: "/n/" + name + "/SKILL.md", Digest: "edited",
	})
	w.proposals["stale-"+name] = patchProposal(t, "stale-"+name, name, "old")
}

// openCreate adds a pending create for a name that is neither on disk nor in the
// ownership table: accept can write it.
func openCreate(t *testing.T, w *decideWorld, name string) {
	t.Helper()
	w.proposals["open-"+name] = listProposal(t, "open-"+name, name)
}

// A proposal accept can still write holds the queue, and three of them stop the
// pass before the model is asked anything.
func TestTheQueueHoldsAtThreeAcceptableProposals(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	for i := 0; i < 3; i++ {
		openCreate(t, w, fmt.Sprintf("fresh-%d", i))
	}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateSkipped || gen.calls != 0 {
		t.Fatalf("outcome %s after %d model calls: three open proposals must hold the queue", outcome, gen.calls)
	}
}

// Proposals accept would refuse do not hold the queue. Each stale patch targets a
// skill edited by hand since its row was written, so every one is stuck pending
// on the page and none of them stops the pass from proposing what it judged.
func TestStaleProposalsDoNotHoldTheQueue(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	staleLearned(t, w, "old-0")
	staleLearned(t, w, "old-1")
	staleLearned(t, w, "old-2")
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if gen.calls != 1 {
		t.Fatalf("the model was called %d times: stale proposals held the queue", gen.calls)
	}
	if outcome != CandidateStored {
		t.Fatalf("outcome %s, want stored: pre-commit-tests's row still describes its bytes", outcome)
	}
}

// A create whose name was taken after it was proposed is refused by accept, so it
// is not acceptable and does not hold the queue.
func TestACreateTakenSinceItWasProposedDoesNotHoldTheQueue(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("taken-%d", i)
		openCreate(t, w, name)
		w.catalog = append(w.catalog, repository.Skill{
			Name: name, Description: "Use when " + name, Source: "nusashell",
			Path: "/n/" + name + "/SKILL.md", Digest: "d",
		})
	}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if gen.calls != 1 || outcome == CandidateSkipped {
		t.Fatalf("outcome %s after %d model calls: names taken on disk may not hold the queue", outcome, gen.calls)
	}
}

// The gate counts only what accept can still write. Two open proposals and any
// number of stale ones leave room for the pass to propose, and three open ones
// do not, whatever else is on the page.
func TestTheQueueCountsOnlyAcceptableProposals(t *testing.T) {
	for _, tc := range []struct {
		name        string
		open, stale int
		held        bool
	}{
		{"two open, one stale", 2, 1, false},
		{"two open, three stale", 2, 3, false},
		{"three open, two stale", 3, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, gen := candidatePatchWorld(t, "rd1", "rd1")
			for i := 0; i < tc.open; i++ {
				openCreate(t, w, fmt.Sprintf("fresh-%d", i))
			}
			for i := 0; i < tc.stale; i++ {
				staleLearned(t, w, fmt.Sprintf("old-%d", i))
			}
			outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
			if tc.held {
				if outcome != CandidateSkipped || gen.calls != 0 {
					t.Fatalf("outcome %s after %d model calls: %d open proposals must hold the queue", outcome, gen.calls, tc.open)
				}
				return
			}
			if outcome == CandidateSkipped || gen.calls != 1 {
				t.Fatalf("outcome %s after %d model calls: %d open proposals may not hold the queue", outcome, gen.calls, tc.open)
			}
		})
	}
}

// brokenOwnership fails every read of the ownership table, as a store that cannot
// be read does: the list the gate and the page take, and the row the pass takes.
type brokenOwnership struct{ decideOwnership }

func (brokenOwnership) Get(context.Context, enum.SkillRoot, string) (*aggregate.SkillOwnership, error) {
	return nil, apperror.New(500, "the ownership table could not be read", nil)
}

func (brokenOwnership) List(context.Context) ([]*aggregate.SkillOwnership, error) {
	return nil, apperror.New(500, "the ownership table could not be read", nil)
}

// When the ownership table cannot be read the gate cannot tell which proposals
// accept would refuse, so the pass fails rather than guess either way: a guess
// would hide the queue from the owner or let it grow past its limit.
func TestUnreadableOwnershipFailsTheQueueCheck(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	for i := 0; i < 3; i++ {
		openCreate(t, w, fmt.Sprintf("fresh-%d", i))
	}
	deps := candidateDeps(w, gen)
	deps.Ownership = brokenOwnership{decideOwnership{w}}
	outcome := Candidate(context.Background(), deps, candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateUnavailable || gen.calls != 0 {
		t.Fatalf("outcome %s after %d model calls: an unreadable table must fail the pass", outcome, gen.calls)
	}
}

// pendingNamed lists the pending proposals of one name in the fake store.
func pendingNamed(w *decideWorld, name string) []*aggregate.SkillProposal {
	var out []*aggregate.SkillProposal
	for _, p := range w.proposals {
		if p.Status() == enum.SkillProposalPending && p.Name() == name {
			out = append(out, p)
		}
	}
	return out
}

// A pending proposal of the name the pass proposes, read against a target that has
// moved since, is retired with its cause, and the new proposal is stored in the
// same pass. The model is asked once, and the name ends with one pending row.
func TestAStaleProposalOfTheNameIsRetiredForTheNewOne(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	stale := patchProposal(t, "stale-pre-commit", "pre-commit-tests", "rd0")
	w.proposals[stale.ID()] = stale
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateStored {
		t.Fatalf("outcome %s, want stored: a stale proposal may not hold its name", outcome)
	}
	if gen.calls != 1 {
		t.Fatalf("the model was called %d times, want once", gen.calls)
	}
	// The fake store hands out copies, so the stored row is read back from the map.
	got := w.proposals[stale.ID()]
	want := aggregate.SupersededReasonPrefix + "the skill's file changed since this proposal read it"
	if got.Status() != enum.SkillProposalDismissed || got.DismissReason() != want {
		t.Errorf("stale proposal is %s with reason %q, want dismissed with %q", got.Status(), got.DismissReason(), want)
	}
	pending := pendingNamed(w, "pre-commit-tests")
	if len(pending) != 1 || pending[0].ID() == stale.ID() || pending[0].TargetDigest() != "rd1" {
		t.Errorf("pending proposals of the name: %d, want exactly the new patch read against rd1", len(pending))
	}
}

// A create proposed while its name was free, and taken since, is retired with the
// cause that fits a create; the patch that now targets the name takes its place.
func TestAStaleCreateIsRetiredWithItsCause(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	stale := listProposal(t, "stale-create", "pre-commit-tests")
	w.proposals[stale.ID()] = stale
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateStored || gen.calls != 1 {
		t.Fatalf("outcome %s after %d model calls, want stored after one", outcome, gen.calls)
	}
	got := w.proposals[stale.ID()]
	want := aggregate.SupersededReasonPrefix + "the name is live in the nusashell root since it was proposed"
	if got.Status() != enum.SkillProposalDismissed || got.DismissReason() != want {
		t.Errorf("stale create is %s with reason %q, want dismissed with %q", got.Status(), got.DismissReason(), want)
	}
}

// An open proposal of the name holds it: the pass retires nothing, and the cooldown
// stops the candidate before the store is written.
func TestAnOpenProposalOfTheNameHoldsItAndIsNotRetired(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	open := patchProposal(t, "open-pre-commit", "pre-commit-tests", "rd1")
	w.proposals[open.ID()] = open
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateCooldown || gen.calls != 1 {
		t.Fatalf("outcome %s after %d model calls, want cooldown after one", outcome, gen.calls)
	}
	if got := w.proposals[open.ID()]; got.Status() != enum.SkillProposalPending {
		t.Errorf("the open proposal is %s; the owner can still accept it", got.Status())
	}
}

// A draft the validator refuses retires nothing: the store changes only for the
// proposal the pass is about to store.
func TestARefusedDraftRetiresNothing(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd2")
	stale := patchProposal(t, "stale-pre-commit", "pre-commit-tests", "rd0")
	w.proposals[stale.ID()] = stale
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateRejected {
		t.Fatalf("outcome %s, want rejected: the patch targets a hand-edited skill", outcome)
	}
	if got := w.proposals[stale.ID()]; got.Status() != enum.SkillProposalPending {
		t.Errorf("a refused draft retired the stale proposal to %s", got.Status())
	}
}

// A retirement the store refuses stops the pass before anything new is stored, and
// the candidate is not put in its place.
func TestARefusedRetirementStoresNothing(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	stale := patchProposal(t, "stale-pre-commit", "pre-commit-tests", "rd0")
	w.proposals[stale.ID()] = stale
	w.commitFail = true
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateUnavailable || gen.calls != 1 {
		t.Fatalf("outcome %s after %d model calls, want unavailable after one", outcome, gen.calls)
	}
	if len(w.proposals) != 1 {
		t.Errorf("the store holds %d proposals after a refused retirement, want only the stale one", len(w.proposals))
	}
	if got := w.proposals[stale.ID()]; got.Status() != enum.SkillProposalPending {
		t.Errorf("a refused retirement left the stale proposal %s", got.Status())
	}
}

// The retirement judges the skill as it stands when the pass retires, not as it
// stood when the pass began: the model call in between can be long, and the owner
// may edit the skill during it. A proposal open at the start is retired at the end.
func TestTheRetirementReadsTheSkillAsItStandsNow(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	held := patchProposal(t, "open-then", "pre-commit-tests", "rd1")
	w.proposals[held.ID()] = held
	deps := candidateDeps(w, gen)
	deps.Generate = answeringEdit(w, gen.generate, func(skills []repository.Skill) { skills[0].Digest = "rd2" })
	outcome := Candidate(context.Background(), deps, candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateTargetChanged {
		t.Errorf("outcome %s, want target-changed: the target moved after the draft was read", outcome)
	}
	if n := len(pendingNamed(w, "pre-commit-tests")); n != 0 {
		t.Errorf("%d pending proposals of the name after a moved target, want none", n)
	}
	got := w.proposals[held.ID()]
	want := aggregate.SupersededReasonPrefix + "the skill's file changed since this proposal read it"
	if got.Status() != enum.SkillProposalDismissed || got.DismissReason() != want {
		t.Errorf("the proposal open when the pass began is %s with reason %q, want dismissed with %q", got.Status(), got.DismissReason(), want)
	}
}

// A pass without its decision store cannot retire what it finds stale, so it does
// not run at all: the model is not asked for a proposal the pass could not store.
func TestAPassWithoutItsDecisionStoreIsUnavailable(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	deps := candidateDeps(w, gen)
	deps.Decisions = nil
	outcome := Candidate(context.Background(), deps, candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateUnavailable || gen.calls != 0 {
		t.Fatalf("outcome %s after %d model calls, want unavailable before the model", outcome, gen.calls)
	}
}

// logged points the pass's log at a buffer, so a test can read the reason the
// pass gave for its outcome.
func logged(deps *CandidateDeps) *bytes.Buffer {
	var buf bytes.Buffer
	deps.Log = slog.New(slog.NewTextHandler(&buf, nil))
	return &buf
}

// answeringEdit wraps the model call so the owner's edit lands while the model
// answers. The edit works on a new slice: the pass's snapshot keeps the bytes it
// read, as the real catalog does.
func answeringEdit(w *decideWorld, generate Generator, edit func([]repository.Skill)) Generator {
	return func(ctx context.Context, convID, system, user string) (string, error) {
		edited := append([]repository.Skill(nil), w.catalog...)
		edit(edited)
		w.catalog = edited
		return generate(ctx, convID, system, user)
	}
}

// accept refuses a create over a row that is still there, file or not. The pass
// refuses such a draft itself, and the log names the rule.
func TestACreateOverAnOrphanRowIsRejectedByTheValidator(t *testing.T) {
	w, gen := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}, &candidateGen{reply: candidateCreateReply()}
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "pre-commit-tests", enum.SkillLevelAgent, "rd1")
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/pre-commit-tests"] = row
	deps := candidateDeps(w, gen)
	buf := logged(&deps)
	outcome := Candidate(context.Background(), deps, candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateRejected || gen.calls != 1 {
		t.Fatalf("outcome %s after %d model calls, want rejected after one", outcome, gen.calls)
	}
	if !strings.Contains(buf.String(), "orphan-row") {
		t.Errorf("the refusal did not name the orphan rule: %s", buf)
	}
	if len(w.proposals) != 0 {
		t.Errorf("a create over an orphan row was stored: %d", len(w.proposals))
	}
}

// The owner edits the target while the model answers. The draft was valid against
// the bytes the model saw, so nothing is refused for its own sake: nothing is
// stored, and the outcome names the move.
func TestATargetMovedWhileTheModelAnswersIsNotStored(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	deps := candidateDeps(w, gen)
	buf := logged(&deps)
	deps.Generate = answeringEdit(w, gen.generate, func(skills []repository.Skill) { skills[0].Digest = "rd2" })
	outcome := Candidate(context.Background(), deps, candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateTargetChanged || gen.calls != 1 {
		t.Fatalf("outcome %s after %d model calls, want target-changed after one", outcome, gen.calls)
	}
	if !strings.Contains(buf.String(), "the target changed while the model answered") {
		t.Errorf("the outcome does not name the change: %s", buf)
	}
	if len(w.proposals) != 0 {
		t.Errorf("the store holds %d proposals after a moved target, want none", len(w.proposals))
	}
}

// The control for the move above: with nothing moved between the read and the
// store, the same pass stores its proposal.
func TestAnUnmovedTargetIsStoredAfterTheReread(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	deps := candidateDeps(w, gen)
	buf := logged(&deps)
	outcome := Candidate(context.Background(), deps, candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateStored {
		t.Fatalf("outcome %s, want stored: nothing moved (%s)", outcome, buf)
	}
	if n := len(pendingNamed(w, "pre-commit-tests")); n != 1 {
		t.Errorf("pending proposals of the name = %d, want the one stored", n)
	}
}

// A create whose name is taken or claimed while the model answers is not stored
// either: the name is no longer free, and the outcome names the move.
func TestACreateNameMovedWhileTheModelAnswersIsNotStored(t *testing.T) {
	for _, tc := range []struct {
		name  string
		why   string
		claim func(t *testing.T, w *decideWorld)
	}{
		{
			name: "taken on disk",
			why:  "the name is live in the nusashell root while the model answered",
			claim: func(t *testing.T, w *decideWorld) {
				w.catalog = append(w.catalog, repository.Skill{
					Name: "pre-commit-tests", Description: "Use when committing code", Source: "nusashell",
					Path: "/n/pre-commit-tests/SKILL.md", Digest: "d1",
				})
			},
		},
		{
			name: "taken in another workspace",
			why:  "the name is live in the workspace root while the model answered",
			claim: func(t *testing.T, w *decideWorld) {
				w.elsewhere = map[string]string{"pre-commit-tests": "workspace"}
			},
		},
		{
			name: "claimed by a row",
			why:  "the name was claimed while the model answered",
			claim: func(t *testing.T, w *decideWorld) {
				row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "pre-commit-tests", enum.SkillLevelAgent, "rd1")
				if err != nil {
					t.Fatal(err)
				}
				w.rows["nusashell/pre-commit-tests"] = row
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, gen := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}, &candidateGen{reply: candidateCreateReply()}
			deps := candidateDeps(w, gen)
			buf := logged(&deps)
			generate := deps.Generate
			deps.Generate = func(ctx context.Context, convID, system, user string) (string, error) {
				tc.claim(t, w)
				return generate(ctx, convID, system, user)
			}
			outcome := Candidate(context.Background(), deps, candidateInput(candidateRoom(t, candidateOwnerLine)))
			if outcome != CandidateTargetChanged {
				t.Fatalf("outcome %s, want target-changed: the name moved after the draft was read (%s)", outcome, buf)
			}
			if !strings.Contains(buf.String(), tc.why) {
				t.Errorf("the outcome does not name the move: %s", buf)
			}
			if len(w.proposals) != 0 {
				t.Errorf("the store holds %d proposals after a moved name, want none", len(w.proposals))
			}
		})
	}
}

// A create's name must be free in every root. A pending create whose name a
// builtin, global, or workspace skill has taken is not open: the queue does not
// hold for it, and the page labels it stale.
func TestACreateWhoseNameIsLiveInAnotherRootIsNotOpen(t *testing.T) {
	p := listProposal(t, "fresh-idea", "release-notes")
	if !newLearnedState(nil, nil, nil).open(p) {
		t.Fatal("a create whose name is free in every root must be open")
	}
	taken := []repository.Skill{{
		Name: "release-notes", Description: "Use when writing release notes", Source: "builtin",
		Path: "/b/release-notes/SKILL.md", Digest: "bd1",
	}}
	if newLearnedState(taken, nil, nil).open(p) {
		t.Error("a create over a builtin's name is open: the queue would hold for it and the page would not label it stale")
	}
	if newLearnedState(nil, nil, map[string]string{"release-notes": "workspace"}).open(p) {
		t.Error("a create whose name is live only in another workspace is open: accept would refuse it")
	}
}

// The reason a retired proposal is dismissed with names the state the owner would
// find: the root that holds the name, a target that no longer reads as a skill,
// a digest that moved, or a row that no longer describes the file.
func TestTheSupersedeCauseNamesTheState(t *testing.T) {
	rowAt := func(name, digest string, level enum.SkillLevel) *aggregate.SkillOwnership {
		t.Helper()
		o, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, name, level, digest)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	row := func(name, digest string) *aggregate.SkillOwnership {
		return rowAt(name, digest, enum.SkillLevelAgent)
	}
	learned := func(name, digest string) repository.Skill {
		return repository.Skill{Name: name, Description: "Use when " + name, Source: "nusashell", Path: "/n/" + name + "/SKILL.md", Digest: digest}
	}
	for _, tc := range []struct {
		name   string
		skills []repository.Skill
		rows   []*aggregate.SkillOwnership
		p      *aggregate.SkillProposal
		want   string
	}{
		{
			name:   "a create whose name a builtin holds",
			skills: []repository.Skill{{Name: "release-notes", Description: "Use when writing release notes", Source: "builtin", Path: "/b/release-notes/SKILL.md", Digest: "bd1"}},
			p:      listProposal(t, "c-live", "release-notes"),
			want:   "the name is live in the builtin root since it was proposed",
		},
		{
			name: "a create whose name only its row still claims",
			rows: []*aggregate.SkillOwnership{row("release-notes", "rd1")},
			p:    listProposal(t, "c-row", "release-notes"),
			want: "the name is still owned by the loop, but its file no longer reads as a skill since it was proposed",
		},
		{
			name: "a patch whose target file is gone",
			rows: []*aggregate.SkillOwnership{row("pre-commit-tests", "rd1")},
			p:    patchProposal(t, "p-gone", "pre-commit-tests", "rd1"),
			want: "the target no longer reads as a skill since it was proposed",
		},
		{
			name:   "a patch whose target digest moved",
			skills: []repository.Skill{learned("pre-commit-tests", "rd2")},
			rows:   []*aggregate.SkillOwnership{row("pre-commit-tests", "rd1")},
			p:      patchProposal(t, "p-moved", "pre-commit-tests", "rd1"),
			want:   "the skill's file changed since this proposal read it",
		},
		{
			name:   "a patch whose row no longer describes the file",
			skills: []repository.Skill{learned("pre-commit-tests", "rd1")},
			rows:   []*aggregate.SkillOwnership{row("pre-commit-tests", "rd0")},
			p:      patchProposal(t, "p-row", "pre-commit-tests", "rd1"),
			want:   "the target's ownership row no longer describes it since it was proposed",
		},
		{
			// The owner moved the target to Only you. The row still holds the file's
			// digest, so the row is right and the owner's move is the cause; the
			// cause must not blame the file.
			name:   "a patch whose target the owner moved to Only you, file unchanged",
			skills: []repository.Skill{learned("pre-commit-tests", "rd1")},
			rows:   []*aggregate.SkillOwnership{rowAt("pre-commit-tests", "rd1", enum.SkillLevelUser)},
			p:      patchProposal(t, "p-only-you", "pre-commit-tests", "rd1"),
			want:   "the owner moved it to Only you since it was proposed",
		},
		{
			// Both facts hold: the owner moved it to Only you, and the row describes
			// an older file. The row's description is the one that names the state.
			name:   "a patch moved to Only you whose row also describes an older file",
			skills: []repository.Skill{learned("pre-commit-tests", "rd1")},
			rows:   []*aggregate.SkillOwnership{rowAt("pre-commit-tests", "rd0", enum.SkillLevelUser)},
			p:      patchProposal(t, "p-both", "pre-commit-tests", "rd1"),
			want:   "the target's ownership row no longer describes it since it was proposed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newLearnedState(tc.skills, tc.rows, nil)
			if st.open(tc.p) {
				t.Fatal("the proposal is open; the case needs one that is not")
			}
			if got := st.supersedeCause(tc.p); got != tc.want {
				t.Errorf("cause %q, want %q", got, tc.want)
			}
		})
	}
}
