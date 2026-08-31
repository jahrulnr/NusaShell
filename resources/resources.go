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

//go:embed agent/prompts/*.md agent/prompts/user/*.md
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

// UserPrompt returns the content of a named user-role prompt file from
// resources/agent/prompts/user/. These are short, imperative user
// messages injected as the opening user turn for background agents
// (e.g. the review agent). The .md extension is appended when omitted.
// User prompts are treated by the LLM as direct instructions, which
// models obey more reliably than system-prompt guidelines.
func UserPrompt(name string) string {
	if len(name) < 3 || name[len(name)-3:] != ".md" {
		name += ".md"
	}
	data, err := PromptsFS.ReadFile("agent/prompts/user/" + name)
	if err != nil {
		return ""
	}
	return string(data)
}

const (
	skillReviewRulesPlaceholder = "{{skill_review_rules}}"
	compactedSummaries          = "{{compacted_summaries}}"
)

func AudioVisionSystemPrompt() string {
	return Prompt("audio-vision")
}

func ImageVisionSystemPrompt() string {
	return Prompt("image-vision")
}

func VideoVisionSystemPrompt() string {
	return Prompt("video-vision")
}

// DescribeImagePrompt returns the user-role prompt sent to the vision
// fallback model when describing an image for a non-vision chat model.
func DescribeImagePrompt() string {
	return UserPrompt("describe-image")
}

// DescribeVideoPrompt returns the user-role prompt sent to the video
// fallback model when describing a video for a non-video chat model.
func DescribeVideoPrompt() string {
	return UserPrompt("describe-video")
}

// TranscribeAudioPrompt returns the user-role prompt sent to the audio
// fallback model when transcribing/describing audio for a non-audio chat
// model.
func TranscribeAudioPrompt() string {
	return UserPrompt("transcribe-audio")
}

// ReviewPrompt loads the combined background review prompt
// (review.md), substituting the {{skill_review_rules}} template
// with skill-rules.md content. There is intentionally only one review
// agent — it decides both memory and skill writes in a single pass to
// avoid the redundancy bug where memory contains skill fragments and
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

// ReviewUserPrompt loads the user-role review prompt
// (prompts/user/review.md). This is injected as the opening user message
// for the background review agent. Models treat user messages as direct
// instructions, which they obey more reliably than system-prompt
// guidelines — so the review trigger is a short imperative user message,
// not a long system-prompt directive.
func ReviewUserPrompt() string {
	return UserPrompt("review")
}

// Brief new agent after compaction to continue conversation
func CompactedUserPrompt(summaries string) string {
	return strings.Replace(UserPrompt("compacted-continue"), compactedSummaries, summaries, 1)
}
