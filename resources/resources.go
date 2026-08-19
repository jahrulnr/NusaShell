// Package resources embeds static agent resources (prompts, docs, skills)
// from the resources/ tree so they ship inside the binary and stay editable
// as plain markdown files. Mirrors NusaShell Electron's resources/agent/
// layout.
package resources

import (
	"embed"
	"io/fs"
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

// SoundsFS embeds the notification sound files from resources/sounds/
// so they ship inside the binary and are served to the frontend for
// turn-complete / turn-error audio cues.
//
//go:embed sounds/*.wav
var SoundsFS embed.FS

// SoundAssets returns the embedded sounds filesystem rooted at the
// sounds/ directory. Callers use fs.Sub to serve files at /sounds/.
func SoundAssets() (fs.FS, error) {
	return fs.Sub(SoundsFS, "sounds")
}

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

const (
	skillReviewRulesPlaceholder = "{{skill_review_rules}}"
	primaryMemoryPlaceholder    = "{{primary_memory}}"
)

// ReviewPrompt loads the combined background review prompt
// (review.md), substituting the {{skill_review_rules}} template
// with skill-rules.md content. The {{primary_memory}} placeholder is left
// intact here and substituted by the caller with the live primary memory
// content (so the review agent sees what is already in primary.md before
// deciding to promote/demote). There is intentionally only one
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

// PrimaryMemoryPlaceholder returns the placeholder token used in review.md
// to mark where the live primary memory content should be injected.
func PrimaryMemoryPlaceholder() string { return primaryMemoryPlaceholder }

// SubstitutePrimaryMemory replaces the {{primary_memory}} placeholder in a
// review system prompt with the formatted content of the primary memory
// document. When the document is empty or the placeholder is absent, the
// prompt is returned unchanged. The caller passes the live PrimaryMemory
// (read from disk) so the review agent sees the current state.
func SubstitutePrimaryMemory(prompt string, entries []string) string {
	if prompt == "" || len(entries) == 0 {
		// Either nothing to inject or no entries — strip the placeholder
		// so the agent does not see a raw {{primary_memory}} token.
		return strings.ReplaceAll(prompt, primaryMemoryPlaceholder, "(empty)")
	}
	body := strings.Join(entries, "\n")
	return strings.Replace(prompt, primaryMemoryPlaceholder, body, 1)
}
