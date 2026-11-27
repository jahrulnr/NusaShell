package skill

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// ListDeps are the reads the Skills page joins. The catalog, the ownership
// rows, and the pending proposals are each read once — the join lives in
// memory. Folders, when set, is read once beside them so a name live in
// another workspace is stale here too. The catalog read covers every
// workspace root without repeating the fixed roots.
type ListDeps struct {
	Catalog   repository.Skills
	Ownership repository.SkillOwnership
	Proposals repository.SkillProposals
	Folders   SkillFolders
	// Writer renders a pending create's document, so a leftover of the loop's
	// own write reads as open. Nil means no file is read that way.
	Writer repository.SkillWriter
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
// for each ownership row whose file no longer reads as a skill, and the total of proposals waiting
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
	catalog, taken, err := catalogView(ctx, deps.Catalog, deps.Folders, workspace)
	if err != nil {
		return SkillPage{}, err
	}
	rows, err := deps.Ownership.List(ctx)
	if err != nil {
		return SkillPage{}, err
	}
	pending, err := deps.Proposals.Pending(ctx)
	if err != nil {
		return SkillPage{}, err
	}
	state, err := newLearnedState(catalog.Skills, rows, taken).withOwnDocs(deps.Writer, pending)
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
	// A row whose file no longer reads as a skill is listed nowhere else, and
	// the owner could not see that the loop still holds the name. Only a name
	// no skill carries counts: a builtin, global, or workspace file of the
	// same name, including one in another workspace folder, is the live
	// skill, and the validator refuses that name either way.
	var orphans []string
	for _, name := range learnedNames(rows) {
		if _, carried := taken[name]; !carried {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		page.Diagnostics = append(page.Diagnostics, fmt.Sprintf("the name %q is still owned by the loop, but its file no longer reads as a skill", name))
	}
	// The catalog port does not promise an order; the page and its tests get
	// one anyway.
	sort.Slice(page.Rows, func(i, j int) bool { return page.Rows[i].Name < page.Rows[j].Name })
	return page, nil
}

// PendingProposal is one proposal still waiting on the owner. Open is
// learnedState.open, the same function the page's stale count uses, so the
// queue and the page cannot disagree about what the owner can still accept.
type PendingProposal struct {
	ID          string
	Op          string
	Kind        string
	Name        string
	Description string
	Target      string
	Tool        string
	Reason      string
	// EvidenceKind and Evidence are what the proposal rests on, by kind: the
	// owner's words, a call's id, two turns' ids, or the owner's rewrite.
	EvidenceKind string
	Evidence     string
	// EvidenceAt is when that evidence happened; zero when the turn carried no time.
	EvidenceAt time.Time
	CreatedAt  time.Time
	Open       bool
	// StaleCause is why the proposal is not open, in the owner's terms: the same
	// sentence the store keeps when the pass retires one. Empty while open.
	StaleCause string
}

// learnedNow reads the catalog and the ownership rows once and joins them the way
// the page and the queue do, so a decision is judged by the rule the owner sees.
// pending is the proposals the caller is judging; each create among them is
// checked for the loop's own leftover.
func learnedNow(ctx context.Context, skills repository.Skills, folders SkillFolders, owners repository.SkillOwnership, writer repository.SkillWriter, workspace string, pending []*aggregate.SkillProposal) (learnedState, error) {
	catalog, taken, err := catalogView(ctx, skills, folders, workspace)
	if err != nil {
		return learnedState{}, err
	}
	rows, err := owners.List(ctx)
	if err != nil {
		return learnedState{}, err
	}
	return newLearnedState(catalog.Skills, rows, taken).withOwnDocs(writer, pending)
}

// ListPending returns the pending proposals in the store's order. Open is
// false when accept would refuse the proposal because of the files or the
// rows. The catalog budget is left out: it is the owner's to make room for,
// not a fact about the proposal.
func ListPending(ctx context.Context, deps ListDeps, workspace string) ([]PendingProposal, error) {
	pending, err := deps.Proposals.Pending(ctx)
	if err != nil {
		return nil, err
	}
	state, err := learnedNow(ctx, deps.Catalog, deps.Folders, deps.Ownership, deps.Writer, workspace, pending)
	if err != nil {
		return nil, err
	}
	out := make([]PendingProposal, 0, len(pending))
	for _, p := range pending {
		open := state.open(p)
		var cause string
		if !open {
			cause = state.supersedeCause(p)
		}
		out = append(out, PendingProposal{
			ID:           p.ID(),
			Op:           string(p.Op()),
			Kind:         string(p.Kind()),
			Name:         p.Name(),
			Description:  p.Description(),
			Target:       p.TargetName(),
			Tool:         p.Tool(),
			Reason:       p.Reason(),
			EvidenceKind: string(p.EvidenceKind()),
			Evidence:     p.Evidence(),
			EvidenceAt:   p.EvidenceAt(),
			CreatedAt:    p.CreatedAt(),
			Open:         open,
			StaleCause:   cause,
		})
	}
	return out, nil
}
