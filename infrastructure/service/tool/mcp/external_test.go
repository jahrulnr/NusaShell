package toolmcp

import (
	"strings"
	"testing"
)

// toolName is the wire name providers validate: safe characters only,
// ≤64 bytes, and never leading with a digit — Gemini rejects identifier-
// breaking names with a 400 before the turn starts.
func TestToolName(t *testing.T) {
	for name, tc := range map[string]struct {
		server, tool, want string
	}{
		"plain":       {"srv", "read", "srv_read"},
		"unsafe tool": {"srv", "a/b.c d", "srv_a_b_c_d"},
		"unsafe srv":  {"my srv", "read", "my_srv_read"},
		"digit srv":   {"9srv", "read", "mcp9srv_read"},
	} {
		if got := toolName(tc.server, tc.tool); got != tc.want {
			t.Errorf("%s: toolName(%q, %q) = %q, want %q", name, tc.server, tc.tool, got, tc.want)
		}
	}
	if got := toolName("srv", strings.Repeat("a", 100)); len(got) != 64 {
		t.Fatalf("truncated name = %d bytes, want 64", len(got))
	}
	if got := toolName("9srv", strings.Repeat("a", 100)); len(got) != 64 || got[0] == '9' {
		t.Fatalf("digit-prefixed truncated name = %q", got[:8])
	}
}
