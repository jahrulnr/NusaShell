// Package tooloutput wraps and summarizes tool results before they are sent
// to the model. Extracted from the application root so the agent runner
// depends on a small leaf package instead of the whole application package.
package tooloutput

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// delimiterVariantRe matches any spelling of "untrusted_tool_result" or
// "untrusted-tool-result" (case-insensitive) so a malicious payload cannot
// forge an open/close tag to escape the envelope.
var delimiterVariantRe = regexp.MustCompile(`untrusted[-_]tool[-_]result`)

// WrapToolOutput wraps every tool payload in an <untrusted_tool_result>
// envelope before sending it to the model. It does not matter whether the
// source is a local built-in (file_read, exec, grep) or an external MCP
// server — all tool output is treated as untrusted data. The wrapper is
// ephemeral: it is applied only when building provider messages, never
// persisted to the conversation store.
func WrapToolOutput(toolName, rawOutput string) string {
	safe := delimiterVariantRe.ReplaceAllString(rawOutput, "untrusted tool result")
	var sb strings.Builder
	sb.WriteString("<untrusted_tool_result source=\"")
	sb.WriteString(toolName)
	sb.WriteString("\">\n")
	sb.WriteString(safe)
	sb.WriteString("\n</untrusted_tool_result>")
	return sb.String()
}

// SummarizeToolContent returns the tool result content with summarization
// applied (show/subagent tools get short summaries) but WITHOUT the untrusted
// envelope. The caller is responsible for wrapping the result via
// WrapToolOutput after any additional content manipulation (e.g. capability
// filtering notes). This ensures all tool-derived content — including
// appended notes — lands inside the untrusted envelope.
func SummarizeToolContent(toolName, rawOutput string) string {
	if toolName == "show" {
		if summary, ok := summarizeShowOutput(rawOutput); ok {
			return summary
		}
	}
	if toolName == "subagent_wait" || toolName == "subagent_steer" || toolName == "subagent_stop" {
		if summary, ok := summarizeSubagentWaitOutput(rawOutput); ok {
			return summary
		}
		return boundedSubagentOutput(rawOutput)
	}
	return rawOutput
}

// ProviderToolContent returns the tool result content that is sent to the
// provider. For most tools this is the full output wrapped in the untrusted
// envelope. For the show tool the output is replaced with a short summary —
// the show tool is UI-only, the model does not need the payload. (The
// backend no longer embeds base64 data URLs in the output at all; the
// summarizer produces a text description from the metadata fields.) The
// full output is still persisted in tc.Output for the frontend.
func ProviderToolContent(toolName, rawOutput string) string {
	return WrapToolOutput(toolName, SummarizeToolContent(toolName, rawOutput))
}

// summarizeShowOutput parses a show tool result and returns a short text
// summary suitable for the provider. Returns (summary, true) when the
// output is a recognized show result, or (raw, false) when parsing fails
// so the caller falls back to the full output.
func summarizeShowOutput(rawOutput string) (string, bool) {
	// show(op=html) returns { "artifact": { path, width, height, title } }.
	// The HTML body is NOT embedded (it bloats the conversation JSON); the
	// frontend fetches it via /local-file?path=. Legacy outputs that still
	// embed html are accepted for backward compatibility.
	var artifact struct {
		Artifact struct {
			HTML   string `json:"html"`
			Path   string `json:"path"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Title  string `json:"title"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal([]byte(rawOutput), &artifact); err == nil && (artifact.Artifact.HTML != "" || artifact.Artifact.Path != "") {
		title := artifact.Artifact.Title
		if title == "" {
			title = "artifact"
		}
		return fmt.Sprintf("Displayed HTML artifact %q in the UI (%dx%d iframe).", title, artifact.Artifact.Width, artifact.Artifact.Height), true
	}

	// show(op=image|audio|video|pdf) returns { "show": { type, path, name,
	// media_type, size_bytes } }. The src field is no longer embedded by
	// the backend (it bloats the conversation JSON); the frontend loads
	// the file via /local-file?path=. The summarizer only needs type and
	// path to describe the result to the provider.
	var show struct {
		Show struct {
			Type string `json:"type"`
			Src  string `json:"src"`
			Path string `json:"path"`
			Name string `json:"name"`
		} `json:"show"`
	}
	if err := json.Unmarshal([]byte(rawOutput), &show); err == nil && show.Show.Type != "" {
		path := show.Show.Path
		name := show.Show.Name
		if name == "" && path != "" {
			name = path
		}
		switch show.Show.Type {
		case "image":
			return fmt.Sprintf("Displayed image %s in the UI.", quotePath(path)), true
		case "audio":
			return fmt.Sprintf("Displayed audio %s in the UI (path: %s).", quotePath(name), quotePath(path)), true
		case "video":
			return fmt.Sprintf("Displayed video %s in the UI (path: %s).", quotePath(name), quotePath(path)), true
		case "pdf":
			return fmt.Sprintf("Displayed PDF %s in the UI (path: %s).", quotePath(name), quotePath(path)), true
		}
	}
	return rawOutput, false
}

// quotePath wraps a path/name in quotes if non-empty, otherwise returns
// "(unknown)".
func quotePath(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return fmt.Sprintf("%q", s)
}

// summarizeSubagentWaitOutput parses compact subagent_wait frontmatter plus
// its last-turn body. It also accepts the full DTO returned by steer/stop and
// older wait results, but only returns a bounded provider-facing summary.
func summarizeSubagentWaitOutput(rawOutput string) (string, bool) {
	header, compactBody, ok := splitYAMLFrontmatter(rawOutput)
	if !ok {
		return rawOutput, false
	}
	var run struct {
		ID         string `yaml:"id"`
		Status     string `yaml:"status"`
		StopReason string `yaml:"stopreason"`
		Error      string `yaml:"error"`
		Workspace  string `yaml:"workspace"`
		OutputPath string `yaml:"output_path"`
		Transcript []struct {
			Kind       string `yaml:"kind"`
			Text       string `yaml:"text"`
			ToolTitle  string `yaml:"tooltitle"`
			ToolStatus string `yaml:"toolstatus"`
		} `yaml:"transcript"`
	}
	if err := yaml.Unmarshal([]byte(header), &run); err != nil {
		return rawOutput, false
	}

	var sb strings.Builder
	sb.WriteString("Subagent run ")
	if run.ID != "" {
		sb.WriteString(run.ID)
	} else {
		sb.WriteString("(unknown)")
	}
	sb.WriteString(" status: ")
	if run.Status != "" {
		sb.WriteString(run.Status)
	} else {
		sb.WriteString("(unknown)")
	}
	sb.WriteString(".")

	// Take only the LAST text chunk (matching domain.lastTranscriptText);
	// the full transcript is persisted in the run's JSON output file. Drop
	// thought/tool/plan/status/usage noise. Truncate to keep the provider
	// context small.
	text := strings.TrimSpace(compactBody)
	if text == "" {
		for i := len(run.Transcript) - 1; i >= 0; i-- {
			c := run.Transcript[i]
			if c.Kind == "text" && strings.TrimSpace(c.Text) != "" {
				text = strings.TrimSpace(c.Text)
				break
			}
		}
	}
	if text != "" {
		if len(text) > 2000 {
			text = text[:2000] + "…"
		}
		sb.WriteString("\n\nSummary: ")
		sb.WriteString(text)
	} else {
		// No text — fall back to last thought (reasoning) for context.
		thought := ""
		for i := len(run.Transcript) - 1; i >= 0; i-- {
			c := run.Transcript[i]
			if c.Kind == "thought" && strings.TrimSpace(c.Text) != "" {
				thought = strings.TrimSpace(c.Text)
				break
			}
		}
		if thought != "" {
			if len(thought) > 2000 {
				thought = thought[:2000] + "…"
			}
			sb.WriteString("\n\nLast reasoning: ")
			sb.WriteString(thought)
			if run.Status == "failed" {
				errPart := run.Error
				if errPart == "" {
					errPart = run.StopReason
				}
				if errPart != "" {
					sb.WriteString("\n\nError: ")
					sb.WriteString(errPart)
				}
			}
		} else if run.Error != "" {
			sb.WriteString("\n\nError: ")
			sb.WriteString(run.Error)
		} else if run.StopReason != "" {
			sb.WriteString("\n\nStop reason: ")
			sb.WriteString(run.StopReason)
		}
	}

	// Surface the last tool call when there was no text and no thought
	// (mirrors domain.StructuredFallbackSummary) so the parent has a clue.
	if text == "" {
		for i := len(run.Transcript) - 1; i >= 0; i-- {
			c := run.Transcript[i]
			if c.Kind == "tool" && c.ToolTitle != "" {
				sb.WriteString("\n\nLast tool: ")
				sb.WriteString(c.ToolTitle)
				if c.ToolStatus != "" {
					sb.WriteString(" (")
					sb.WriteString(c.ToolStatus)
					sb.WriteString(")")
				}
				break
			}
		}
	}

	if run.Workspace != "" {
		sb.WriteString("\n\nWorkspace: ")
		sb.WriteString(run.Workspace)
	}
	if run.OutputPath != "" {
		sb.WriteString("\n\nOutput path: ")
		sb.WriteString(run.OutputPath)
	}
	return sb.String(), true
}

func splitYAMLFrontmatter(raw string) (header, body string, ok bool) {
	if !strings.HasPrefix(raw, "---\n") {
		return "", "", false
	}
	end := strings.Index(raw[4:], "\n---")
	if end < 0 {
		return "", "", false
	}
	bodyStart := 4 + end + len("\n---")
	return raw[4 : 4+end], strings.TrimSpace(raw[bodyStart:]), true
}

func boundedSubagentOutput(raw string) string {
	const maxChars = 2000
	raw = strings.TrimSpace(raw)
	if len(raw) > maxChars {
		raw = "…" + raw[len(raw)-maxChars:]
	}
	return "Subagent tool output could not be parsed; showing a bounded tail:\n\n" + raw
}
