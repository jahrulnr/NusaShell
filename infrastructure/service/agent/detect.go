package agent

import (
	"os/exec"
	"slices"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// knownCLI is a popular agent CLI that speaks ACP over stdio. Candidates
// are tried in order; the first binary found on PATH wins. Each candidate
// is an argv slice: the binary name, then its ACP-mode arguments.
type knownCLI struct {
	id, name, description string
	candidates            [][]string
}

// knownCLIs follows the ACP registry invocations, ordered by preference.
var knownCLIs = []knownCLI{
	{"devin", "Devin", "Cognition Devin over ACP", [][]string{{"devin", "acp"}}},
	{"claude", "Claude Agent", "Anthropic Claude via the ACP adapter", [][]string{{"claude-agent-acp"}, {"claude-code-acp"}}},
	// The Codex CLI itself is not an ACP server; `codex acp` is a TUI prompt.
	{"codex", "Codex", "OpenAI Codex via the ACP adapter", [][]string{{"codex-acp"}}},
	{"gemini", "Gemini CLI", "Google Gemini CLI in ACP mode", [][]string{{"gemini", "--acp"}, {"gemini", "--experimental-acp"}}},
	{"copilot", "GitHub Copilot", "GitHub Copilot CLI in ACP mode", [][]string{{"copilot", "--acp"}}},
	{"opencode", "OpenCode", "OpenCode agent in ACP mode", [][]string{{"opencode", "acp"}}},
	{"auggie", "Auggie", "Augment Code Auggie in ACP mode", [][]string{{"auggie", "--acp"}}},
	{"goose", "goose", "Block goose agent in ACP mode", [][]string{{"goose", "acp"}}},
	{"kimi", "Kimi CLI", "Moonshot AI Kimi CLI in ACP mode", [][]string{{"kimi", "acp"}}},
	{"cursor", "Cursor", "Cursor agent CLI in ACP mode", [][]string{{"cursor-agent", "acp"}}},
	{"kilo", "Kilo", "Kilo Code agent in ACP mode", [][]string{{"kilo", "acp"}}},
	{"qwen", "Qwen Code", "Alibaba Qwen Code in ACP mode", [][]string{{"qwen", "--acp"}}},
	{"vibe", "Mistral Vibe", "Mistral Vibe ACP agent", [][]string{{"vibe-acp"}}},
	{"kiro", "Kiro CLI", "AWS Kiro CLI in ACP mode", [][]string{
		{"kiro-cli", "acp", "--agent-engine=v3"},
		{"kiro-cli-chat", "acp", "--agent-engine=v3"},
	}},
}

// Detector looks up known agent CLIs on PATH.
type Detector struct{}

var _ dservice.AgentDetector = Detector{}

// Detect returns each known CLI found on PATH, in catalog order.
func (Detector) Detect() []dservice.AgentCandidate {
	var out []dservice.AgentCandidate
	for _, k := range knownCLIs {
		for _, argv := range k.candidates {
			path, err := exec.LookPath(argv[0])
			if err != nil {
				continue
			}
			out = append(out, dservice.AgentCandidate{
				ID: k.id, Name: k.name, Description: k.description,
				Command: path, Args: slices.Clone(argv[1:]),
			})
			break
		}
	}
	return out
}
