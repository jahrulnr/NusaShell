package domain

import (
	"strings"
	"testing"
)

func TestSkillSaveSupportPath(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		rel     string
		support bool
		err     string
	}{
		{name: "empty is body save", path: ""},
		{name: "whitespace is body save", path: "  \t"},
		{name: "relative SKILL.md is body save", path: "SKILL.md"},
		{name: "skill.md case is body save", path: "skill.md"},
		{
			name: "absolute SKILL.md is body save",
			path: "/home/u/.config/nusashell/skills/learned-tool-mapping/SKILL.md",
		},
		{
			name: "windows absolute SKILL.md is body save",
			path: `C:\Users\u\skills\foo\SKILL.md`,
		},
		{
			name:    "relative support file",
			path:    "references/errors.md",
			rel:     "references/errors.md",
			support: true,
		},
		{
			name:    "backslash support file",
			path:    `templates\draft.md`,
			rel:     "templates/draft.md",
			support: true,
		},
		{
			name: "absolute support file rejected",
			path: "/tmp/errors.md",
			err:  "relative support file",
		},
		{
			name: "parent traversal rejected",
			path: "references/../secrets.md",
			err:  "relative support file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, support, err := SkillSaveSupportPath(tc.path)
			if tc.err != "" {
				if err == nil {
					t.Fatalf("path %q: expected error containing %q", tc.path, tc.err)
				}
				if !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("path %q error = %v, want containing %q", tc.path, err, tc.err)
				}
				if support {
					t.Fatalf("path %q: support=true on error", tc.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("path %q: unexpected error %v", tc.path, err)
			}
			if support != tc.support {
				t.Fatalf("path %q: support=%v, want %v", tc.path, support, tc.support)
			}
			if rel != tc.rel {
				t.Fatalf("path %q: rel=%q, want %q", tc.path, rel, tc.rel)
			}
		})
	}
}
