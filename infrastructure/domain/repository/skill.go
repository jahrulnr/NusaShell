package repository

import (
	"context"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// Skill is the discovery projection of a SKILL.md frontmatter and its location.
// Digest covers the whole file, frontmatter included — it is what a patch is
// read against and what the writer's compare-and-swap compares.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	Digest      string `json:"digest"`
}

type SkillCatalog struct {
	Skills      []Skill  `json:"skills"`
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// Skills resolves the effective catalog for one workspace in root precedence:
// builtin, then nusashell — the learned skills — then global, then workspace,
// each overriding the same declared name of the roots before it. Read returns
// complete source, including frontmatter; supporting paths resolve from Path.
// Missing roots are empty; malformed entries remain diagnostics.
type Skills interface {
	List(ctx context.Context, workspace string) (SkillCatalog, error)
	// ListAcross reads the fixed roots — builtin, nusashell, global — once,
	// then each workspace root once. The catalog is the effective one for
	// focus: later fixed roots replace earlier ones, and only focus's own
	// workspace root is applied on top. Taken names every declared name and a
	// root that holds it; where nusashell and another root both hold a name,
	// taken names the other root, since nusashell is the one root the loop may
	// replace. A name live only in one of the other workspace
	// folders is taken as workspace and does not enter the focus catalog, so
	// a page for focus still shows focus's own skills. focus is always read,
	// even when others already contains it, and it is read once.
	ListAcross(ctx context.Context, focus string, others []string) (SkillCatalog, map[string]string, error)
	Read(ctx context.Context, workspace, name string) (Skill, string, error)
}

// SkillProposals keeps learned-skill proposals until the owner decides them.
// A missing proposal is not found, reported with a stable error identity.
type SkillProposals interface {
	// Add stores a new pending proposal; an id already stored is a conflict, and so
	// is a second pending proposal of the same skill name — two open rows for
	// one class would decide against each other in the owner's queue, so the
	// store, not the caller's discipline, enforces it.
	Add(ctx context.Context, p *aggregate.SkillProposal) error
	Get(ctx context.Context, id string) (*aggregate.SkillProposal, error)
	Pending(ctx context.Context) ([]*aggregate.SkillProposal, error)
	// Seen answers the candidate's cooldown: a pending proposal of this name holds
	// it whatever its op, a proposal of this op the owner accepted since the cutoff
	// holds it too, and so does one the owner dismissed while it was still open, so
	// the pass does not ask the owner twice about the same skill. A retirement the
	// pass wrote, and a dismissal of a proposal that had already gone stale, hold
	// nothing.
	Seen(ctx context.Context, name string, op enum.SkillProposalOp, since time.Time) (bool, error)
	// Declined lists the owner's refusals the candidate prompt shows: dismissals of
	// proposals that were still open, decided since the cutoff, newest first, at
	// most limit. A retirement and a stale close are not refusals, as in Seen.
	Declined(ctx context.Context, since time.Time, limit int) ([]*aggregate.SkillProposal, error)
}

// SkillDecisions is the one write that decides a proposal. Commit stores the
// proposal's decision and the ownership row it leaves behind in one transaction,
// or neither. row is nil for a dismissal and must be present for an accept.
// A proposal decided meanwhile is a conflict, and so is a row whose precondition no
// longer holds: the name taken on a create, or the digest or level moved on a
// patch. The file write happens before the commit, and a failed commit is what
// the caller compensates, so a refused commit must not leave either write behind.
type SkillDecisions interface {
	Commit(ctx context.Context, p *aggregate.SkillProposal, row *aggregate.SkillOwnership) error
}

// SkillOwnership keeps the ownership row of each learned skill, keyed by its
// root and name: a same-named skill of another root must never answer for a
// learned one. A missing row is not found, reported with a stable error identity.
type SkillOwnership interface {
	Get(ctx context.Context, root enum.SkillRoot, name string) (*aggregate.SkillOwnership, error)
	// List returns every ownership row in one read: the skills page joins the
	// catalog with levels and pending counts in a constant number of queries,
	// never one per row.
	List(ctx context.Context) ([]*aggregate.SkillOwnership, error)
	// Save records an observed change; it never decides a proposal, so an
	// accept's row goes through SkillDecisions.Commit and never through here.
	// It is a compare-and-swap, like the writer's: an empty expectDigest writes
	// only when no row exists, and a non-empty one writes only while the stored
	// row's digest still equals it — a stale read must not overwrite a row a
	// commit moved meanwhile. A mismatched write is a conflict, a conditional
	// write against no row is not found.
	Save(ctx context.Context, o *aggregate.SkillOwnership, expectDigest string) error
}

// SkillDocument is the learned skill a writer renders: the domain values, never
// the file bytes. The adapter owns the SKILL.md format — frontmatter, escaping,
// layout — because the format written and the format read must live in one
// place, and the application must not know that YAML exists.
type SkillDocument struct {
	Name        string
	Description string
	Body        string
}

// SkillWritten is what a write left on disk: the path it was written to, which
// the proposal records, and the digest of the rendered bytes, which the
// ownership row records — the digest a later read compares against.
type SkillWritten struct {
	Path   string
	Digest string
}

// SkillWriter writes learned skills into the nusashell root, the only root they
// live in. It renders the document itself and every digest covers the rendered
// whole file, frontmatter included. An empty expectDigest is create-only: the
// write is refused when the file already exists with other content. A non-empty
// expectDigest replaces the file only while its digest on disk still equals it.
// Either way, a file that already holds exactly the rendered bytes is a
// successful no-op rather than a conflict: it is what a retry of an accept whose
// commit failed finds, and the retry is how that row gets finished. A refused
// write is a conflict; archiving a missing skill is not found, and archiving
// one that no longer holds the digest the caller read is a conflict that
// moves nothing.
type SkillWriter interface {
	Write(ctx context.Context, root enum.SkillRoot, doc SkillDocument, expectDigest string) (SkillWritten, error)
	// Digest is the digest the document renders to: the value Write records for
	// it, and the one a catalog read of the same file reports. It writes nothing,
	// so the application can tell whether a file on disk is this document.
	Digest(doc SkillDocument) (string, error)
	// Archive moves the skill out of the root only while its file still holds
	// expectDigest.
	Archive(ctx context.Context, root enum.SkillRoot, name, expectDigest string) error
}
