package skill

import (
	"context"
	"fmt"
	"sort"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// ListDeps are the three reads the Skills page joins. Each port is called once
// for the whole page — the join lives in memory, and a tree of two hundred
// skills is still three reads, never one per row.
type ListDeps struct {
	Catalog   repository.Skills
	Ownership repository.SkillOwnership
	Proposals repository.SkillProposals
}

// SkillRow is one line of the Skills page.
type SkillRow struct {
	Name        string
	Description string
	Source      string
	Path        string
	// Digest is the digest of the whole file on disk — the bytes the page is
	// showing the owner, and the ones MoveLevel compares a move against.
	Digest string
	// Level is who may change the skill now, user or agent: the row's level
	// while the row describes the file on disk, user otherwise (see
	// aggregate.SkillStandingOf). A skill with no ownership row is the
	// owner's, and a row never speaks for an entry of another root: a
	// builtin, global or workspace file is the owner's whatever a doctored
	// or stale row says, because the loop cannot write it there.
	Level string
	// Tracked reports that an ownership row exists for the skill.
	Tracked bool
	// Changed reports that the file was edited outside the loop since its
	// row was written, so the row's level no longer applies.
	Changed bool
	// Pending counts the proposals waiting on the owner for this skill.
	Pending int
	// PendingStale counts those of Pending the owner can no longer accept — the
	// file or the row moved on under them. They stay listed, labelled, and only
	// stop holding the queue; the owner decides them like any other.
	PendingStale int
}

// SkillPage is the whole page: every live catalog row joined with its level
// and pending counts, sorted by name, plus the catalog's own diagnostics, one
// for each ownership row whose file is gone, and the total of proposals waiting
// on the owner, whatever skill they name, and how many of those are stale.
type SkillPage struct {
	Rows         []SkillRow
	Diagnostics  []string
	Pending      int
	PendingStale int
}

// List joins the page. A failed read fails the page: a half-joined list would
// show levels or counts the owner would act on as if they were true.
func List(ctx context.Context, deps ListDeps, workspace string) (SkillPage, error) {
	catalog, err := deps.Catalog.List(ctx, workspace)
	if err != nil {
		return SkillPage{}, err
	}
	rows, err := deps.Ownership.List(ctx)
	if err != nil {
		return SkillPage{}, err
	}
	state := newLearnedState(catalog.Skills, rows)
	pending, err := deps.Proposals.Pending(ctx)
	if err != nil {
		return SkillPage{}, err
	}
	counts := make(map[string]int, len(pending))
	staleCounts := make(map[string]int, len(pending))
	page := SkillPage{Rows: make([]SkillRow, 0, len(catalog.Skills)), Diagnostics: catalog.Diagnostics, Pending: len(pending)}
	for _, p := range pending {
		// A patch proposal carries its target's name — the shape rules keep
		// a patch's name and target equal — so every op counts for the same
		// skill it would be decided on.
		counts[p.Name()]++
		// The same open rule the queue gate counts by, so a label and the
		// gate can never disagree about what the owner can still accept.
		if !state.open(p) {
			staleCounts[p.Name()]++
			page.PendingStale++
		}
	}
	for _, s := range catalog.Skills {
		standing := aggregate.SkillStanding{Level: enum.SkillLevelUser}
		if s.Source == string(enum.SkillRootNusashell) {
			standing = aggregate.SkillStandingOf(state.rows[s.Name], s.Digest)
		}
		page.Rows = append(page.Rows, SkillRow{
			Name:         s.Name,
			Description:  s.Description,
			Source:       s.Source,
			Path:         s.Path,
			Digest:       s.Digest,
			Level:        string(standing.Level),
			Tracked:      standing.Tracked,
			Changed:      standing.Changed,
			Pending:      counts[s.Name],
			PendingStale: staleCounts[s.Name],
		})
	}
	// A row whose file is gone is listed nowhere else, and the owner could not
	// see that the loop still holds the name. Only a name no skill carries
	// counts: a builtin, global or workspace file of the same name is the live
	// skill, and the validator refuses that name either way.
	carried := make(map[string]bool, len(catalog.Skills))
	for _, s := range catalog.Skills {
		carried[s.Name] = true
	}
	var orphans []string
	for _, name := range learnedNames(rows) {
		if !carried[name] {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		page.Diagnostics = append(page.Diagnostics, fmt.Sprintf("the name %q is still owned by the loop, but its file is gone", name))
	}
	// The catalog port does not promise an order; the page and its tests get
	// one anyway.
	sort.Slice(page.Rows, func(i, j int) bool { return page.Rows[i].Name < page.Rows[j].Name })
	return page, nil
}
