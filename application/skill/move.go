package skill

import (
	"context"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// MoveDeps are the ports one level move needs: the catalog to see the file
// the owner is looking at, and the ownership store to read the row and
// persist the decision. A move never writes the file — ownership is a
// permission, and rewriting the body to change one would lose the owner's
// text.
type MoveDeps struct {
	Catalog   repository.Skills
	Ownership repository.SkillOwnership
}

// MoveInput is one owner decision from the Skills page. Digest is the disk
// digest the page showed — the bytes the owner read and decided on.
type MoveInput struct {
	Name      string
	Level     string
	Digest    string
	Workspace string
}

// MoveLevel records the owner's decision about who a learned skill answers
// to. It refuses, in words the owner can act on, everything that moved: a
// file that changed since the page loaded, a skill that is no longer live,
// a row another decision moved while this one was writing. Both directions
// demand the same evidence — handing a skill to the loop and taking it back
// both name the exact bytes the owner saw, so a hand-edited skill can be
// handed back while a stale page cannot unlock or revoke anything.
func MoveLevel(ctx context.Context, deps MoveDeps, in MoveInput) error {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return apperror.New(400, "a level move names its skill", nil)
	}
	level, ok := enum.ParseSkillLevel(in.Level)
	if !ok {
		return apperror.New(400, "level must be user or agent", nil)
	}
	if strings.TrimSpace(in.Digest) == "" {
		return apperror.New(400, "a level move carries the digest the page showed", nil)
	}
	catalog, err := deps.Catalog.List(ctx, in.Workspace)
	if err != nil {
		return err
	}
	var entry *repository.Skill
	for i := range catalog.Skills {
		if catalog.Skills[i].Name == name {
			entry = &catalog.Skills[i]
			break
		}
	}
	if entry == nil {
		return apperror.New(404, "the skill "+name+" is no longer live — reload the page", nil)
	}
	// A builtin or an owner-authored file is outside the loop whatever a row
	// says, so the level moves only on the learned root's own entry.
	if entry.Source != string(enum.SkillRootNusashell) {
		return apperror.New(400, "the level moves only on a learned skill; "+name+" is the owner's "+entry.Source+" file", nil)
	}
	// The staleness check, in both directions: the owner decided on the bytes
	// the page showed, and a file that moved since is a decision about a
	// file that no longer exists.
	if entry.Digest != in.Digest {
		return apperror.New(409, "the skill's file changed since this page was opened — reload the page and try again", nil)
	}
	row, err := deps.Ownership.Get(ctx, enum.SkillRootNusashell, name)
	if err != nil {
		if apperror.As(err).StatusCode() == 404 {
			return apperror.New(404, "the skill "+name+" has no ownership row — it is already the owner's; only accepting a proposal grants the loop write access", nil)
		}
		return err
	}
	// The compare-and-swap happens at the digest the row was read at, before
	// MoveLevel moves it: a decision that changed the row while this request
	// was writing must not be overwritten by it.
	expect := row.Digest()
	if err := row.MoveLevel(level, entry.Digest); err != nil {
		return apperror.New(400, err.Error(), nil)
	}
	if err := deps.Ownership.Save(context.WithoutCancel(ctx), row, expect); err != nil {
		if apperror.As(err).StatusCode() == 409 {
			return apperror.New(409, "this skill's ownership changed while the page was open — reload the page and try again", nil)
		}
		return err
	}
	// The compare-and-swap guards only the digest, and a take-back does not
	// move it — two take-backs racing between their reads and their saves both
	// compare against the same digest and both write the same level, so the
	// race is harmless by construction. A re-grant does move the digest, so a
	// take-back racing a re-grant is the case the 409 above answers.
	return nil
}
