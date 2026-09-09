package tools

import "testing"

func TestIsInternalToolName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"internal_send_progress", true},
		{"internal_", true},
		{"admin.send_progress", true},
		{"admin.", true},
		{"send_progress", false},
		{"mcp_call", false},
		{"file_read", false},
		{"INTERNAL_SEND", false},
		{"xadmin.send", false},
		{"", false},
		{"  internal_x  ", true},
	}
	for _, tc := range cases {
		if got := IsInternalToolName(tc.name); got != tc.want {
			t.Fatalf("IsInternalToolName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFilterInternalTools(t *testing.T) {
	in := []ToolInfo{
		{Name: "file_read"},
		{Name: "internal_send_progress"},
		{Name: "admin.send_progress"},
		{Name: "mcp_call"},
	}
	out := FilterInternalTools(in)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(out), out)
	}
	if out[0].Name != "file_read" || out[1].Name != "mcp_call" {
		t.Fatalf("unexpected filter result: %+v", out)
	}
}
