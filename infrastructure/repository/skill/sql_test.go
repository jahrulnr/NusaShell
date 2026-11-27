package reposkill

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// openSQL opens a real database against the real migrations: the contract the
// fake pins is behavioral, but only these tests prove the rows actually round
// trip — a fake has no columns, no constraints, and no transactions.
func openSQL(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "n.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var sqlNow = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func sqlProposal(t *testing.T, id, name string) *aggregate.SkillProposal {
	t.Helper()
	p, err := aggregate.NewSkillProposal(id, aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: name, Description: "Use when writing " + name,
		Body:         "## Steps\n1. read the log\n2. group by area",
		Evidence:     "write release notes like the last one",
		EvidenceKind: enum.SkillEvidenceQuote,
	}, sqlNow)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	return p
}

func sqlPatchProposal(t *testing.T, id, name, digest string) *aggregate.SkillProposal {
	t.Helper()
	p, err := aggregate.NewSkillProposal(id, aggregate.SkillProposalInput{
		Op: enum.SkillProposalPatch, Kind: enum.SkillKindMethod,
		Name: name, Description: "Use when writing " + name,
		Body:         "## Steps\n1. read the log\n2. group by area and week",
		Evidence:     "write release notes like the last one",
		EvidenceKind: enum.SkillEvidenceQuote,
		TargetName:   name, TargetDigest: digest, TargetLevel: enum.SkillLevelAgent,
	}, sqlNow)
	if err != nil {
		t.Fatalf("patch proposal: %v", err)
	}
	return p
}

func sqlStatus(err error) int {
	if err == nil {
		return 0
	}
	return apperror.As(err).StatusCode()
}

func TestSQLProposalsRoundTrip(t *testing.T) {
	db := openSQL(t)
	props := NewProposals(db)
	ctx := context.Background()
	p := sqlProposal(t, "sprop_rt", "release-notes")
	if err := props.Add(ctx, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := props.Get(ctx, p.ID())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name() != p.Name() || got.Body() != p.Body() || got.Evidence() != p.Evidence() || got.Status() != enum.SkillProposalPending {
		t.Errorf("round trip drifted: %q %q", got.Name(), got.Status())
	}
	if !got.CreatedAt().Equal(sqlNow) {
		t.Errorf("created_at %v, want %v", got.CreatedAt(), sqlNow)
	}
	// a tool-kind proposal round-trips its tool — the column the plan's list
	// forgot, and hydration cannot live without
	tool, err := aggregate.NewSkillProposal("sprop_tool", aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindTool, Tool: "fetch_page",
		Name: "fetch-pages", Description: "Use when fetching pages",
		Body:     "## Steps\n1. call fetch_page\n2. quote the answer",
		Evidence: "fetch_page", EvidenceKind: enum.SkillEvidenceCall,
	}, sqlNow)
	if err != nil {
		t.Fatalf("tool proposal: %v", err)
	}
	if err := props.Add(ctx, tool); err != nil {
		t.Fatalf("add tool: %v", err)
	}
	gotTool, err := props.Get(ctx, tool.ID())
	if err != nil {
		t.Fatalf("get tool: %v", err)
	}
	if gotTool.Tool() != "fetch_page" || gotTool.Kind() != enum.SkillKindTool {
		t.Errorf("the tool did not round-trip: %q %s", gotTool.Tool(), gotTool.Kind())
	}
	// a patch round-trips its target fields — under its own name, because one
	// pending per name is the invariant the database enforces
	patch := sqlPatchProposal(t, "sprop_pt", "patch-target", "d1")
	if err := props.Add(ctx, patch); err != nil {
		t.Fatalf("add patch: %v", err)
	}
	gotPatch, err := props.Get(ctx, patch.ID())
	if err != nil {
		t.Fatalf("get patch: %v", err)
	}
	if gotPatch.TargetName() != "patch-target" || gotPatch.TargetDigest() != "d1" || gotPatch.TargetLevel() != enum.SkillLevelAgent {
		t.Errorf("the target fields did not round-trip: %q %q %s", gotPatch.TargetName(), gotPatch.TargetDigest(), gotPatch.TargetLevel())
	}
	// duplicate id is a 409, a decided add a 400, a missing get a 404
	if err := props.Add(ctx, p); sqlStatus(err) != 409 {
		t.Errorf("duplicate add: %v; want 409", err)
	}
	decided := sqlProposal(t, "sprop_dc", "other-skill")
	if err := decided.Dismiss("too narrow", sqlNow, true); err != nil {
		t.Fatal(err)
	}
	if err := props.Add(ctx, decided); sqlStatus(err) != 400 {
		t.Errorf("adding a decided proposal: %v; want 400", err)
	}
	if _, err := props.Get(ctx, "sprop_missing"); sqlStatus(err) != 404 {
		t.Errorf("missing get: %v; want 404", err)
	}
}

func TestSQLProposalsPendingAndSeen(t *testing.T) {
	db := openSQL(t)
	props := NewProposals(db)
	ctx := context.Background()
	pending := sqlProposal(t, "sprop_p1", "release-notes")
	if err := props.Add(ctx, pending); err != nil {
		t.Fatal(err)
	}
	dismissed := sqlProposal(t, "sprop_p2", "deploy-day")
	if err := props.Add(ctx, dismissed); err != nil {
		t.Fatal(err)
	}
	if err := dismissed.Dismiss("too narrow", sqlNow.Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE skill_proposals SET status = 'dismissed', dismiss_reason = 'too narrow', decided_at = ?, decided_open = 1 WHERE id = ?`,
		ms(sqlNow.Add(time.Hour)), dismissed.ID()); err != nil {
		t.Fatal(err)
	}
	list, err := props.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID() != pending.ID() {
		t.Errorf("pending = %d rows, want the one pending proposal", len(list))
	}
	// the cooldown: pending counts, recently decided counts, old decisions do not
	if seen, _ := props.Seen(ctx, "release-notes", enum.SkillProposalCreate, sqlNow.Add(2*time.Hour)); !seen {
		t.Error("a pending proposal was not seen")
	}
	if seen, _ := props.Seen(ctx, "deploy-day", enum.SkillProposalCreate, sqlNow.Add(30*time.Minute)); !seen {
		t.Error("a recently dismissed proposal was not seen")
	}
	if seen, _ := props.Seen(ctx, "deploy-day", enum.SkillProposalCreate, sqlNow.Add(2*time.Hour)); seen {
		t.Error("a proposal dismissed before the cutoff was seen")
	}
	if seen, _ := props.Seen(ctx, "never-proposed", enum.SkillProposalCreate, sqlNow); seen {
		t.Error("a name never proposed was seen")
	}
}

// The prompt's refusals are the owner's dismissals that were still open when
// decided, inside the thirty-day window, newest first, at most the limit. A
// retirement, a stale close, a pending proposal, and an older decision are not
// refusals, so none of them reach the prompt.
func TestSQLDeclinedIsTheWindowOfOwnerRefusals(t *testing.T) {
	db := openSQL(t)
	props, decisions := NewProposals(db), NewDecisions(db)
	ctx := context.Background()
	add := func(id, name string) *aggregate.SkillProposal {
		t.Helper()
		p := sqlProposal(t, id, name)
		if err := props.Add(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	refuse := func(id, name string, at time.Time, open bool, reason string) {
		t.Helper()
		p := add(id, name)
		if err := p.Dismiss(reason, at, open); err != nil {
			t.Fatal(err)
		}
		if err := decisions.Commit(ctx, p, nil); err != nil {
			t.Fatal(err)
		}
	}
	const window = 30 * 24 * time.Hour
	refuse("sprop_new", "new-refusal", sqlNow, true, "too narrow")
	refuse("sprop_w1", "window-1", sqlNow.Add(-1*time.Hour), true, "a refusal")
	refuse("sprop_w2", "window-2", sqlNow.Add(-2*time.Hour), true, "a refusal")
	refuse("sprop_w3", "window-3", sqlNow.Add(-3*time.Hour), true, "a refusal")
	refuse("sprop_w4", "window-4", sqlNow.Add(-4*time.Hour), true, "a refusal")
	refuse("sprop_w5", "window-5", sqlNow.Add(-5*time.Hour), true, "a refusal")
	refuse("sprop_w6", "window-6", sqlNow.Add(-6*time.Hour), true, "the oldest in the window")
	refuse("sprop_old", "old-refusal", sqlNow.Add(-window-time.Hour), true, "too old")
	refuse("sprop_stale", "stale-close", sqlNow, false, "closed after it went stale")
	retired := add("sprop_retired", "retired-name")
	if err := retired.Supersede("the name is live in the nusashell root since it was proposed", sqlNow); err != nil {
		t.Fatal(err)
	}
	if err := decisions.Commit(ctx, retired, nil); err != nil {
		t.Fatal(err)
	}
	// Supersede always records a closed decision, so the prefix clause is what
	// keeps a retirement out of the prompt on its own. Setting the flag by hand
	// shows the clause does the excluding, not the flag.
	if _, err := db.ExecContext(ctx, `UPDATE skill_proposals SET decided_open = 1 WHERE id = ?`, retired.ID()); err != nil {
		t.Fatal(err)
	}
	add("sprop_pending", "pending-name")
	ids := func(list []*aggregate.SkillProposal) string {
		out := make([]string, len(list))
		for i, p := range list {
			out[i] = p.ID()
		}
		return strings.Join(out, ",")
	}
	all, err := props.Declined(ctx, sqlNow.Add(-window), 100)
	if err != nil {
		t.Fatal(err)
	}
	const inWindow = "sprop_new,sprop_w1,sprop_w2,sprop_w3,sprop_w4,sprop_w5,sprop_w6"
	if got := ids(all); got != inWindow {
		t.Errorf("refusals in the window = %s, want %s", got, inWindow)
	}
	five, err := props.Declined(ctx, sqlNow.Add(-window), 5)
	if err != nil {
		t.Fatal(err)
	}
	const newestFive = "sprop_new,sprop_w1,sprop_w2,sprop_w3,sprop_w4"
	if got := ids(five); got != newestFive {
		t.Errorf("refusals at the limit of five = %s, want the newest five %s", got, newestFive)
	}
}

// A retirement the pass wrote is not the owner's decision, so the cooldown does
// not hold its name; the owner's own dismissal of the same op still does. A
// pending proposal holds its name whatever its op, as the one-pending index does.
func TestSQLSupersededHoldsNothing(t *testing.T) {
	db := openSQL(t)
	props, decisions := NewProposals(db), NewDecisions(db)
	ctx := context.Background()
	retired := sqlProposal(t, "sprop_r1", "release-notes")
	if err := props.Add(ctx, retired); err != nil {
		t.Fatal(err)
	}
	if err := retired.Supersede("the name was taken since it was proposed", sqlNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := decisions.Commit(ctx, retired, nil); err != nil {
		t.Fatal(err)
	}
	if seen, _ := props.Seen(ctx, "release-notes", enum.SkillProposalCreate, sqlNow); seen {
		t.Error("a retirement the pass wrote held its name")
	}

	owned := sqlProposal(t, "sprop_r2", "deploy-day")
	if err := props.Add(ctx, owned); err != nil {
		t.Fatal(err)
	}
	if err := owned.Dismiss("too narrow", sqlNow.Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if err := decisions.Commit(ctx, owned, nil); err != nil {
		t.Fatal(err)
	}
	if seen, _ := props.Seen(ctx, "deploy-day", enum.SkillProposalCreate, sqlNow); !seen {
		t.Error("the owner's dismissal did not hold its name")
	}

	patch, err := aggregate.NewSkillProposal("sprop_r3", aggregate.SkillProposalInput{
		Op: enum.SkillProposalPatch, Kind: enum.SkillKindMethod,
		Name: "pre-commit-tests", Description: "Use when committing code, to verify the tests first",
		Body: "## Steps\n1. read the log\n2. group by area", Reason: "the owner states this rule explicitly",
		Evidence: "run the tests before you commit", EvidenceKind: enum.SkillEvidenceQuote,
		TargetName: "pre-commit-tests", TargetDigest: "d1", TargetLevel: enum.SkillLevelAgent,
	}, sqlNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := props.Add(ctx, patch); err != nil {
		t.Fatal(err)
	}
	if seen, _ := props.Seen(ctx, "pre-commit-tests", enum.SkillProposalCreate, sqlNow); !seen {
		t.Error("a pending patch of the name did not hold it against a create")
	}
}

func TestSQLHydrationRefusesACorruptRow(t *testing.T) {
	// the owner asked for this test against the real SQL path: hydration may
	// only produce states Accept could produce, so a stored patch accepted at
	// the owner's level is a corrupt row, and reading it must fail — not
	// silently resurrect a write authority the rules never granted
	db := openSQL(t)
	props := NewProposals(db)
	ctx := context.Background()
	patch := sqlPatchProposal(t, "sprop_corrupt", "release-notes", "d1")
	if err := props.Add(ctx, patch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE skill_proposals
		SET status = 'accepted', accepted_path = '/x/SKILL.md', accepted_level = 'user', decided_at = ?
		WHERE id = ?`, ms(sqlNow), patch.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := props.Get(ctx, patch.ID()); err == nil {
		t.Error("a patch hydrated at the owner's level came back to life")
	}
	// and the pending listing never scans decided rows, so this corruption is
	// invisible to it; a pending row carrying a decision, however, must
	// surface loudly, not hand back a state Accept could never produce
	if _, err := db.ExecContext(ctx, `UPDATE skill_proposals SET status = 'pending', accepted_path = '', accepted_level = '', decided_at = 0 WHERE id = ?`, patch.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE skill_proposals SET accepted_level = 'user' WHERE id = ?`, patch.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := props.Pending(ctx); err == nil {
		t.Error("a pending row carrying a decision hydrated without complaint")
	}
}

func TestSQLOwnershipCompareAndSwap(t *testing.T) {
	db := openSQL(t)
	rows := NewOwnership(db)
	ctx := context.Background()
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d1")
	if err != nil {
		t.Fatal(err)
	}
	// insert only where no row exists
	if err := rows.Save(ctx, row, ""); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := rows.Save(ctx, row, ""); sqlStatus(err) != 409 {
		t.Errorf("a second unconditional insert: %v; want 409", err)
	}
	got, err := rows.Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Level() != enum.SkillLevelAgent || got.Digest() != "d1" {
		t.Errorf("row = %s/%s, want agent/d1", got.Level(), got.Digest())
	}
	// the flip: conditional update against the read digest
	flipped, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelUser, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if err := rows.Save(ctx, flipped, "stale"); sqlStatus(err) != 409 {
		t.Errorf("a stale conditional write: %v; want 409", err)
	}
	if err := rows.Save(ctx, flipped, "d1"); err != nil {
		t.Errorf("the conditional write against the read digest: %v", err)
	}
	// a conditional write against no row is a 404
	if err := rows.Save(ctx, flipped, "d1"); err != nil {
		t.Fatal(err)
	}
	if _, err := rows.Get(ctx, enum.SkillRootNusashell, "missing"); sqlStatus(err) != 404 {
		t.Errorf("missing row: %v; want 404", err)
	}
	ghost, _ := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "ghost", enum.SkillLevelUser, "d1")
	if err := rows.Save(ctx, ghost, "d1"); sqlStatus(err) != 404 {
		t.Errorf("conditional write against no row: %v; want 404", err)
	}
	// keyed by root and name: another root never answers for the learned one
	if _, err := rows.Get(ctx, enum.SkillRootWorkspace, "release-notes"); sqlStatus(err) != 404 {
		t.Errorf("the workspace root answered for a learned row: %v", err)
	}
}

func TestSQLOwnershipListHydratesEveryRow(t *testing.T) {
	db := openSQL(t)
	rows := NewOwnership(db)
	ctx := context.Background()
	seed := []struct{ name, level, digest string }{
		{"release-notes", "agent", "d1"},
		{"deploy-day", "user", "d2"},
	}
	for _, s := range seed {
		if _, err := db.ExecContext(ctx, `INSERT INTO skill_ownership (root, name, level, digest) VALUES ('nusashell', ?, ?, ?)`,
			s.name, s.level, s.digest); err != nil {
			t.Fatal(err)
		}
	}
	got, err := rows.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != len(seed) {
		t.Fatalf("list returned %d rows, want %d — the page's one read must carry every row", len(got), len(seed))
	}
	byName := map[string]*aggregate.SkillOwnership{}
	for _, row := range got {
		byName[row.Name()] = row
	}
	for _, s := range seed {
		row := byName[s.name]
		if row == nil {
			t.Fatalf("%s is missing from the list", s.name)
		}
		if row.Root() != enum.SkillRootNusashell || row.Level() != enum.SkillLevel(s.level) || row.Digest() != s.digest {
			t.Errorf("%s hydrated as %s/%s/%q, want nusashell/%s/%q", s.name, row.Root(), row.Level(), row.Digest(), s.level, s.digest)
		}
	}
	// A row keyed by another root is corruption, not a learned skill of that
	// name: hydration refuses it loudly instead of answering it.
	if _, err := db.ExecContext(ctx, `INSERT INTO skill_ownership (root, name, level, digest) VALUES ('workspace', 'release-notes', 'agent', 'd9')`); err != nil {
		t.Fatal(err)
	}
	if _, err := rows.List(ctx); err == nil {
		t.Error("a foreign-root row hydrated without complaint")
	}
}

func TestSQLOwnershipListRefusesACorruptLevel(t *testing.T) {
	db := openSQL(t)
	rows := NewOwnership(db)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO skill_ownership (root, name, level, digest) VALUES ('nusashell', 'release-notes', 'admin', 'd1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := rows.List(context.Background()); err == nil {
		t.Error("an unreadable level hydrated without complaint — the page must fail, not show a level that does not exist")
	}
}

func TestSQLCommitTransactional(t *testing.T) {
	db := openSQL(t)
	props, rows, decisions := NewProposals(db), NewOwnership(db), NewDecisions(db)
	ctx := context.Background()

	// accept-create: decision and row commit together
	p := sqlProposal(t, "sprop_c1", "release-notes")
	if err := props.Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := p.Accept("/skills/release-notes/SKILL.md", enum.SkillLevelAgent, sqlNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "dw")
	if err != nil {
		t.Fatal(err)
	}
	if err := decisions.Commit(ctx, p, row); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, _ := props.Get(ctx, p.ID())
	if got.Status() != enum.SkillProposalAccepted || got.AcceptedPath() != "/skills/release-notes/SKILL.md" {
		t.Errorf("the decision did not persist: %s", got.Status())
	}
	if _, err := rows.Get(ctx, enum.SkillRootNusashell, "release-notes"); err != nil {
		t.Errorf("the row did not commit: %v", err)
	}
	// a second commit of the same proposal is a 409
	if err := decisions.Commit(ctx, p, row); sqlStatus(err) != 409 {
		t.Errorf("re-commit: %v; want 409", err)
	}
	// a missing proposal is a 404
	ghost := sqlProposal(t, "sprop_ghost", "ghost-skill")
	if err := ghost.Dismiss("x", sqlNow, true); err != nil {
		t.Fatal(err)
	}
	if err := decisions.Commit(ctx, ghost, nil); sqlStatus(err) != 404 {
		t.Errorf("committing a missing proposal: %v; want 404", err)
	}
}

func TestSQLCommitCreateConflictLeavesNoDecision(t *testing.T) {
	db := openSQL(t)
	props, decisions := NewProposals(db), NewDecisions(db)
	ctx := context.Background()
	// a row already exists — another accept committed it, or the owner moved it in
	if _, err := db.ExecContext(ctx, `INSERT INTO skill_ownership (root, name, level, digest) VALUES ('nusashell', 'release-notes', 'agent', 'other')`); err != nil {
		t.Fatal(err)
	}
	p := sqlProposal(t, "sprop_c2", "release-notes")
	if err := props.Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := p.Accept("/x/SKILL.md", enum.SkillLevelAgent, sqlNow); err != nil {
		t.Fatal(err)
	}
	row, _ := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "dw")
	err := decisions.Commit(ctx, p, row)
	if sqlStatus(err) != 409 {
		t.Fatalf("create over an existing row: %v; want 409", err)
	}
	// the atomic promise: the decision did not stay behind
	got, _ := props.Get(ctx, p.ID())
	if got.Status() != enum.SkillProposalPending {
		t.Errorf("a refused commit left the decision behind: %s", got.Status())
	}
}

func TestSQLCommitPatchPreconditions(t *testing.T) {
	// each case runs against its own database: a refused commit leaves the
	// proposal pending, and one pending per name is the invariant — cases
	// sharing a database would collide on it instead of testing the commit
	setup := func(t *testing.T) (*sql.DB, *Proposals, *Decisions) {
		db := openSQL(t)
		if _, err := db.ExecContext(context.Background(), `INSERT INTO skill_ownership (root, name, level, digest) VALUES ('nusashell', 'release-notes', 'agent', 'd1')`); err != nil {
			t.Fatal(err)
		}
		return db, NewProposals(db), NewDecisions(db)
	}
	commit := func(t *testing.T, props *Proposals, decisions *Decisions, id, targetDigest string) error {
		p := sqlPatchProposal(t, id, "release-notes", targetDigest)
		if err := props.Add(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if err := p.Accept("/x/SKILL.md", enum.SkillLevelAgent, sqlNow); err != nil {
			t.Fatal(err)
		}
		row, _ := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "dw")
		return decisions.Commit(context.Background(), p, row)
	}
	t.Run("the digest the decision was read against moved", func(t *testing.T) {
		_, props, decisions := setup(t)
		if err := commit(t, props, decisions, "sprop_p1", "stale"); sqlStatus(err) != 409 {
			t.Errorf("patch against a moved row: %v; want 409", err)
		}
		got, _ := props.Get(context.Background(), "sprop_p1")
		if got.Status() != enum.SkillProposalPending {
			t.Errorf("a refused patch commit left the decision behind: %s", got.Status())
		}
	})
	t.Run("the row at the owner's level refuses even with the right digest", func(t *testing.T) {
		db, props, decisions := setup(t)
		if _, err := db.ExecContext(context.Background(), `UPDATE skill_ownership SET level = 'user' WHERE name = 'release-notes'`); err != nil {
			t.Fatal(err)
		}
		if err := commit(t, props, decisions, "sprop_p2", "d1"); sqlStatus(err) != 409 {
			t.Errorf("patch against the owner's row: %v; want 409", err)
		}
	})
	t.Run("the happy path moves the digest", func(t *testing.T) {
		db, props, decisions := setup(t)
		if err := commit(t, props, decisions, "sprop_p3", "d1"); err != nil {
			t.Errorf("the honest patch refused: %v", err)
		}
		var digest string
		if err := db.QueryRowContext(context.Background(), `SELECT digest FROM skill_ownership WHERE name = 'release-notes'`).Scan(&digest); err != nil {
			t.Fatal(err)
		}
		if digest != "dw" {
			t.Errorf("the row did not move to the written digest: %q", digest)
		}
	})
}

func TestSQLOnePendingPerName(t *testing.T) {
	db := openSQL(t)
	props := NewProposals(db)
	ctx := context.Background()
	if err := props.Add(ctx, sqlProposal(t, "sprop_u1", "release-notes")); err != nil {
		t.Fatal(err)
	}
	// a second pending row for the same name is the database's refusal, not
	// the caller's discipline
	err := props.Add(ctx, sqlProposal(t, "sprop_u2", "release-notes"))
	if sqlStatus(err) != 409 {
		t.Fatalf("a second pending proposal for one name: %v; want 409", err)
	}
	if err != nil && !strings.Contains(err.Error(), "pending") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	// once decided, the name is free again
	if _, err := db.ExecContext(ctx, `UPDATE skill_proposals SET status = 'dismissed', dismiss_reason = 'x', decided_at = 1 WHERE id = 'sprop_u1'`); err != nil {
		t.Fatal(err)
	}
	if err := props.Add(ctx, sqlProposal(t, "sprop_u3", "release-notes")); err != nil {
		t.Errorf("a proposal after the first was decided refused: %v", err)
	}
}

func TestSQLCommitDismissalAndValidation(t *testing.T) {
	db := openSQL(t)
	props, decisions := NewProposals(db), NewDecisions(db)
	ctx := context.Background()
	p := sqlProposal(t, "sprop_d1", "release-notes")
	if err := props.Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	// a pending proposal has no decision to commit
	row, _ := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "dw")
	if err := decisions.Commit(ctx, p, row); sqlStatus(err) != 400 {
		t.Errorf("committing a pending proposal: %v; want 400", err)
	}
	if err := p.Dismiss("too narrow", sqlNow, true); err != nil {
		t.Fatal(err)
	}
	// a dismissal carries no row
	if err := decisions.Commit(ctx, p, row); sqlStatus(err) != 400 {
		t.Errorf("a dismissal with a row: %v; want 400", err)
	}
	if err := decisions.Commit(ctx, p, nil); err != nil {
		t.Fatalf("the honest dismissal refused: %v", err)
	}
	got, _ := props.Get(ctx, p.ID())
	if got.Status() != enum.SkillProposalDismissed || got.DismissReason() != "too narrow" {
		t.Errorf("the dismissal did not persist: %s %q", got.Status(), got.DismissReason())
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_ownership`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a dismissal left a row behind: %d", n)
	}
}
