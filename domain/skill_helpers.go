package domain

import "strings"

// SkillSlug normalizes a skill name into a filesystem-safe ID matching the
// slug rules used across the skill subsystem: lowercase, ASCII letters and
// digits, with runs of whitespace/underscore/hyphen collapsed into a single
// hyphen. An empty result defaults to "skill".
func SkillSlug(name string) string {
	var out []byte
	prevDash := true
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, byte(r))
			prevDash = false
		case r == ' ' || r == '_' || r == '-':
			if !prevDash {
				out = append(out, '-')
				prevDash = true
			}
		}
	}
	result := strings.Trim(string(out), "-")
	if result == "" {
		result = "skill"
	}
	return result
}
