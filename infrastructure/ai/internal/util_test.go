package aiutil

import "testing"

// TestSanitizeToolName verifies that tool names with characters outside the
// OpenAI Responses API pattern ^[a-zA-Z0-9_-]+$ are replaced with underscores.
// This is a defensive auto-heal: when a model hallucinates a tool name like
// "terminal:exec" or "filesystem.read" (which providers reject with HTTP 400
// "Invalid 'input[N].name': string does not match pattern"), the sanitizer
// rewrites it to a syntactically valid name so the conversation can still be
// replayed. Pairing is unaffected because all three providers (Responses,
// chat-completion, Codex) match function_call ↔ function_call_output by
// call_id, not by name.
func TestSanitizeToolName(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"already valid", "memory_save", "memory_save"},
		{"hyphen and underscore", "my-tool_v2", "my-tool_v2"},
		{"colon", "terminal:exec", "terminal_exec"},
		{"dot", "filesystem.read", "filesystem_read"},
		{"slash", "mcp/server/tool", "mcp_server_tool"},
		{"space", "tool name", "tool_name"},
		{"multiple invalid", "a:b.c d", "a_b_c_d"},
		{"empty", "", ""},
		{"only invalid", "::::", "____"},
		{"unicode", "tööl", "t__l"},
	}
	for _, tc := range cases {
		if got := SanitizeToolName(tc.in); got != tc.want {
			t.Errorf("%s: SanitizeToolName(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
