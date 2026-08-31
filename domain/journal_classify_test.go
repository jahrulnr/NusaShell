package domain

import "testing"

func TestClassifyMutation(t *testing.T) {
	cases := []struct {
		name        string
		toolName    string
		argsJSON    string
		wantClass   MutationClass
		wantPaths   []string
		wantCommand string
		wantCwd     string
	}{
		{
			name:        "exec opaque",
			toolName:    "exec",
			argsJSON:    `{"command":"ls","cwd":"/tmp"}`,
			wantClass:   MutationOpaque,
			wantCommand: "ls",
			wantCwd:     "/tmp",
		},
		{
			name:      "mcp_call unobserved",
			toolName:  "mcp_call",
			argsJSON:  `{}`,
			wantClass: MutationUnobserved,
		},
		{
			name:      "file_write declared single path",
			toolName:  "file_write",
			argsJSON:  `{"path":"/abs/file.txt"}`,
			wantClass: MutationDeclared,
			wantPaths: []string{"/abs/file.txt"},
		},
		{
			name:      "file_patch declared",
			toolName:  "file_patch",
			argsJSON:  `{"path":"/abs/file.go"}`,
			wantClass: MutationDeclared,
			wantPaths: []string{"/abs/file.go"},
		},
		{
			name:      "file_delete declared",
			toolName:  "file_delete",
			argsJSON:  `{"path":"/abs/file.go"}`,
			wantClass: MutationDeclared,
			wantPaths: []string{"/abs/file.go"},
		},
		{
			name:      "file_mkdir declared",
			toolName:  "file_mkdir",
			argsJSON:  `{"path":"/abs/dir"}`,
			wantClass: MutationDeclared,
			wantPaths: []string{"/abs/dir"},
		},
		{
			name:      "file_move declared two paths",
			toolName:  "file_move",
			argsJSON:  `{"source":"/a","destination":"/b"}`,
			wantClass: MutationDeclared,
			wantPaths: []string{"/a", "/b"},
		},
		{
			name:      "file_copy declared two paths",
			toolName:  "file_copy",
			argsJSON:  `{"source":"/a","destination":"/b"}`,
			wantClass: MutationDeclared,
			wantPaths: []string{"/a", "/b"},
		},
		{
			name:      "unknown tool is none",
			toolName:  "memory_project",
			argsJSON:  `{"op":"admit"}`,
			wantClass: MutationNone,
		},
		{
			name:      "file_read is none",
			toolName:  "file_read",
			argsJSON:  `{"path":"/abs/file.go"}`,
			wantClass: MutationNone,
		},
		{
			name:      "malformed json falls back to class only",
			toolName:  "file_write",
			argsJSON:  `{not json`,
			wantClass: MutationDeclared,
			wantPaths: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, paths, command, cwd := ClassifyMutation(tc.toolName, []byte(tc.argsJSON))
			if class != tc.wantClass {
				t.Fatalf("class = %q, want %q", class, tc.wantClass)
			}
			if !sliceEqual(paths, tc.wantPaths) {
				t.Fatalf("paths = %v, want %v", paths, tc.wantPaths)
			}
			if command != tc.wantCommand {
				t.Fatalf("command = %q, want %q", command, tc.wantCommand)
			}
			if cwd != tc.wantCwd {
				t.Fatalf("cwd = %q, want %q", cwd, tc.wantCwd)
			}
		})
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
