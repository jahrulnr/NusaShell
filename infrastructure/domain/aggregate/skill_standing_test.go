package aggregate

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// Who may change a learned skill is read from one rule: the row's level stands
// only while its digest is the file's on disk. No row, or bytes that moved since
// the row was written, is the owner's, whatever the row says.
func TestSkillStandingOfReadsTheRowOnlyWhileItDescribesTheFile(t *testing.T) {
	agent := mustOwnership(t, enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "rd1")
	user := mustOwnership(t, enum.SkillRootNusashell, "release-notes", enum.SkillLevelUser, "rd1")
	cases := []struct {
		name string
		row  *SkillOwnership
		disk string
		want SkillStanding
	}{
		{"no row is the owner's", nil, "rd1", SkillStanding{Level: enum.SkillLevelUser}},
		{"an agent row over the bytes it wrote stands", agent, "rd1", SkillStanding{Level: enum.SkillLevelAgent, Tracked: true}},
		{"an agent row over edited bytes is the owner's", agent, "rd2", SkillStanding{Level: enum.SkillLevelUser, Tracked: true, Changed: true}},
		{"a user row over the bytes it knows stays the owner's", user, "rd1", SkillStanding{Level: enum.SkillLevelUser, Tracked: true}},
		{"a user row over edited bytes is changed and the owner's", user, "rd2", SkillStanding{Level: enum.SkillLevelUser, Tracked: true, Changed: true}},
	}
	for _, c := range cases {
		if got := SkillStandingOf(c.row, c.disk); got != c.want {
			t.Errorf("%s: SkillStandingOf = %+v, want %+v", c.name, got, c.want)
		}
	}
}
