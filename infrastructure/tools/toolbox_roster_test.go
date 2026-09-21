package tools

import (
	"testing"

	"nusashell/domain"
)

// TestListToolsRosterGolden pins the advertised tool roster — names and
// their exact order — captured from the pre-registry ListTools. Provider
// prompt caches key on this list, so any name, order, or conditional-
// advertisement change must be a deliberate, reviewed decision.
//
// Four configurations are pinned:
//   - bare: no optional backends
//   - acp: one enabled ACP agent (advertises subagent)
//   - media: image generation configured (advertises generate_media)
//   - webAnswer: web answer provider + stored key (advertises web_answer)
func TestListToolsRosterGolden(t *testing.T) {
	// tool names appended after the typed tools, in order: native file
	// CRUD + grep/find_file/show, exec, then the dispatcher roots.
	tail := []string{
		"file_read", "file_write", "file_patch", "file_list", "file_mkdir",
		"file_delete", "file_move", "file_copy", "file_info",
		"grep", "find_file", "show", "exec",
		"automation", "automation_schedule", "skill", "memory", "docs",
		"memory_project", "conversation",
	}
	base := []string{
		"todo", "ask_question", "wait_until", "sleep",
		"mcp_list", "tool_list", "tool_schema", "mcp_search", "mcp_call",
		"contract_read", "mcp_register", "mcp_enable", "mcp_disable",
		"mcp_unregister", "mcp_install", "mcp_server_add",
		"read_media", "web_search", "web_fetch",
	}
	with := func(extra ...string) []string {
		out := make([]string, 0, len(base)+len(extra)+len(tail))
		out = append(out, base...)
		out = append(out, extra...)
		out = append(out, tail...)
		return out
	}

	cases := []struct {
		name  string
		setup func(t *Toolbox)
		want  []string
	}{
		{name: "bare", want: with()},
		{
			name: "acp",
			setup: func(tb *Toolbox) {
				tb.Acp = &stubAcp{agents: []*domain.AcpAgent{{ID: "acp_1", Name: "Cursor", Enabled: true}}}
			},
			want: with("subagent"),
		},
		{
			name: "media",
			setup: func(tb *Toolbox) {
				tb.Settings = &memToolboxSettings{s: domain.Settings{ImageProviderID: "or", ImageModelID: "openai/gpt-image-2"}}
			},
			want: with("generate_media"),
		},
		{
			name: "webAnswer",
			setup: func(tb *Toolbox) {
				tb.Settings = &memToolboxSettings{s: domain.Settings{WebAnswerProvider: "openrouter", WebAnswerModel: "m"}}
				tb.Credentials = &swTestCreds{keys: map[string]string{"web_answer": "fake-key"}}
			},
			want: with("web_answer"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb := testToolbox(nil, nil, &stubMCP{})
			if tc.setup != nil {
				tc.setup(tb)
			}
			infos := tb.ListTools()
			got := make([]string, len(infos))
			for i, ti := range infos {
				got[i] = ti.Name
				if ti.Description == "" {
					t.Errorf("tool %q has empty Description", ti.Name)
				}
				if len(ti.InputSchema) == 0 {
					t.Errorf("tool %q has empty InputSchema", ti.Name)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("roster length = %d, want %d\ngot:  %v\nwant: %v", len(got), len(tc.want), got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("roster[%d] = %q, want %q\ngot:  %v\nwant: %v", i, got[i], tc.want[i], got, tc.want)
				}
			}
		})
	}
}

// TestListToolsWebAnswerAbsentWithoutConfig pins the conditional
// advertisement: without a configured Web Answer provider + key,
// web_answer must not appear in the bare roster.
func TestListToolsWebAnswerAbsentWithoutConfig(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	for _, ti := range tb.ListTools() {
		if ti.Name == "web_answer" {
			t.Fatal("web_answer must not be advertised without a configured provider and API key")
		}
	}
}
