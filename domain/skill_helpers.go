package domain

import (
	"fmt"
	"strings"
)

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

// SkillSaveSupportPath classifies the optional path argument on skill save.
// Empty path and any path whose basename is SKILL.md (including absolute
// data-dir paths) mean the caller should Save the SKILL.md body. A relative
// support-file path means WriteFile. Absolute non-SKILL.md paths and parent
// traversal are rejected.
func SkillSaveSupportPath(path string) (rel string, support bool, err error) {
	raw := strings.TrimSpace(path)
	if raw == "" {
		return "", false, nil
	}
	norm := strings.ReplaceAll(raw, "\\", "/")
	base := norm
	if i := strings.LastIndex(norm, "/"); i >= 0 {
		base = norm[i+1:]
	}
	if strings.EqualFold(base, "SKILL.md") {
		return "", false, nil
	}
	if skillSavePathIsAbs(norm) || skillSavePathHasParent(norm) {
		return "", false, fmt.Errorf("path must be a relative support file inside the skill folder; omit path to create or update SKILL.md")
	}
	rel = strings.TrimPrefix(norm, "./")
	if rel == "" || rel == "." {
		return "", false, fmt.Errorf("path must be a relative support file inside the skill folder; omit path to create or update SKILL.md")
	}
	return rel, true, nil
}

func skillSavePathIsAbs(norm string) bool {
	if strings.HasPrefix(norm, "/") {
		return true
	}
	if len(norm) >= 3 && norm[1] == ':' && norm[2] == '/' {
		return true
	}
	return false
}

func skillSavePathHasParent(norm string) bool {
	for _, part := range strings.Split(norm, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}
