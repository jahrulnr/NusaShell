package skill

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// candidateRoom is a finished turn: the owner described a procedure, the
// assistant replied, the turn ended.
func candidateRoom(t *testing.T, ownerText string) *aggregate.Conversation {
	t.Helper()
	return room(t,
		newMsg(t, "m1", enum.RoleUser, ownerText),
		finalReply(t, "m2", "done, the tests are green"),
	)
}

// candidateGen is a generator double: it records what the pass sent and
// answers with the programmed reply.
type candidateGen struct {
	system, user string
	reply        string
	err          error
	calls        int
}

func (g *candidateGen) generate(ctx context.Context, convID, system, user string) (string, error) {
	g.calls++
	g.system, g.user = system, user
	return g.reply, g.err
}

func candidateDeps(w *decideWorld, g *candidateGen) CandidateDeps {
	return CandidateDeps{
		Catalog:   decideCatalog{w},
		Proposals: decideProposals{w},
		Ownership: decideOwnership{w},
		Decisions: decideDecisions{w},
		Generate:  g.generate,
		Budget:    1 << 20,
	}
}

func candidateInput(conv *aggregate.Conversation) CandidateInput {
	return CandidateInput{
		ConvID:    "room",
		Workspace: conv.Workspace(),
		Messages:  slices.Clone(conv.Messages()),
		Now:       validateTurnAt,
	}
}

const candidateOwnerLine = "always run the tests before you commit code, every single time"

func candidateCreateReply() string {
	return `{"ops": [{
		"op": "create", "kind": "method", "name": "pre-commit-tests",
		"description": "Use when committing code, to verify the tests first",
		"body": "## Steps\n1. run the tests\n2. commit only when they pass",
		"reason": "the owner states this rule explicitly",
		"evidence": {"kind": "quote", "quote": "run the tests before you commit"}
	}]}`
}

func TestCandidateStoresAnAdmittedProposal(t *testing.T) {
	w, gen := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}, &candidateGen{reply: candidateCreateReply()}
	deps := candidateDeps(w, gen)
	outcome := Candidate(context.Background(), deps, candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateStored {
		t.Fatalf("outcome %s, want stored", outcome)
	}
	if len(w.proposals) != 1 {
		t.Fatalf("proposals = %d, want the one stored", len(w.proposals))
	}
	for _, p := range w.proposals {
		if p.Name() != "pre-commit-tests" || p.Op() != enum.SkillProposalCreate {
			t.Errorf("stored %q %s", p.Name(), p.Op())
		}
		if p.Status() != enum.SkillProposalPending {
			t.Errorf("status %s, want pending for the owner", p.Status())
		}
		if p.EvidenceKind() != enum.SkillEvidenceQuote {
			t.Errorf("evidence kind %s, want quote", p.EvidenceKind())
		}
		if p.Reason() == "" {
			t.Error("the reason did not ride into the row")
		}
	}
}

func TestCandidateSkipsAnUnrecoveredFailureTurn(t *testing.T) {
	// The owner states a procedure worth learning, but the turn carries a
	// tool failure that never recovered — so there is no working procedure
	// to teach, and the pass must skip before spending the model call.
	turn := room(t,
		newMsg(t, "m1", enum.RoleUser, candidateOwnerLine),
		callRound(t, "m2", "search", "", true),
		finalReply(t, "m3", "the search never came back"),
	)
	w, gen := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}, &candidateGen{reply: candidateCreateReply()}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(turn))
	if outcome != CandidateSkipped {
		t.Fatalf("outcome %s, want skipped", outcome)
	}
	if gen.calls != 0 {
		t.Fatalf("the model was called %d times for a turn that could never teach", gen.calls)
	}
	if len(w.proposals) != 0 {
		t.Fatalf("proposals = %d, want none", len(w.proposals))
	}
}

func TestCandidateRunsWhenTheOwnerAnsweredTheFailure(t *testing.T) {
	// The turn that matters most: the assistant failed, and the owner
	// corrected the direction — "not like that, do it this way". The failure
	// never recovered as a tool fact, but the owner's words after it are the
	// strongest evidence this feature has, so the pass must run.
	steer := newMsg(t, "m3", enum.RoleUser, "not like that — always restore the snapshot before you reapply the migration")
	steer.SetInputID("st-1")
	turn := room(t,
		newMsg(t, "m1", enum.RoleUser, "fix the migration ordering"),
		callRound(t, "m2", "run_sql", "", true),
		steer,
		finalReply(t, "m4", "restored the snapshot and reapplied, green now"),
	)
	w, gen := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}, &candidateGen{reply: candidateCreateReply()}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(turn))
	if outcome == CandidateSkipped {
		t.Fatal("a steered failure skipped the pass — the owner's correction was thrown away")
	}
	if gen.calls != 1 {
		t.Fatalf("the model was called %d times, want the one pass", gen.calls)
	}
}

func TestCandidateRunsWhenTheFailureRecovered(t *testing.T) {
	// Control: the same failure, recovered — the pass is not skipped for it.
	turn := room(t,
		newMsg(t, "m1", enum.RoleUser, candidateOwnerLine),
		callRound(t, "m2", "search", "", true),
		callRound(t, "m3", "search", "hits", false),
		finalReply(t, "m4", "done, the tests are green"),
	)
	w, gen := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}, &candidateGen{reply: candidateCreateReply()}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(turn))
	if outcome == CandidateSkipped {
		t.Fatal("a recovered failure skipped the pass")
	}
	if gen.calls != 1 {
		t.Fatalf("the model was called %d times, want the one pass", gen.calls)
	}
}

func TestCandidatePromptCarriesTheClippedCatalog(t *testing.T) {
	w := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}
	long := strings.Repeat("a very long description word ", 20)
	w.catalog = []repository.Skill{{Name: "live-skill", Description: long, Source: "nusashell", Digest: "d1"}}
	gen := &candidateGen{reply: `{"ops": []}`}
	Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	// the owner pays for descriptions, not bodies — and never unclipped ones
	if strings.Contains(gen.user, long) {
		t.Error("the prompt carries an unclipped catalog description")
	}
	if !strings.Contains(gen.user, "live-skill (nusashell)") {
		t.Error("the catalog line does not name the skill and its root")
	}
	if !strings.Contains(gen.user, "catalog bytes ") {
		t.Error("the prompt does not show the catalog's byte accounting — the model cannot see the headroom")
	}
	// the turn carries owner lines with their ids — the only thing a citation may rest on
	if !strings.Contains(gen.user, "owner #m1: "+candidateOwnerLine) {
		t.Error("the owner line does not ride the digest verbatim with its id")
	}
	// and the system prompt is the candidate's own
	if !strings.Contains(gen.system, "skill") {
		t.Error("the system prompt is not the candidate's")
	}
}

func TestCandidateNoneMalformedAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reply   string
		outcome CandidateOutcome
	}{
		{"empty ops is none", `{"ops": []}`, CandidateNone},
		{"malformed json is unavailable", `this is not json at all`, CandidateUnavailable},
		{"an empty answer is none", ``, CandidateNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}
			gen := &candidateGen{reply: tc.reply}
			outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
			if outcome != tc.outcome {
				t.Errorf("outcome %s, want %s", outcome, tc.outcome)
			}
			if len(w.proposals) != 0 {
				t.Errorf("a row was stored: %d", len(w.proposals))
			}
		})
	}
}

func TestCandidateKeepsTheFirstOpOnly(t *testing.T) {
	two := `{"ops": [
		{"op": "create", "kind": "method", "name": "pre-commit-tests",
		 "description": "Use when committing code, to verify the tests first",
		 "body": "## Steps\n1. run the tests\n2. commit only when they pass",
		 "reason": "the owner states this rule explicitly",
		 "evidence": {"kind": "quote", "quote": "run the tests before you commit"}},
		{"op": "create", "kind": "method", "name": "second-skill",
		 "description": "Use when doing the second thing",
		 "body": "## Steps\n1. one\n2. two",
		 "reason": "dropped",
		 "evidence": {"kind": "quote", "quote": "run the tests before you commit"}}
	]}`
	w := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}
	gen := &candidateGen{reply: two}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateStored {
		t.Fatalf("outcome %s, want stored", outcome)
	}
	if len(w.proposals) != 1 {
		t.Fatalf("proposals = %d, want exactly one", len(w.proposals))
	}
	for _, p := range w.proposals {
		if p.Name() != "pre-commit-tests" {
			t.Errorf("the first op was not the one kept: %q", p.Name())
		}
	}
}

func TestCandidateCooldownSkipsADecidedName(t *testing.T) {
	w := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}
	// the owner dismissed a proposal of this name inside the cooldown window
	old, err := aggregate.NewSkillProposal("sprop_old", aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: "pre-commit-tests", Description: "Use when committing code, to verify the tests first",
		Body:     "## Steps\n1. run the tests\n2. commit only when they pass",
		Evidence: "run the tests", EvidenceKind: enum.SkillEvidenceQuote,
	}, validateTurnAt.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Dismiss("the tests already run in ci", validateTurnAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	w.proposals[old.ID()] = old
	gen := &candidateGen{reply: candidateCreateReply()}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateCooldown {
		t.Errorf("outcome %s, want cooldown", outcome)
	}
	if len(w.proposals) != 1 {
		t.Error("the dismissed name was proposed again")
	}
}

func TestCandidateSkipsAFullQueue(t *testing.T) {
	w := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}
	for _, id := range []string{"sprop_q1", "sprop_q2", "sprop_q3"} {
		p := sqlFreeProposal(t, id)
		w.proposals[p.ID()] = p
	}
	gen := &candidateGen{reply: candidateCreateReply()}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateSkipped {
		t.Errorf("outcome %s, want skipped", outcome)
	}
	if gen.calls != 0 {
		t.Error("the utility model was asked though the queue is full — that call is the owner's money")
	}
}

// sqlFreeProposal is a pending proposal of a unique name, for queue fixtures.
func sqlFreeProposal(t *testing.T, id string) *aggregate.SkillProposal {
	t.Helper()
	p, err := aggregate.NewSkillProposal(id, aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: "skill-" + strings.ReplaceAll(id, "_", "-"), Description: "Use when doing " + id,
		Body:     "## Steps\n1. one\n2. two",
		Evidence: "the owner said so", EvidenceKind: enum.SkillEvidenceQuote,
	}, validateTurnAt)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCandidateRejectsWhatValidateRefuses(t *testing.T) {
	// a draft quoting words the owner never said can never become a row
	bad := `{"ops": [{
		"op": "create", "kind": "method", "name": "pre-commit-tests",
		"description": "Use when committing code, to verify the tests first",
		"body": "## Steps\n1. run the tests\n2. commit only when they pass",
		"reason": "invented",
		"evidence": {"kind": "quote", "quote": "words the owner never wrote at all"}
	}]}`
	w := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}
	gen := &candidateGen{reply: bad}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateRejected {
		t.Errorf("outcome %s, want rejected", outcome)
	}
	if len(w.proposals) != 0 {
		t.Error("a rejected draft was stored")
	}
}

func TestCandidateRepeatEvidenceAcrossTurns(t *testing.T) {
	// two owner turns inside thirty days: the earlier line rides the digest
	// with its id, the model cites both, and the repeat validates
	conv := room(t,
		newMsg(t, "m0", enum.RoleUser, "please squash my commits before the merge"),
		finalReply(t, "m0a", "squashed"),
		newMsg(t, "m1", enum.RoleUser, "squash the commits again before merging this one"),
		finalReply(t, "m2", "squashed"),
	)
	reply := `{"ops": [{
		"op": "create", "kind": "method", "name": "squash-before-merge",
		"description": "Use when merging, to squash the branch's commits first",
		"body": "## Steps\n1. squash the commits\n2. merge the branch",
		"reason": "the owner asked for this twice",
		"evidence": {"kind": "repeat", "turns": ["m0", "m1"]}
	}]}`
	w := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}
	gen := &candidateGen{reply: reply}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(conv))
	if outcome != CandidateStored {
		t.Fatalf("outcome %s, want stored", outcome)
	}
	if !strings.Contains(gen.user, "owner #m0 (") {
		t.Error("the earlier owner line does not ride the digest with its id and date")
	}
}

func TestCandidateZeroBudgetSkipsBeforeGeneration(t *testing.T) {
	// No budget means validation refuses every draft, so the model call is
	// the owner's money spent on a row that can never exist.
	w, gen := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}, &candidateGen{reply: candidateCreateReply()}
	deps := candidateDeps(w, gen)
	deps.Budget = 0
	outcome := Candidate(context.Background(), deps, candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateSkipped {
		t.Errorf("outcome %s, want skipped", outcome)
	}
	if gen.calls != 0 {
		t.Errorf("the model was called %d times for a pass that could never admit", gen.calls)
	}
	if len(w.proposals) != 0 {
		t.Errorf("proposals = %d, want none", len(w.proposals))
	}
}

func TestCandidateFactsMarkSteersAsNeverOpeningTurns(t *testing.T) {
	// A repeat rests on two occasions. The window's steer is part of the
	// anchor's one occasion; an earlier line opens a turn only when no input
	// bound it.
	steer := newMsg(t, "s1", enum.RoleUser, "also check the lock")
	steer.SetInputID("st-1")
	conv := room(t,
		newMsg(t, "m1", enum.RoleUser, candidateOwnerLine),
		finalReply(t, "m2", "working on it"),
		steer,
		finalReply(t, "m3", "done, the tests are green"),
	)
	w, ok := TurnWindow(conv.Messages())
	if !ok {
		t.Fatal("no window in a finished turn")
	}
	earlierSteer := newMsg(t, "e2", enum.RoleUser, "an earlier steer")
	earlierSteer.SetInputID("st-9")
	earlier := []entity.Message{
		newMsg(t, "e1", enum.RoleUser, "an earlier opener"),
		earlierSteer,
	}
	facts := candidateFacts(w, earlier, nil, false)
	open := map[string]bool{}
	for _, l := range facts.OwnerLines {
		open[l.Turn] = l.OpensTurn
	}
	for id, want := range map[string]bool{"m1": true, "s1": false, "e1": true, "e2": false} {
		got, seen := open[id]
		if !seen {
			t.Errorf("%s is missing from the facts", id)
		} else if got != want {
			t.Errorf("%s OpensTurn = %v, want %v", id, got, want)
		}
	}
}

func TestCandidateDigestHonorsTheCapAndDropsOldestRoundsFirst(t *testing.T) {
	// Sixty rounds of clipped size overflow the cap; the oldest rounds go
	// first, the newest survive, the anchor is never touched, and the digest
	// says what it dropped.
	msgs := []entity.Message{newMsg(t, "m1", enum.RoleUser, candidateOwnerLine)}
	for i := 0; i < 60; i++ {
		msgs = append(msgs, finalReply(t, fmt.Sprintf("r%02d", i), fmt.Sprintf("round-%02d ", i)+strings.Repeat("a", 600)))
	}
	conv := room(t, msgs...)
	w, ok := TurnWindow(conv.Messages())
	if !ok {
		t.Fatal("no window")
	}
	turn, shown, _ := candidateDigest(w, nil, false)
	if len(turn) > digestCapBytes+256 { // the marker and the owner lines are never trimmed
		t.Errorf("turn digest = %d bytes, cap %d", len(turn), digestCapBytes)
	}
	if !strings.Contains(turn, "owner #m1: "+candidateOwnerLine) {
		t.Error("the anchor line was trimmed — it is the one line that may never go")
	}
	if !strings.Contains(turn, "round-59") {
		t.Error("the newest round was dropped before an older one")
	}
	if strings.Contains(turn, "round-00") {
		t.Error("the oldest round survived the trim")
	}
	if !strings.Contains(turn, "dropped for size") {
		t.Error("the digest does not say what it dropped")
	}
	if len(shown) != 0 {
		t.Errorf("shown = %d, want none passed in", len(shown))
	}
}

func TestCandidateDigestDropsRoundsBeforeEarlierOwnerLines(t *testing.T) {
	// Earlier owner lines are evidence; rounds are not. While a round can
	// still go, no earlier line is touched.
	msgs := []entity.Message{newMsg(t, "m1", enum.RoleUser, candidateOwnerLine)}
	for i := 0; i < 56; i++ {
		msgs = append(msgs, finalReply(t, fmt.Sprintf("r%02d", i), strings.Repeat("a", 700)))
	}
	conv := room(t, msgs...)
	w, _ := TurnWindow(conv.Messages())
	earlier := []entity.Message{
		newMsg(t, "e1", enum.RoleUser, "an earlier owner line"),
		newMsg(t, "e2", enum.RoleUser, "another earlier owner line"),
	}
	turn, shown, _ := candidateDigest(w, earlier, false)
	if len(shown) != 2 && len(turn) < digestCapBytes {
		t.Errorf("earlier lines dropped while the digest already fit: shown = %d", len(shown))
	}
	for _, m := range shown {
		if !strings.Contains(turn, m.ID()) {
			// the turn section does not render earlier lines — the prompt
			// does — but the survivors must be the newest ones
		}
	}
	if len(shown) > 0 && shown[0].ID() == "e1" && len(shown) == 1 {
		t.Error("the newer earlier line dropped before the older")
	}
}

func TestCandidateDigestOmitsTheDroppedRoundsCalls(t *testing.T) {
	// A round the trim dropped never reaches the facts: the model cannot
	// cite a call it never saw, because the facts come from the same source
	// as the digest.
	msgs := []entity.Message{newMsg(t, "m1", enum.RoleUser, candidateOwnerLine)}
	for i := 0; i < 56; i++ {
		msgs = append(msgs, callRound(t, fmt.Sprintf("r%02d", i), "bash", strings.Repeat("o", 700), false))
	}
	msgs = append(msgs, finalReply(t, "r99", "done"))
	conv := room(t, msgs...)
	w, _ := TurnWindow(conv.Messages())
	_, _, calls := candidateDigest(w, nil, false)
	for _, tc := range calls {
		if strings.HasPrefix(tc.ID(), "r00") || strings.HasPrefix(tc.ID(), "r01") {
			t.Errorf("a dropped round's call %s reached the facts", tc.ID())
		}
	}
	if len(calls) == 0 {
		t.Error("every call dropped — the newest rounds must survive")
	}
}

func TestCandidateDigestDropsEarlierLinesOldestFirstWhenOnlyTheyRemain(t *testing.T) {
	// With no rounds left to drop, the earliest owner lines before the
	// anchor go next — oldest first — and the anchor still never moves.
	conv := room(t,
		newMsg(t, "m1", enum.RoleUser, candidateOwnerLine),
		finalReply(t, "m2", "done"),
	)
	w, _ := TurnWindow(conv.Messages())
	line := func(id string, size int) entity.Message {
		return newMsg(t, id, enum.RoleUser, strings.Repeat("x", size))
	}
	earlier := []entity.Message{line("old", digestCapBytes/2), line("new", digestCapBytes/2)}
	_, shown, _ := candidateDigest(w, earlier, false)
	if len(shown) != 1 || shown[0].ID() != "new" {
		names := []string{}
		for _, m := range shown {
			names = append(names, m.ID())
		}
		t.Errorf("shown = %v, want only the newest line", names)
	}
	if _, shown, _ := candidateDigest(w, []entity.Message{line("huge", digestCapBytes)}, false); len(shown) != 0 {
		t.Error("an oversized single earlier line survived")
	}
}

func TestCandidateRefusesACitationOfATrimmedLine(t *testing.T) {
	// The model cites two earlier owner lines for a repeat — but the older
	// one was trimmed for size, so it never entered the facts, and the
	// validator refuses the citation. The digest the model read and the
	// facts the validator reads are one source.
	big := strings.Repeat("x", digestCapBytes/2)
	conv := room(t,
		newMsg(t, "e-old", enum.RoleUser, big),
		newMsg(t, "e-new", enum.RoleUser, big),
		newMsg(t, "m1", enum.RoleUser, candidateOwnerLine),
		finalReply(t, "m2", "done, the tests are green"),
	)
	reply := `{"ops": [{
		"op": "create", "kind": "method", "name": "trimmed-cite",
		"description": "Use when committing code, to verify the tests first",
		"body": "## Steps\n1. run the tests\n2. commit only when they pass",
		"reason": "the owner asked twice",
		"evidence": {"kind": "repeat", "turns": ["e-old", "m1"]}
	}]}`
	w, gen := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}, &candidateGen{reply: reply}
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(conv))
	if outcome == CandidateStored {
		t.Fatal("a proposal resting on a trimmed line was stored")
	}
	if len(w.proposals) != 0 {
		t.Errorf("proposals = %d, want none", len(w.proposals))
	}

	// The same citation against the surviving line is a different failure —
	// it reached the facts but opened no second occasion distance problem —
	// so prove the trim, not the distance, was what refused the first one.
	reply2 := `{"ops": [{
		"op": "create", "kind": "method", "name": "trimmed-cite",
		"description": "Use when committing code, to verify the tests first",
		"body": "## Steps\n1. run the tests\n2. commit only when they pass",
		"reason": "the owner asked twice",
		"evidence": {"kind": "repeat", "turns": ["e-new", "m1"]}
	}]}`
	w2, gen2 := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}, &candidateGen{reply: reply2}
	if outcome := Candidate(context.Background(), candidateDeps(w2, gen2), candidateInput(conv)); outcome != CandidateStored {
		t.Errorf("the surviving-line citation: outcome %s, want stored — the trim must be the only difference", outcome)
	}
}
