package application

import (
	"regexp"
	"strings"
)

// delimiterVariantRe matches any spelling of "untrusted_tool_result" or
// "untrusted-tool-result" (case-insensitive) so a malicious payload cannot
// forge an open/close tag to escape the envelope.
var delimiterVariantRe = regexp.MustCompile(`untrusted[-_]tool[-_]result`)

// wrapToolOutput wraps every tool payload in an <untrusted_tool_result>
// envelope before sending it to the model. It does not matter whether the
// source is a local built-in (file_read, exec, grep) or an external MCP
// server — all tool output is treated as untrusted data. The wrapper is
// ephemeral: it is applied only when building provider messages, never
// persisted to the conversation store.
func wrapToolOutput(toolName, rawOutput string) string {
	safe := delimiterVariantRe.ReplaceAllString(rawOutput, "untrusted tool result")
	var sb strings.Builder
	sb.WriteString("<untrusted_tool_result source=\"")
	sb.WriteString(toolName)
	sb.WriteString("\">\n")
	sb.WriteString(safe)
	sb.WriteString("\n</untrusted_tool_result>")
	return sb.String()
}
