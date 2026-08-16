// Package resources embeds static agent resources (prompts, docs, skills)
// from the resources/ tree so they ship inside the binary and stay editable
// as plain markdown files. Mirrors NusaShell Electron's resources/agent/
// layout.
package resources

import (
	"embed"
	"strings"
)

//go:embed agent/prompts/*.md
var PromptsFS embed.FS

//go:embed agent/docs/*.md
var DocsFS embed.FS

//go:embed agent/docs/ui-source/ui-map.json
var UIMapJSON []byte

// BuiltinSkillsFS embeds the bundled skill packages from
// resources/agent/skills/. Each skill is a directory containing SKILL.md
// plus optional support files (references/, templates/, etc.). The seeder
// copies these to the user data directory on startup, respecting user
// deletions.
//
//go:embed agent/skills
var BuiltinSkillsFS embed.FS

// Prompt returns the content of a named prompt file (e.g. "system.md",
// "continue.md") from resources/agent/prompts/. The .md extension is
// appended when the caller omits it.
func Prompt(name string) string {
	if len(name) < 3 || name[len(name)-3:] != ".md" {
		name += ".md"
	}
	data, err := PromptsFS.ReadFile("agent/prompts/" + name)
	if err != nil {
		return ""
	}
	return string(data)
}

const skillReviewRulesPlaceholder = "{{skill_review_rules}}"

// ReviewPrompt loads the combined background review prompt
// (review.md), substituting the {{skill_review_rules}} template
// with skill-rules.md content. There is intentionally only one
// review agent — it decides both memory and skill writes in a single pass
// to avoid the redundancy bug where memory contains skill fragments and
// vice versa.
func ReviewPrompt() string {
	base := Prompt("review")
	if base == "" {
		return ""
	}
	rules := Prompt("skill-rules")
	if rules == "" {
		return base
	}
	return strings.Replace(base, skillReviewRulesPlaceholder, strings.TrimSpace(rules), 1)
}
