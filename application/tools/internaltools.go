package tools

import "strings"

// IsInternalToolName reports host-internal MCP (or built-in) tool names that
// agents must never see or execute. Community MCP servers have no hidden-tool
// concept; NusaShell uses these name patterns as the host-side policy:
//
//   - prefix "internal_" (e.g. internal_send_progress)
//   - namespace "admin."  (e.g. admin.send_progress)
//
// Host code may still invoke them via MCPToolCaller. Manifest-listed
// mcp.internal_tools is a documented follow-up; name patterns are the
// enforced rule today.
func IsInternalToolName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "internal_") {
		return true
	}
	return strings.HasPrefix(name, "admin.")
}

// FilterInternalTools drops host-internal tools from an advertised tool list.
func FilterInternalTools(defs []ToolInfo) []ToolInfo {
	if len(defs) == 0 {
		return defs
	}
	out := make([]ToolInfo, 0, len(defs))
	for _, d := range defs {
		if IsInternalToolName(d.Name) {
			continue
		}
		out = append(out, d)
	}
	return out
}
