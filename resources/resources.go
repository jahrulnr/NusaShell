// Package resources embeds static agent resources (prompts, docs, skills,
// and first-install profile templates) from the resources/ tree so they
// ship inside the binary and stay editable as plain markdown files.
// Mirrors NusaShell Electron's resources/agent/ layout.
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

// TemplatesFS embeds first-install profile documents from
// resources/templates/. On boot, missing memory/user.md and
// memory/soul.md are copied from here into the data directory.
//
//go:embed templates/*.md
var TemplatesFS embed.FS

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

// Template returns the content of a named first-install template file
// (e.g. "user.md", "soul.md") from resources/templates/. The .md
// extension is appended when the caller omits it.
func Template(name string) string {
	if len(name) < 3 || name[len(name)-3:] != ".md" {
		name += ".md"
	}
	data, err := TemplatesFS.ReadFile("templates/" + name)
	if err != nil {
		return ""
	}
	return string(data)
}

func joinPromptSections(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(p)
	}
	return b.String()
}

// SystemPrompt is the interactive conversation system prompt: identity and
// operating rules from system.md plus the Primary Memory Writing Rules.
func SystemPrompt() string {
	return Prompt("system")
}

// LearnerPrompt loads the unified learner system prompt plus the same
// Primary Memory Writing Rules the conversation agent sees (the learner
// does not receive system.md).
func LearnerPrompt() string {
	return Prompt("learner")
}

// UserPrompt returns the content of a named user-role prompt file from
// resources/agent/prompts/user/. These are short, imperative user
// messages injected as the opening user turn for background agents
// (e.g. the unified background learning agent). The .md extension is
// appended when omitted. User prompts are treated by the LLM as direct
// instructions, which models obey more reliably than system-prompt
// guidelines.
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
	compactedSummaries = "{{compacted_summaries}}"
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

// LearnerUserPrompt loads the short learner user-role instruction.
func LearnerUserPrompt() string {
	return UserPrompt("learner")
}

// ConsolidatorPrompt is a legacy alias for LearnerPrompt.
func ConsolidatorPrompt() string {
	return LearnerPrompt()
}

// ConsolidatorUserPrompt is a legacy alias for LearnerUserPrompt.
func ConsolidatorUserPrompt() string {
	return LearnerUserPrompt()
}

// SkillEvolverPrompt is a legacy alias for LearnerPrompt.
func SkillEvolverPrompt() string {
	return LearnerPrompt()
}

// SkillEvolverUserPrompt is a legacy alias for LearnerUserPrompt.
func SkillEvolverUserPrompt() string {
	return LearnerUserPrompt()
}

// SkillEvaluatorPrompt is a legacy alias for LearnerPrompt.
func SkillEvaluatorPrompt() string {
	return LearnerPrompt()
}

// SkillEvaluatorUserPrompt is a legacy alias for LearnerUserPrompt.
func SkillEvaluatorUserPrompt() string {
	return LearnerUserPrompt()
}

// Brief new agent after compaction to continue conversation
func CompactedUserPrompt(summaries string) string {
	return strings.Replace(UserPrompt("compacted-continue"), compactedSummaries, summaries, 1)
}
