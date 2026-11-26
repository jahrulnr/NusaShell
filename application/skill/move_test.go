package skill

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

func moveSetup() (*decideWorld, MoveDeps) {
	w := &decideWorld{
		proposals: map[string]*aggregate.SkillProposal{},
		rows:      map[string]*aggregate.SkillOwnership{},
		files:     map[string]string{},
	}
	return w, MoveDeps{Catalog: decideCatalog{w}, Ownership: decideOwnership{w}}
}

func moveEntry(name, source, digest string) repository.Skill {
	return repository.Skill{Name: name, Description: "Use when writing " + name, Source: source,
		Path: "/" + source + "/" + name + "/SKILL.md", Digest: digest}
}

func moveRow(t *testing.T, w *decideWorld, name string, level enum.SkillLevel, digest string) {
	t.Helper()
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, name, level, digest)
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/"+name] = row
}

func storedRow(t *testing.T, w *decideWorld, name string) *aggregate.SkillOwnership {
	t.Helper()
	row, ok := w.rows["nusashell/"+name]
	if !ok {
		t.Fatalf("the row for %s vanished", name)
	}
	return row
}

func moveIn(name, level, digest string) MoveInput {
	return MoveInput{Name: name, Level: level, Digest: digest, Workspace: "/ws"}
}

func TestMoveLevelTakesASkillBackToTheOwner(t *testing.T) {
	w, deps := moveSetup()
	w.catalog = []repository.Skill{moveEntry("release-notes", "nusashell", "disk-1")}
	moveRow(t, w, "release-notes", enum.SkillLevelAgent, "disk-1")
	if err := MoveLevel(context.Background(), deps, moveIn("release-notes", "user", "disk-1")); err != nil {
		t.Fatalf("take-back: %v", err)
	}
	row := storedRow(t, w, "release-notes")
	if row.Level() != enum.SkillLevelUser {
		t.Errorf("level = %s, want user", row.Level())
	}
	if row.Digest() != "disk-1" {
		t.Errorf("a take-back keeps the digest as history, got %q", row.Digest())
	}
}

func TestMoveLevelRegrantsAHandEditedSkill(t *testing.T) {
	// The binding semantic: the loop wrote disk-1, the owner edited the file
	// to disk-2, and handing the skill back to the loop must still be
	// possible — against the bytes the owner sees, never the old row digest.
	w, deps := moveSetup()
	w.catalog = []repository.Skill{moveEntry("release-notes", "nusashell", "disk-2")}
	moveRow(t, w, "release-notes", enum.SkillLevelUser, "disk-1")
	if err := MoveLevel(context.Background(), deps, moveIn("release-notes", "agent", "disk-2")); err != nil {
		t.Fatalf("re-grant of a hand-edited skill: %v", err)
	}
	row := storedRow(t, w, "release-notes")
	if row.Level() != enum.SkillLevelAgent || row.Digest() != "disk-2" {
		t.Errorf("the re-grant = %s/%q, want agent at the bytes the owner saw", row.Level(), row.Digest())
	}
}

func TestMoveLevelRefusesAStalePageInBothDirections(t *testing.T) {
	for _, level := range []string{"user", "agent"} {
		w, deps := moveSetup()
		w.catalog = []repository.Skill{moveEntry("release-notes", "nusashell", "disk-2")}
		moveRow(t, w, "release-notes", enum.SkillLevelAgent, "disk-1")
		err := MoveLevel(context.Background(), deps, moveIn("release-notes", level, "disk-1"))
		if apperror.As(err).StatusCode() != 409 {
			t.Fatalf("a stale page moving to %s: %v; want a 409", level, err)
		}
		for _, want := range []string{"changed since this page was opened", "reload"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal to %s must tell the owner to reload, got %q", level, err.Error())
			}
		}
		row := storedRow(t, w, "release-notes")
		if row.Level() != enum.SkillLevelAgent || row.Digest() != "disk-1" {
			t.Errorf("a refused move to %s changed the row: %s/%q", level, row.Level(), row.Digest())
		}
	}
}

func TestMoveLevelRefusesANonLearnedTarget(t *testing.T) {
	for source, digest := range map[string]string{"builtin": "bd", "global": "gd", "workspace": "wd"} {
		w, deps := moveSetup()
		w.catalog = []repository.Skill{moveEntry("release-notes", source, digest)}
		moveRow(t, w, "release-notes", enum.SkillLevelAgent, digest)
		err := MoveLevel(context.Background(), deps, moveIn("release-notes", "user", digest))
		if apperror.As(err).StatusCode() != 400 {
			t.Fatalf("a %s target: %v; want a 400", source, err)
		}
		if !strings.Contains(err.Error(), "learned") || !strings.Contains(err.Error(), source) {
			t.Errorf("the refusal must name the %s file, got %q", source, err.Error())
		}
		row := storedRow(t, w, "release-notes")
		if row.Level() != enum.SkillLevelAgent {
			t.Errorf("a refused %s move changed the level: %s", source, row.Level())
		}
	}
}

func TestMoveLevelNamesWhatIsMissing(t *testing.T) {
	w, deps := moveSetup()
	w.catalog = []repository.Skill{moveEntry("release-notes", "nusashell", "disk-1")}
	if err := MoveLevel(context.Background(), deps, moveIn("ghost-skill", "user", "disk-1")); apperror.As(err).StatusCode() != 404 {
		t.Errorf("a skill that is not live: %v; want a 404", err)
	}
	err := MoveLevel(context.Background(), deps, moveIn("release-notes", "user", "disk-1"))
	if apperror.As(err).StatusCode() != 404 {
		t.Fatalf("a skill with no row: %v; want a 404", err)
	}
	if !strings.Contains(err.Error(), "no ownership row") || !strings.Contains(err.Error(), "proposal") {
		t.Errorf("the refusal must explain how a row is earned, got %q", err.Error())
	}
}

func TestMoveLevelValidatesItsInput(t *testing.T) {
	w, deps := moveSetup()
	w.catalog = []repository.Skill{moveEntry("release-notes", "nusashell", "disk-1")}
	moveRow(t, w, "release-notes", enum.SkillLevelAgent, "disk-1")
	cases := []struct {
		name string
		in   MoveInput
	}{
		{"an empty name", moveIn("  ", "user", "disk-1")},
		{"a level outside user and agent", moveIn("release-notes", "owner", "disk-1")},
		{"an empty digest", moveIn("release-notes", "user", " ")},
	}
	for _, c := range cases {
		if err := MoveLevel(context.Background(), deps, c.in); apperror.As(err).StatusCode() != 400 {
			t.Errorf("%s: %v; want a 400", c.name, err)
		}
	}
}

// racingRows hands back the row it read, then lets another writer move the
// store between the read and the save — the window the compare-and-swap
// exists to close.
type racingRows struct {
	w        *decideWorld
	afterGet func()
}

func (r racingRows) Get(ctx context.Context, root enum.SkillRoot, name string) (*aggregate.SkillOwnership, error) {
	row, err := decideOwnership{r.w}.Get(ctx, root, name)
	if err == nil {
		r.afterGet()
	}
	return row, err
}
func (r racingRows) List(ctx context.Context) ([]*aggregate.SkillOwnership, error) {
	return decideOwnership{r.w}.List(ctx)
}
func (r racingRows) Save(ctx context.Context, o *aggregate.SkillOwnership, expectDigest string) error {
	return decideOwnership{r.w}.Save(ctx, o, expectDigest)
}

func TestMoveLevelDoesNotOverwriteADecisionMadeMeanwhile(t *testing.T) {
	w, _ := moveSetup()
	w.catalog = []repository.Skill{moveEntry("release-notes", "nusashell", "disk-1")}
	moveRow(t, w, "release-notes", enum.SkillLevelUser, "disk-1")
	rows := racingRows{w: w, afterGet: func() {
		// A concurrent accept committed between this request's read and its
		// write: the row now describes bytes this decision never saw.
		moveRow(t, w, "release-notes", enum.SkillLevelAgent, "concurrent")
	}}
	deps := MoveDeps{Catalog: decideCatalog{w}, Ownership: rows}
	err := MoveLevel(context.Background(), deps, moveIn("release-notes", "user", "disk-1"))
	if apperror.As(err).StatusCode() != 409 {
		t.Fatalf("a decision made meanwhile: %v; want a 409", err)
	}
	if !strings.Contains(err.Error(), "reload") {
		t.Errorf("the refusal must tell the owner to reload, got %q", err.Error())
	}
	row := storedRow(t, w, "release-notes")
	if row.Digest() != "concurrent" || row.Level() != enum.SkillLevelAgent {
		t.Errorf("the move overwrote the concurrent decision: %s/%q", row.Level(), row.Digest())
	}
}
