package domain

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// TranscriptSummary extracts the concatenated text chunks from a run's
// transcript, truncated to 4000 chars. Returns the error or stop_reason
// when no text was produced. Pure function — no I/O.
func TranscriptSummary(run *AcpRun) string {
	var b strings.Builder
	for _, c := range run.Transcript {
		if c.Kind == "text" && c.Text != "" {
			b.WriteString(c.Text)
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		if run.Error != "" {
			return run.Error
		}
		return run.StopReason
	}
	if len(s) > 4000 {
		return s[:4000] + "…"
	}
	return s
}

// SubagentCompletionResult builds the tool result injected into the
// parent agent's message history when a subagent finishes: YAML
// frontmatter (status, workspace, output_path) + markdown body.
func SubagentCompletionResult(run *AcpRun, outputPath string) string {
	body := SubagentCompletionBody(run)
	var y strings.Builder
	y.WriteString("---\n")
	y.WriteString("status: ")
	y.WriteString(YamlScalar(string(run.Status)))
	y.WriteString("\n")
	if run.ID != "" {
		y.WriteString("id: ")
		y.WriteString(YamlScalar(run.ID))
		y.WriteString("\n")
	}
	if run.Workspace != "" {
		y.WriteString("workspace: ")
		y.WriteString(YamlScalar(run.Workspace))
		y.WriteString("\n")
	}
	if outputPath != "" {
		y.WriteString("output_path: ")
		y.WriteString(YamlScalar(outputPath))
		y.WriteString("\n")
	}
	y.WriteString("---\n\n")
	y.WriteString(body)
	return y.String()
}

// SubagentCompletionBody builds the markdown body of the tool result —
// the human-readable summary the parent agent reads and acts on.
//
// The full transcript is persisted as JSON to the run's output_path
// (conversations/<conv_id>.acp/acprun_<id>.json). The tool result only
// carries the LAST meaningful content so the parent agent sees the final
// result, not intermediate progress. Selection order:
//  1. Last `text` chunk (the agent's final message/summary)
//  2. Last `thought` chunk (reasoning — only when no text was produced)
//  3. StructuredFallbackSummary (tool-only, empty, or cancelled runs)
//
// Handles: normal text, failed+text, tool-only/empty/thinking-only,
// and cancelled.
func SubagentCompletionBody(run *AcpRun) string {
	textOut := lastTranscriptText(run)

	isFailed := run.Status == AcpRunFailed
	isCancelled := run.Status == AcpRunCancelled

	if isCancelled {
		if textOut != "" {
			return textOut + "\n\n[Subagent was cancelled.]"
		}
		return "Subagent was cancelled."
	}

	if isFailed && textOut != "" {
		errPart := run.Error
		if errPart == "" {
			errPart = run.StopReason
		}
		if errPart != "" {
			if len(textOut) > 3800 {
				textOut = textOut[:3800] + "…"
			}
			return textOut + "\n\n[Subagent failed: " + errPart + "]"
		}
	}

	if textOut != "" {
		if len(textOut) > 4000 {
			return textOut[:4000] + "…"
		}
		return textOut
	}

	// No text chunk — fall back to the last thought (reasoning) so the
	// parent agent has context when the subagent ended without a visible
	// message (e.g. stopped mid-reasoning, tool-only run that produced
	// thinking but no final text).
	if thought := lastThoughtText(run); thought != "" {
		if len(thought) > 3800 {
			thought = thought[:3800] + "…"
		}
		if isFailed {
			errPart := run.Error
			if errPart == "" {
				errPart = run.StopReason
			}
			if errPart != "" {
				return thought + "\n\n[Subagent failed: " + errPart + "]"
			}
		}
		return thought
	}

	return StructuredFallbackSummary(run)
}

// StructuredFallbackSummary builds a summary when the subagent produced
// no text chunks (tool-only, thinking-only, or empty output).
func StructuredFallbackSummary(run *AcpRun) string {
	var parts []string
	switch run.Status {
	case AcpRunFailed:
		parts = append(parts, "Subagent failed.")
	case AcpRunCancelled:
		parts = append(parts, "Subagent was cancelled.")
	case AcpRunCompleted:
		parts = append(parts, "Subagent completed with no text output.")
	default:
		parts = append(parts, "Subagent ended (status: "+string(run.Status)+").")
	}
	if run.Error != "" {
		parts = append(parts, "Error: "+run.Error)
	}
	if run.StopReason != "" && run.StopReason != run.Error {
		parts = append(parts, "Stop reason: "+run.StopReason)
	}
	if lt := LastToolCallFromTranscript(run); lt != "" {
		parts = append(parts, "Last tool: "+lt)
	}
	return strings.Join(parts, " ")
}

// LastToolCallFromTranscript scans the transcript in reverse and returns
// a compact description of the last tool call (title + status), or "".
func LastToolCallFromTranscript(run *AcpRun) string {
	for i := len(run.Transcript) - 1; i >= 0; i-- {
		c := run.Transcript[i]
		if c.Kind == "tool" && c.ToolTitle != "" {
			s := c.ToolTitle
			if c.ToolStatus != "" {
				s += " (" + c.ToolStatus + ")"
			}
			return s
		}
	}
	return ""
}

// AvailableAcpSummary returns a comma-separated list of agent names+ids
// and the first agent's name as default. Pure function on []*AcpAgent.
func AvailableAcpSummary(agents []*AcpAgent) (list, def string) {
	if len(agents) == 0 {
		return "(none configured)", "(none)"
	}
	var names []string
	for _, agent := range agents {
		names = append(names, agent.Name+" ("+agent.ID+")")
	}
	return strings.Join(names, ", "), agents[0].Name
}

// PermissionTitle returns the pending permission tool title, or "".
func PermissionTitle(run *AcpRun) string {
	if run != nil && run.PendingPermission != nil {
		return run.PendingPermission.ToolTitle
	}
	return ""
}

// lastTranscriptText returns the LAST text chunk from a run's transcript
// — the agent's final visible message/summary, not intermediate progress.
// The full transcript is persisted in the run's JSON output file; the
// tool result only carries this last chunk so the parent agent sees the
// final result without context overflow from concatenating all progress.
func lastTranscriptText(run *AcpRun) string {
	for i := len(run.Transcript) - 1; i >= 0; i-- {
		c := run.Transcript[i]
		if c.Kind == "text" && strings.TrimSpace(c.Text) != "" {
			return strings.TrimSpace(c.Text)
		}
	}
	return ""
}

// lastThoughtText returns the LAST thought (reasoning) chunk from a run's
// transcript. Used as a fallback when no text chunk was produced so the
// parent agent still has context about what the subagent was doing.
func lastThoughtText(run *AcpRun) string {
	for i := len(run.Transcript) - 1; i >= 0; i-- {
		c := run.Transcript[i]
		if c.Kind == "thought" && strings.TrimSpace(c.Text) != "" {
			return strings.TrimSpace(c.Text)
		}
	}
	return ""
}

// yamlScalar quotes a string for YAML if it contains characters that
// require quoting. Minimal escaper for flat key:value headers.
func YamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":\"'\n#") || s[0] == ' ' || s[0] == '-' {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return s
}

// AcpSpawned is the result of one spawn attempt: the run if successful,
// or the error if not. Used by FormatSpawnResult.
type AcpSpawned struct {
	Run *AcpRun
	Err error
}

// FormatSpawnResult builds the YAML tool output for a `subagent` tool
// call. Always async: each item has status + workspace, and a summary
// when the run already finished (not live).
func FormatSpawnResult(results []AcpSpawned) string {
	type item struct {
		ID        string `yaml:"id,omitempty"`
		Status    string `yaml:"status"`
		Workspace string `yaml:"workspace,omitempty"`
		Summary   string `yaml:"summary,omitempty"`
		Error     string `yaml:"error,omitempty"`
		Async     bool   `yaml:"async,omitempty"`
	}
	out := make([]item, 0, len(results))
	for _, r := range results {
		if r.Err != nil {
			out = append(out, item{Status: "failed", Error: r.Err.Error(), Async: true})
			continue
		}
		it := item{ID: r.Run.ID, Status: string(r.Run.Status), Workspace: r.Run.Workspace, Async: true}
		if !r.Run.Live() {
			it.Summary = TranscriptSummary(r.Run)
			it.Error = r.Run.Error
		}
		out = append(out, it)
	}
	b, _ := yaml.Marshal(map[string]any{"runs": out, "async": true, "count": len(out)})
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---"
}
