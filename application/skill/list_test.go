package skill

import (
	"context"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// The doubles count their reads: the join is a constant number of queries
// whatever the catalog holds, and the counts are what prove it — a join that
// went one-per-row would show up here as ownership calls tracking the number
// of skills.
type fixedCatalog struct {
	skills      []repository.Skill
	diagnostics []string
	calls       int
	// elsewhere names a skill live only in some other workspace folder.
	elsewhere map[string]string
}

func (f *fixedCatalog) List(ctx context.Context, workspace string) (repository.SkillCatalog, error) {
	f.calls++
	return repository.SkillCatalog{Skills: f.skills, Diagnostics: f.diagnostics}, nil
}
func (f *fixedCatalog) ListAcross(ctx context.Context, focus string, others []string) (repository.SkillCatalog, map[string]string, error) {
	cat, err := f.List(ctx, focus)
	if err != nil {
		return cat, nil, err
	}
	taken := map[string]string{}
	for _, s := range cat.Skills {
		if _, ok := taken[s.Name]; !ok {
			taken[s.Name] = s.Source
		}
	}
	for name, root := range f.elsewhere {
		if _, ok := taken[name]; !ok {
			taken[name] = root
		}
	}
	return cat, taken, nil
}
func (f *fixedCatalog) Read(ctx context.Context, workspace, name string) (repository.Skill, string, error) {
	return repository.Skill{}, "", nil
}

type countedOwnership struct {
	w     *decideWorld
	calls int
}

func (c *countedOwnership) Get(ctx context.Context, root enum.SkillRoot, name string) (*aggregate.SkillOwnership, error) {
	return decideOwnership{c.w}.Get(ctx, root, name)
}
func (c *countedOwnership) List(ctx context.Context) ([]*aggregate.SkillOwnership, error) {
	c.calls++
	return decideOwnership{c.w}.List(ctx)
}
func (c *countedOwnership) Save(ctx context.Context, o *aggregate.SkillOwnership, expectDigest string) error {
	return decideOwnership{c.w}.Save(ctx, o, expectDigest)
}

type countedProposals struct {
	w     *decideWorld
	calls int
}

func (c *countedProposals) Add(ctx context.Context, p *aggregate.SkillProposal) error {
	return decideProposals{c.w}.Add(ctx, p)
}
func (c *countedProposals) Get(ctx context.Context, id string) (*aggregate.SkillProposal, error) {
	return decideProposals{c.w}.Get(ctx, id)
}
func (c *countedProposals) Pending(ctx context.Context) ([]*aggregate.SkillProposal, error) {
	c.calls++
	return decideProposals{c.w}.Pending(ctx)
}
func (c *countedProposals) Seen(ctx context.Context, name string, op enum.SkillProposalOp, since time.Time) (bool, error) {
	return decideProposals{c.w}.Seen(ctx, name, op, since)
}
func (c *countedProposals) Declined(ctx context.Context, since time.Time, limit int) ([]*aggregate.SkillProposal, error) {
	return decideProposals{c.w}.Declined(ctx, since, limit)
}

func listSetup() (*decideWorld, *fixedCatalog, *countedOwnership, *countedProposals, ListDeps) {
	w := &decideWorld{
		proposals: map[string]*aggregate.SkillProposal{},
		rows:      map[string]*aggregate.SkillOwnership{},
		files:     map[string]string{},
	}
	catalog := &fixedCatalog{}
	ownership := &countedOwnership{w: w}
	proposals := &countedProposals{w: w}
	deps := ListDeps{Catalog: catalog, Ownership: ownership, Proposals: proposals}
	return w, catalog, ownership, proposals, deps
}

func listProposal(t *testing.T, id, name string) *aggregate.SkillProposal {
	t.Helper()
	p, err := aggregate.NewSkillProposal(id, aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: name, Description: "Use when writing " + name,
		Body:         "## Steps\n1. read the log\n2. group by area",
		Evidence:     "write release notes like the last one",
		EvidenceKind: enum.SkillEvidenceQuote,
	}, validateTurnAt)
	if err != nil {
		t.Fatalf("proposal %s: %v", id, err)
	}
	return p
}

func TestListJoinsLevelAndPendingInOnePass(t *testing.T) {
	w, catalog, ownership, proposals, deps := listSetup()
	catalog.skills = []repository.Skill{
		{Name: "release-notes", Description: "Use when writing release notes", Source: "nusashell", Path: "/n/release-notes/SKILL.md", Digest: "rd1"},
		{Name: "deploy-day", Description: "Use when deploying", Source: "nusashell", Path: "/n/deploy-day/SKILL.md", Digest: "dd1"},
		{Name: "coding-workflow", Description: "Use when changing code", Source: "builtin", Path: "/b/coding-workflow/SKILL.md", Digest: "bd1"},
	}
	catalog.diagnostics = []string{"/broken/root: unreadable"}
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "rd1")
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/release-notes"] = row
	// A row planted on a builtin's name must not speak for it: the loop
	// cannot write a builtin whatever the database says.
	doctored, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "coding-workflow", enum.SkillLevelAgent, "bd1")
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/coding-workflow"] = doctored
	w.proposals["p1"] = listProposal(t, "p1", "release-notes")
	w.proposals["p2"] = listProposal(t, "p2", "release-notes")
	w.proposals["p3"] = listProposal(t, "p3", "fresh-idea")

	page, err := List(context.Background(), deps, "/ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if catalog.calls != 1 || ownership.calls != 1 || proposals.calls != 1 {
		t.Fatalf("the join reads: catalog %d, ownership %d, proposals %d; want 1 each — one per row is the shape the page forbids",
			catalog.calls, ownership.calls, proposals.calls)
	}
	if len(page.Diagnostics) != 1 || page.Diagnostics[0] != "/broken/root: unreadable" {
		t.Errorf("diagnostics did not pass through: %v", page.Diagnostics)
	}
	if len(page.Rows) != 3 {
		t.Fatalf("rows = %d, want the 3 live skills", len(page.Rows))
	}
	if page.Rows[0].Name != "coding-workflow" || page.Rows[1].Name != "deploy-day" || page.Rows[2].Name != "release-notes" {
		t.Errorf("the page is not sorted by name: %s, %s, %s", page.Rows[0].Name, page.Rows[1].Name, page.Rows[2].Name)
	}
	byName := map[string]SkillRow{}
	for _, r := range page.Rows {
		byName[r.Name] = r
	}
	if r := byName["release-notes"]; r.Level != "agent" || r.Pending != 2 || r.Digest != "rd1" || r.Source != "nusashell" || r.Path != "/n/release-notes/SKILL.md" || r.Description != "Use when writing release notes" {
		t.Errorf("release-notes joined as %+v", r)
	}
	if r := byName["deploy-day"]; r.Level != "user" || r.Pending != 0 {
		t.Errorf("a learned skill with no row must show user and no pending: %+v", r)
	}
	if r := byName["coding-workflow"]; r.Level != "user" {
		t.Errorf("a doctored row made a builtin answer as agent: %+v", r)
	}
	if _, ok := byName["fresh-idea"]; ok {
		t.Error("a pending proposal for a name that is not live grew a page row")
	}
}

// An ownership row whose file no longer reads as a skill is no page row: the owner could not act on
// it there, so the page says in a diagnostic that the loop still holds the name.
// A row planted on a name a builtin carries is not one; the builtin is the skill.
func TestAnOrphanRowIsADiagnosticNotARow(t *testing.T) {
	w, catalog, _, _, deps := listSetup()
	catalog.skills = []repository.Skill{
		{Name: "release-notes", Description: "Use when writing release notes", Source: "nusashell", Path: "/n/release-notes/SKILL.md", Digest: "rd1"},
		{Name: "coding-workflow", Description: "Use when changing code", Source: "builtin", Path: "/b/coding-workflow/SKILL.md", Digest: "bd1"},
	}
	for _, name := range []string{"release-notes", "ghost-skill", "coding-workflow"} {
		row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, name, enum.SkillLevelAgent, "rd1")
		if err != nil {
			t.Fatal(err)
		}
		w.rows["nusashell/"+name] = row
	}
	page, err := List(context.Background(), deps, "/ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := `the name "ghost-skill" is still owned by the loop, but its file no longer reads as a skill`
	if len(page.Diagnostics) != 1 || page.Diagnostics[0] != want {
		t.Errorf("diagnostics = %q, want exactly %q", page.Diagnostics, want)
	}
	if len(page.Rows) != 2 {
		t.Errorf("rows = %d, want the two live skills and no row for the orphan", len(page.Rows))
	}
}

// A proposal the owner can no longer accept stays on the page, labelled: the row
// and the page count it as stale, and it stops holding the queue. The label and
// the gate read the one open rule, so they cannot disagree about it.
func TestPageLabelsAStaleProposalAndKeepsItListed(t *testing.T) {
	w, catalog, _, _, deps := listSetup()
	staleLearned(t, w, "old-skill")
	// A live patch: its row and the file on disk both read digest ld1.
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "live-skill", enum.SkillLevelAgent, "ld1")
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/live-skill"] = row
	w.catalog = append(w.catalog, repository.Skill{
		Name: "live-skill", Description: "Use when live-skill", Source: "nusashell",
		Path: "/n/live-skill/SKILL.md", Digest: "ld1",
	})
	w.proposals["live-patch"] = patchProposal(t, "live-patch", "live-skill", "ld1")
	openCreate(t, w, "fresh-idea")
	catalog.skills = w.catalog

	page, err := List(context.Background(), deps, "/ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Pending != 3 || page.PendingStale != 1 {
		t.Fatalf("page pending %d, stale %d; want 3 and 1: the stale patch is listed and labelled, not hidden", page.Pending, page.PendingStale)
	}
	byName := map[string]SkillRow{}
	for _, r := range page.Rows {
		byName[r.Name] = r
	}
	if r := byName["old-skill"]; r.Pending != 1 || r.PendingStale != 1 {
		t.Errorf("old-skill reads pending %d, stale %d; want 1 and 1", r.Pending, r.PendingStale)
	}
	if r := byName["live-skill"]; r.Pending != 1 || r.PendingStale != 0 {
		t.Errorf("live-skill reads pending %d, stale %d; want 1 and 0", r.Pending, r.PendingStale)
	}
	// The queue reads the same rule: the stale patch is not one the owner can
	// accept, so the two open proposals are the ones that would hold the queue.
	pending, err := decideProposals{w}.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	open, err := openProposals(context.Background(), candidateDeps(w, &candidateGen{}), "/ws", pending)
	if err != nil {
		t.Fatal(err)
	}
	if open != 2 {
		t.Fatalf("open = %d, want 2: the stale patch must not count as one the owner can accept", open)
	}
}

// A create whose name is live only in another workspace is not a row of this
// page — that folder's skill is not the skill here — and it is stale, because
// accept would refuse it.
func TestListLabelsACreateTakenOnlyInAnotherWorkspace(t *testing.T) {
	w, catalog, _, _, deps := listSetup()
	catalog.skills = []repository.Skill{{
		Name: "coding-workflow", Description: "Use when changing code", Source: "builtin",
		Path: "/b/coding-workflow/SKILL.md", Digest: "bd1",
	}}
	catalog.elsewhere = map[string]string{"release-notes": "workspace"}
	w.proposals["p"] = listProposal(t, "p", "release-notes")

	page, err := List(context.Background(), deps, "/ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Pending != 1 || page.PendingStale != 1 {
		t.Fatalf("pending %d, stale %d; want 1 and 1", page.Pending, page.PendingStale)
	}
	if len(page.Rows) != 1 || page.Rows[0].Name != "coding-workflow" {
		t.Fatalf("rows = %+v, want only this workspace's catalog", page.Rows)
	}
	if len(page.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %q, want none: the name is live in another workspace", page.Diagnostics)
	}
}

// An ownership row whose file is gone is not an orphan when another workspace
// folder still has a skill of that name. The file here is not the skill.
func TestANameLiveInAnotherWorkspaceIsNotAnOrphan(t *testing.T) {
	w, catalog, _, _, deps := listSetup()
	catalog.elsewhere = map[string]string{"release-notes": "workspace"}
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "rd1")
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/release-notes"] = row
	page, err := List(context.Background(), deps, "/ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %q, want none", page.Diagnostics)
	}
}

// The queue and the page read one open function. A name that is free is
// acceptable. A name live in this catalog, or only in another workspace, is
// not, and the row still comes back so the owner can dismiss it.
func TestListPendingUsesTheOpenRule(t *testing.T) {
	w, catalog, _, _, deps := listSetup()
	catalog.skills = []repository.Skill{{
		Name: "release-notes", Description: "Use when writing release notes", Source: "nusashell",
		Path: "/n/release-notes/SKILL.md", Digest: "rd1",
	}}
	catalog.elsewhere = map[string]string{"other-room": "workspace"}
	w.proposals["sprop_open"] = listProposal(t, "sprop_open", "fresh-idea")
	w.proposals["sprop_here"] = listProposal(t, "sprop_here", "release-notes")
	w.proposals["sprop_else"] = listProposal(t, "sprop_else", "other-room")

	got, err := ListPending(context.Background(), deps, "/ws")
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	open := map[string]bool{}
	for _, p := range got {
		open[p.ID] = p.Open
		if p.Op != "create" || p.Kind != "method" || p.Name == "" {
			t.Errorf("proposal %+v lost its op, kind, or name", p)
		}
	}
	if len(open) != 3 || !open["sprop_open"] || open["sprop_here"] || open["sprop_else"] {
		t.Fatalf("open = %v, want fresh-idea acceptable and the two taken names not", open)
	}
}
